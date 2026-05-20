package actions

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/rancher/dartboard/internal/dart"
	"github.com/rancher/dartboard/internal/tofu"
	"github.com/rancher/shepherd/clients/rancher"
	shepherddefaults "github.com/rancher/shepherd/extensions/defaults"
	"github.com/sirupsen/logrus"
)

var maxWorkers = runtime.GOMAXPROCS(0) * 2

// Mutex to sync map[string]*ClusterStatus mutations and state file writes
var stateMutex sync.Mutex

// stateUpdate is a simple "signaling" struct for the file writer goroutine to persist state
type stateUpdate struct {
	Name      string
	Stage     Stage
	Completed time.Time
}

type jobResult struct {
	skipped bool
	err     error
}

type JobDataTypes interface {
	tofu.Cluster | dart.ClusterTemplate | tofu.CustomCluster
}

// SequencedBatchRunner contains all the channels and WaitGroups needed
// for processing a batch of Clusters concurrently with sequenced state updates
type SequencedBatchRunner[J JobDataTypes] struct {
	// Channel to sequence updates
	seqCh chan struct{}
	// Channel for all write requests
	Updates chan stateUpdate
	// Channel for batch jobs
	Jobs chan J
	// Channel for each individual job's results/error output
	Results chan jobResult

	// Path to the upstream cluster kubeconfig — used to build provisioning
	// watch clients that bypass Rancher's steve aggregation (which has a ~60s
	// idle ceiling on watch streams and drops bookmark events).
	UpstreamKubeconfigPath string

	// WaitGroups for Job workers and the Updates channel which sequences writes to the ClustarStatus state file
	wgWorkers sync.WaitGroup
	wgWriter  sync.WaitGroup
}

// NewSequencedBatchRunner constructs a new runner for one batch
func NewSequencedBatchRunner[J JobDataTypes](batchSize int) *SequencedBatchRunner[J] {
	br := &SequencedBatchRunner[J]{
		Updates: make(chan stateUpdate, batchSize*3),
		seqCh:   make(chan struct{}, 1),
		Jobs:    make(chan J, batchSize),
		Results: make(chan jobResult, batchSize),
	}
	// seed the sequencer
	br.seqCh <- struct{}{}
	return br
}

// Run executes the batch: starts the file writer, workers, enqueues jobs, collects results
func (br *SequencedBatchRunner[J]) Run(batch []J,
	statuses map[string]*ClusterStatus, statePath string, client *rancher.Client,
	config *rancher.Config) error {
	// Start writer
	br.wgWriter.Add(1)
	go br.writer(statuses, statePath)

	// Spawn workers
	for range maxWorkers {
		br.wgWorkers.Add(1)
		go br.worker(statuses, client, config)
	}

	// Enqueue and close jobs
	for _, c := range batch {
		br.Jobs <- c
	}
	close(br.Jobs)

	// Collect ALL results, even after failures, so one slow/failed cluster
	// doesn't waste the rest of the batch's infrastructure spend. Aggregate
	// errors with errors.Join and return them after every job has reported in.
	numSkipped := 0
	var jobErrs []error
	for range batch {
		res := <-br.Results
		if res.err != nil {
			jobErrs = append(jobErrs, res.err)
			continue
		}
		if res.skipped {
			numSkipped++
		}
	}
	// Decide sleep based on completed batch state
	sleepAfter := numSkipped < len(batch)/2

	// After finishing this batch:
	if len(jobErrs) > 0 {
		logrus.Errorf("Batch summary: %d/%d jobs failed, %d skipped, %d succeeded",
			len(jobErrs), len(batch), numSkipped, len(batch)-len(jobErrs)-numSkipped)
		for i, e := range jobErrs {
			logrus.Errorf("  failure [%d/%d]: %v", i+1, len(jobErrs), e)
		}
	} else if sleepAfter {
		logrus.Infof("Batch done: %d/%d skipped; sleeping before next batch.\n", numSkipped, len(batch))
		time.Sleep(shepherddefaults.TwoMinuteTimeout)
	} else {
		logrus.Infof("Batch done: %d/%d skipped; continuing without sleep.\n", numSkipped, len(batch))
	}

	// Clean up
	br.Wait()
	if len(jobErrs) > 0 {
		return fmt.Errorf("batch had %d failures: %w", len(jobErrs), errors.Join(jobErrs...))
	}
	return nil
}

func (br *SequencedBatchRunner[J]) Wait() {
	br.wgWorkers.Wait()
	close(br.Updates)
	br.wgWriter.Wait()
	close(br.Results)
}

// writer serializes all state updates and persists immediately
func (br *SequencedBatchRunner[J]) writer(statuses map[string]*ClusterStatus, statePath string) {
	defer br.wgWriter.Done()
	for u := range br.Updates {
		stateMutex.Lock()
		logrus.Debugf("\nIN WRITER\n")
		logrus.Debugf("\n%v\n", statuses)
		cs := statuses[u.Name]
		cs.Stage = u.Stage
		switch u.Stage {
		case StageNew:
			cs.New = true
		case StageInfra:
			cs.Infra = true
		case StageCreated:
			cs.Created = true
		case StageImported:
			cs.Imported = true
		case StageProvisioned:
			cs.Provisioned = true
		case StageRegistered:
			cs.Registered = true
		}
		if err := SaveClusterState(statePath, statuses); err != nil {
			logrus.Errorf("failed to save state for %s:%s: %v", u.Name, u.Stage, err)
		}
		stateMutex.Unlock()
	}
}

// worker consumes Jobs, calls the proper handler based on the Job Type, signals Updates and Results
func (br *SequencedBatchRunner[J]) worker(statuses map[string]*ClusterStatus,
	client *rancher.Client, config *rancher.Config) {
	defer br.wgWorkers.Done()
	for job := range br.Jobs {
		var skipped bool
		var err error
		// Use type assertion to determine which function to call
		switch typedJob := any(job).(type) {
		case tofu.Cluster:
			skipped, err = importClusterWithRunner(br, typedJob, statuses, client, config, br.UpstreamKubeconfigPath)
		case dart.ClusterTemplate:
			skipped, err = provisionClusterWithRunner(br, typedJob, statuses, client)
		case tofu.CustomCluster:
			skipped, err = registerCustomClusterWithRunner(br, typedJob, statuses, client, config, br.UpstreamKubeconfigPath)
		default:
			err = fmt.Errorf("unsupported job type: %T", job)
		}
		// Always continue draining the Jobs channel even after a failure —
		// the batch result loop in Run() aggregates errors across all jobs
		// rather than aborting on the first one. Returning here would leave
		// queued jobs unprocessed and stall the result collector.
		br.Results <- jobResult{skipped: skipped, err: err}
	}
}
