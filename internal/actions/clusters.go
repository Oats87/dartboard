package actions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/dartboard/internal/dart"
	"github.com/rancher/dartboard/internal/tofu"
	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/tests/actions/clusters"
	rancherclusters "github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/reports"
	"github.com/sirupsen/logrus"

	kubeProvisioning "github.com/rancher/shepherd/clients/provisioning"
	"github.com/rancher/shepherd/clients/rancher"
	mgmtv3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	shepherdclusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	shepherddefaults "github.com/rancher/shepherd/extensions/defaults"
	stevetypes "github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/etcdsnapshot"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	nodestat "github.com/rancher/shepherd/extensions/nodes"
	"github.com/rancher/shepherd/extensions/tokenregistration"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	shepherdnodes "github.com/rancher/shepherd/pkg/nodes"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/shepherd/pkg/wait"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"

	shepclusters "github.com/rancher/shepherd/extensions/clusters"
)

const (
	psactRancherPrivileged string = "rancher-privileged"

	// customClusterReadyMaxRetries caps the IsProvisioningClusterReady watch
	// retry loop in RegisterCustomCluster. Each iteration's effective duration
	// is bounded by the kube-apiserver watch timeout (defaults.WatchTimeoutSeconds,
	// 30 min) but is often cut earlier by Rancher controller restarts under
	// burst registration load. At observed p90 provisioning times of ~25 min
	// for downstream-custom clusters under 50-cluster batches, 30 iterations
	// gives us multi-hour coverage for the slow tail.
	customClusterReadyMaxRetries = 30
)

// NewUpstreamProvisioningClient builds a provisioning.cattle.io/v1 client that
// talks directly to the upstream cluster's kube-apiserver (port 6443), bypassing
// Rancher's steve aggregation layer.
//
// Why: Rancher's steve relay enforces a ~60s idle ceiling on watch response
// streams and does not forward bookmark events, which causes long-running
// cluster-provisioning watches to die with HTTP/2 RST_STREAM INTERNAL_ERROR.
// Going straight to the kube-apiserver (which honors the watch TimeoutSeconds
// and AllowWatchBookmarks contract) avoids that whole failure mode.
func NewUpstreamProvisioningClient(kubeconfigPath string, ts *session.Session) (*kubeProvisioning.Client, error) {
	if kubeconfigPath == "" {
		return nil, fmt.Errorf("upstream kubeconfig path is empty")
	}
	restConfig, err := GetRESTConfigFromPath(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("error building REST config from upstream kubeconfig %s: %w", kubeconfigPath, err)
	}
	return kubeProvisioning.NewForConfig(restConfig, ts)
}

// TODELETE:
// type CustomClusterTemplate struct {
// 	dart.ClusterTemplate
// 	Nodes []tofu.Node `yaml:"nodes"`
// }

// ConvertConfigToClusterConfig converts the ClusterConfig from (user) input to a rancher/tests ClusterConfig
func ConvertConfigToClusterConfig(config *dart.ClusterConfig) *rancherclusters.ClusterConfig {
	var newConfig rancherclusters.ClusterConfig
	for i := range config.MachinePools {
		newConfig.MachinePools[i].Pools = config.MachinePools[i].Pools
		newConfig.MachinePools[i].MachinePoolConfig = machinepools.MachinePoolConfig{
			NodeRoles: machinepools.NodeRoles{
				ControlPlane: config.MachinePools[i].MachinePoolConfig.ControlPlane,
				Etcd:         config.MachinePools[i].MachinePoolConfig.Etcd,
				Worker:       config.MachinePools[i].MachinePoolConfig.Worker,
				Quantity:     config.MachinePools[i].MachinePoolConfig.Quantity,
			},
		}
	}
	newConfig.Providers = &[]string{config.Provider}
	newConfig.PSACT = psactRancherPrivileged
	return &newConfig
}

// GetK3SRKE2Cluster is a "helper" functions that takes a rancher client, and the rke2 cluster config as parameters.
// This function registers a delete cluster function with a wait.WatchWait to ensure the cluster is removed cleanly
func GetK3SRKE2Cluster(client *rancher.Client, config *rancher.Config, cluster *apisV1.Cluster) (*v1.SteveAPIObject, error) {
	const maxAttempts = 5
	const pollTimeout = 2 * time.Minute

	var steveClusterObject *v1.SteveAPIObject
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx := context.Background()
		lastErr = kwait.PollUntilContextTimeout(ctx, 500*time.Millisecond, pollTimeout, true, func(_ context.Context) (done bool, err error) {
			client, err = client.ReLoginForConfig(config)
			if err != nil {
				return false, err
			}

			clusterObjs, err := client.Steve.SteveType(shepherdclusters.ProvisioningSteveResourceType).ListAll(nil)
			if err != nil {
				logrus.Errorf("error when listing clusters from the steve client:  %s: %v", cluster.Name, err)
				return false, nil
			}
			for _, obj := range clusterObjs.Data {
				if obj.Name == cluster.Name {
					steveClusterObject, err = client.Steve.SteveType(shepherdclusters.ProvisioningSteveResourceType).ByID(obj.ID)
					if err != nil {
						return false, nil
					}
					return true, nil
				}
			}
			return false, nil
		})
		if lastErr == nil {
			return steveClusterObject, nil
		}
		logrus.Warnf("GetK3SRKE2Cluster attempt %d/%d for %s failed: %v", attempt, maxAttempts, cluster.Name, lastErr)
	}
	return nil, fmt.Errorf("Could not find expected Cluster %s after %d attempts: %w", cluster.Name, maxAttempts, lastErr)
}

// CreateK3SRKE2Cluster is a "helper" functions that takes a rancher client, and the rke2 cluster config as parameters.
// This function registers a delete cluster function with a wait.WatchWait to ensure the cluster is removed cleanly
func CreateK3SRKE2Cluster(client *rancher.Client, config *rancher.Config, upstreamKubeconfigPath string, cluster *apisV1.Cluster) (*v1.SteveAPIObject, error) {
	clusterObj, err := client.Steve.SteveType(shepherdclusters.ProvisioningSteveResourceType).Create(cluster)
	if err != nil {
		if !strings.Contains(err.Error(), "409 Conflict") {
			return nil, err
		}
		logrus.Errorf("[%s/%s] 409 Conflict found for the cluster, so we'll adopt the existing one.", cluster.Namespace, cluster.Name)
		// we need to find the conflicting cluster
		clusterObj, err = GetK3SRKE2Cluster(client, config, cluster)
		if err != nil {
			return nil, err
		}
	}

	ctx := context.Background()
	err = kwait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 2*time.Minute, true, func(_ context.Context) (done bool, err error) {
		client, err = client.ReLoginForConfig(config)
		if err != nil {
			return false, err
		}

		_, err = client.Steve.SteveType(shepherdclusters.ProvisioningSteveResourceType).ByID(clusterObj.ID)
		if err != nil {
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return nil, err
	}

	client.Session.RegisterCleanupFunc(func() error {
		adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
		if err != nil {
			return err
		}

		provKubeClient, err := NewUpstreamProvisioningClient(upstreamKubeconfigPath, adminClient.Session)
		if err != nil {
			return err
		}

		watchInterface, err := provKubeClient.Clusters(clusterObj.ObjectMeta.Namespace).Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:       "metadata.name=" + clusterObj.ObjectMeta.Name,
			TimeoutSeconds:      &shepherddefaults.WatchTimeoutSeconds,
			AllowWatchBookmarks: true,
		})

		if err != nil {
			return err
		}

		logrus.Infof("[%s/%s] performing relogin on cleanup", clusterObj.ObjectMeta.Namespace, clusterObj.ObjectMeta.Name)
		client, err = client.ReLogin() // TODO is relogin is actually necessary for what we're doing?
		if err != nil {
			return err
		}

		err = client.Steve.SteveType(shepherdclusters.ProvisioningSteveResourceType).Delete(clusterObj)
		if err != nil {
			return err
		}

		return wait.WatchWait(watchInterface, func(event watch.Event) (ready bool, err error) {
			cluster := event.Object.(*apisV1.Cluster)
			if event.Type == watch.Error {
				return false, fmt.Errorf("there was an error deleting cluster %s: %w", cluster.Name, err)
			} else if event.Type == watch.Deleted {
				return true, nil
			} else if cluster == nil {
				return true, nil
			}
			return false, nil
		})
	})

	return clusterObj, nil
}

// createRegistrationCommand is a helper for rke2/k3s custom clusters to create the registration command with advanced options configured per node
func createRegistrationCommand(command, publicIP, privateIP string, machinePool apisV1.RKEMachinePool) string {
	if len(publicIP) > 0 {
		command += fmt.Sprintf(" --address %s", publicIP)
	}
	if len(privateIP) > 0 {
		command += fmt.Sprintf(" --internal-address %s", privateIP)
	}
	for labelKey, labelValue := range machinePool.Labels {
		command += fmt.Sprintf(" --label %s=%s", labelKey, labelValue)
	}
	for _, taint := range machinePool.Taints {
		command += fmt.Sprintf(" --taints %s=%s:%s", taint.Key, taint.Value, taint.Effect)
	}
	return command
}

// RegisterCustomCluster registers a non-rke1 cluster using a 3rd party client for its nodes
func RegisterCustomCluster(client *rancher.Client, config *rancher.Config, upstreamKubeconfigPath string, steveObject *v1.SteveAPIObject, cluster *apisV1.Cluster, nodes []tofu.Node) (*v1.SteveAPIObject, error) {
	quantityPerPool := []int32{}
	rolesPerPool := []string{}
	logrus.Infof("[%s/%s] Running custom cluster registration", cluster.Namespace, cluster.Name)
	for _, pool := range cluster.Spec.RKEConfig.MachinePools {
		var finalRoleCommand string
		if pool.ControlPlaneRole {
			finalRoleCommand += " --controlplane"
		}
		if pool.EtcdRole {
			finalRoleCommand += " --etcd"
		}
		if pool.WorkerRole {
			finalRoleCommand += " --worker"
		}

		quantityPerPool = append(quantityPerPool, *pool.Quantity)
		rolesPerPool = append(rolesPerPool, finalRoleCommand)
	}

	mgmtClusterName := ""
	clusterRetrievalAttempts := 0
	for {
		clusterRetrievalAttempts++

		if clusterRetrievalAttempts == 5 {
			logrus.Debugf("performing relogin during cluster retrieval for cluster")
			var err error
			client, err = client.ReLoginForConfig(config)
			if err != nil {
				return nil, fmt.Errorf("unable to relogin for client (attempts=%d) by ID: %s: %w", clusterRetrievalAttempts, steveObject.ID, err)
			}
		}

		if clusterRetrievalAttempts > 10 {
			return nil, fmt.Errorf("unable to retrieve cluster (attempts=%d) by ID: %s", clusterRetrievalAttempts, steveObject.ID)
		}
		// this appears to get the latest cluster version
		customCluster, err := client.Steve.SteveType(etcdsnapshot.ProvisioningSteveResouceType).ByID(steveObject.ID)
		if err != nil {
			logrus.Errorf("error while retrieving cluster by ID (%s) on attempt %d: %v", steveObject.ID, clusterRetrievalAttempts, err)
			continue
		}

		clusterStatus := &apisV1.ClusterStatus{}
		err = v1.ConvertToK8sType(customCluster.Status, clusterStatus)
		if err != nil {
			logrus.Errorf("error while converting cluster by ID (%s) on attempt %d: %v", steveObject.ID, clusterRetrievalAttempts, err)
			continue
		}
		if clusterStatus.ClusterName != "" {
			mgmtClusterName = clusterStatus.ClusterName
			break
		}
	}

	// GetRegistrationToken polls until a CRT with non-empty Token+ManifestURL
	// appears (or its own internal timeout fires). At 999-cluster scale the
	// leader-side handler tail can run several minutes per cluster, so a
	// timeout here means "token never populated" — distinct from "node never
	// booted" (caught later in the registration command exec) or "cluster
	// never went ready" (caught by the IsProvisioningClusterReady watch).
	token, err := tokenregistration.GetRegistrationToken(client, mgmtClusterName)
	if err != nil {
		return nil, fmt.Errorf("[%s/%s] cluster registration token never populated (mgmt cluster %s): %w", cluster.Namespace, cluster.Name, mgmtClusterName, err)
	}
	if token.InsecureNodeCommand == "" {
		return nil, fmt.Errorf("[%s/%s] cluster registration token populated but InsecureNodeCommand is empty (mgmt cluster %s)", cluster.Namespace, cluster.Name, mgmtClusterName)
	}

	var command string
	totalNodesObserved := 0
	for poolIndex, poolRole := range rolesPerPool {
		for nodeIndex := range int(quantityPerPool[poolIndex]) {
			attempts := 0
			for {
				node := nodes[totalNodesObserved+nodeIndex]

				logrus.Infof("[%s/%s] (%s) Executing registration command for node", cluster.Namespace, cluster.Name, node.Name)
				logrus.Infof("[%s/%s] (%s) Linux nodepool detected, using bash...", cluster.Namespace, cluster.Name, node.Name)

				command = fmt.Sprintf("%s %s", token.InsecureNodeCommand, poolRole)
				command = createRegistrationCommand(command, node.PublicIP, node.PrivateIP, cluster.Spec.RKEConfig.MachinePools[poolIndex])
				logrus.Infof("[%s/%s] (%s) Node command: %s", cluster.Namespace, cluster.Name, node.Name, command)

				nodeSSHKey, err := tofu.ReadBytesFromPath(node.SSHKeyPath)
				if err != nil {
					return nil, fmt.Errorf("[%s/%s] (%s) error getting SSH key for node from (%s): %w", cluster.Namespace, cluster.Name, node.Name, node.SSHKeyPath, err)
				}
				shepherdNode := shepherdnodes.Node{
					PublicIPAddress:  node.PublicIP,
					PrivateIPAddress: node.PrivateIP,
					SSHUser:          node.SSHUser,
					SSHKey:           nodeSSHKey,
				}
				output, err := shepherdNode.ExecuteCommand(command)
				if err != nil {
					return nil, err
				}
				if strings.Contains(output, "rancher-system-agent.service") {
					logrus.Infof("[%s/%s] (%s) (attempt=%d) Executed Output: %s", cluster.Namespace, cluster.Name, node.Name, attempts, output)
					break
				}
				logrus.Errorf("[%s/%s] (%s) (attempt=%d) Executed Output did not contain expected content: %s", cluster.Namespace, cluster.Name, node.Name, attempts, output)
				if attempts == 5 {
					return nil, fmt.Errorf("unable to register node %s for cluster %s/%s as it never contained the expected output!", node.Name, cluster.Namespace, cluster.Name)
				}
				attempts++
			}
		}
		totalNodesObserved += int(quantityPerPool[poolIndex])
	}

	// this order of operations was wrong and causing issues because the watch was started BEFORE the registration which might have led to the cluster going "ready" before the check function got "checked"
	kubeProvisioningClient, err := NewUpstreamProvisioningClient(upstreamKubeconfigPath, client.Session)
	if err != nil {
		return nil, err
	}
	retryIterations := -1
	for {
		retryIterations++
		if retryIterations == customClusterReadyMaxRetries {
			return nil, fmt.Errorf("[%s/%s] IsProvisioningClusterReady max iterations (%d) reached, last err: %w", cluster.Namespace, cluster.Name, customClusterReadyMaxRetries, err)
		} else if err != nil {
			// On each WatchWait failure, do a one-shot Get to disambiguate
			// "watch missed the readiness event" from "cluster is genuinely
			// stuck on Y". If the cluster is already Ready+Updated, treat as
			// success — defends against Rancher leader-restart races where the
			// watch channel closes while the controller is mid-reconciling.
			if got, getErr := kubeProvisioningClient.Clusters(cluster.Namespace).Get(context.Background(), cluster.Name, metav1.GetOptions{}); getErr == nil {
				updatedStatus, updatedReason, updatedMsg, updatedAt := "-", "-", "-", "-"
				for _, c := range got.Status.Conditions {
					if c.Type == "Updated" {
						updatedStatus = string(c.Status)
						updatedReason = c.Reason
						updatedMsg = c.Message
						updatedAt = c.LastUpdateTime
						break
					}
				}
				if got.Status.Ready && updatedStatus == "True" {
					logrus.Infof("[%s/%s] post-watch Get shows cluster is already Ready+Updated (Updated@%s); treating as success", cluster.Namespace, cluster.Name, updatedAt)
					break
				}
				logrus.Errorf("[%s/%s] IsProvisioningClusterReady watch iteration [%d] errored with: %v (current Status.Ready=%v Updated=%s reason=%q msg=%.200q updatedAt=%s)",
					cluster.Namespace, cluster.Name, retryIterations-1, err,
					got.Status.Ready, updatedStatus, updatedReason, updatedMsg, updatedAt)
			} else {
				logrus.Errorf("[%s/%s] IsProvisioningClusterReady watch iteration [%d] errored with: %v (post-watch Get also failed: %v)",
					cluster.Namespace, cluster.Name, retryIterations-1, err, getErr)
			}
			err = nil
		}
		logrus.Infof("[%s/%s] IsProvisioningClusterReady Starting watch [i=%d]", cluster.Namespace, cluster.Name, retryIterations)
		var watchInterface watch.Interface
		watchInterface, err = kubeProvisioningClient.Clusters(cluster.Namespace).Watch(context.Background(), metav1.ListOptions{
			FieldSelector:       "metadata.name=" + cluster.Name,
			TimeoutSeconds:      &defaults.WatchTimeoutSeconds,
			AllowWatchBookmarks: true,
		})
		if err != nil {
			continue
		}
		err = wait.WatchWait(watchInterface, shepherdclusters.IsProvisioningClusterReady)
		if err != nil {
			err = fmt.Errorf("[%s/%s] error encountered during IsProvisioningClusterReady check: %w", cluster.Namespace, cluster.Name, err)
			continue
		}
		break
	}

	logrus.Infof("[%s/%s] Provisioning cluster is now ready", cluster.Namespace, cluster.Name)

	registeredCluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(cluster.Namespace + "/" + cluster.Name)
	if err != nil {
		return nil, fmt.Errorf("[%s/%s] error encountered during provisioning cluster retrieval: %w", cluster.Namespace, cluster.Name, err)
	}
	return registeredCluster, nil
}

// VerifyClusterCreated confirms that the cluster resource exists
func VerifyClusterCreated(client *rancher.Client, name, namespace string) (bool, error) {
	obj, _, err := shepherdclusters.GetProvisioningClusterByName(client, name, namespace)
	if err != nil {
		return false, fmt.Errorf("API error verifying creation of %s: %w", name, err)
	}
	return obj != nil, nil
}

// VerifyClusterImported confirms that the cluster resource is in Ready state
func VerifyClusterImported(client *rancher.Client, name, namespace string) (bool, error) {
	obj, _, err := shepherdclusters.GetProvisioningClusterByName(client, name, namespace)
	if err != nil {
		return false, fmt.Errorf("error getting Cluster by verifying import of %s: %w", name, err)
	}
	// In case the Cluster object was not successfully created in the first place
	if obj == nil {
		return false, nil
	}
	return obj.Status.Ready, nil
}

// VerifyCluster validates that a non-rke1 cluster and its resources are in a good state, matching a given config.
func VerifyCluster(client *rancher.Client, config *rancher.Config, upstreamKubeconfigPath string, cluster *v1.SteveAPIObject) error {
	client, err := client.ReLoginForConfig(config)
	if err != nil {
		return fmt.Errorf("unable to relogin: %w", err)
	}
	logrus.Infof("[%s/%s] RELOGIN CLIENT: %v", cluster.Namespace, cluster.Name, client)
	logrus.Infof("[%s/%s] RANCHER CONFIG: %v", cluster.Namespace, cluster.Name, config)
	logrus.Infof("[%s/%s] CLUSTER OBJECT: %v", cluster.Namespace, cluster.Name, cluster)

	adminClient, err := rancher.NewClientForConfig(client.RancherConfig.AdminToken, config, client.Session)
	if err != nil {
		return fmt.Errorf("unable to create new admin client: %w", err)
	}

	kubeProvisioningClient, err := NewUpstreamProvisioningClient(upstreamKubeconfigPath, adminClient.Session)
	reports.TimeoutClusterReport(cluster, err)
	if err != nil {
		return fmt.Errorf("unable to get kube api provisioning client: %w", err)
	}

	watchInterface, err := kubeProvisioningClient.Clusters(cluster.Namespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:       "metadata.name=" + cluster.Name,
		TimeoutSeconds:      &defaults.WatchTimeoutSeconds,
		AllowWatchBookmarks: true,
	})
	reports.TimeoutClusterReport(cluster, err)
	if err != nil {
		return fmt.Errorf("unable to watch for the cluster: %w", err)
	}

	checkFunc := shepherdclusters.IsProvisioningClusterReady
	err = wait.WatchWait(watchInterface, checkFunc)
	reports.TimeoutClusterReport(cluster, err)
	if err != nil {
		return fmt.Errorf("error while waiting for the provisioning cluster to be ready: %w", err)
	}

	clusterToken, err := clusters.CheckServiceAccountTokenSecret(client, cluster.Name)
	reports.TimeoutClusterReport(cluster, err)
	if err != nil {
		return fmt.Errorf("error while checking the service account token secret: %w", err)
	}
	if !clusterToken {
		logrus.Errorf("cluster %s serviceaccount not found so trying to get cluster object", cluster.Name)
		clusterID, err := shepclusters.GetClusterIDByName(client, cluster.Name)
		if err == nil {
			logrus.Errorf("cluster %s ID by name: %s", cluster.Name, clusterID)
			mgmtCluster, err := client.Management.Cluster.ByID(clusterID)
			if err == nil {
				logrus.Errorf("the cluster object for the cluster %s was: %v", cluster.Name, mgmtCluster)
			} else {
				logrus.Errorf("cluster %s ID %s error: %v", cluster.Name, clusterID, err)
			}
		} else {
			logrus.Errorf("cluster %s error getting ID by name: %v", cluster.Name, err)
		}
		return fmt.Errorf("serviceAccountTokenSecret does not exist in this cluster: %s", cluster.Name)
	}

	clusterID, err := shepclusters.GetClusterIDByName(client, cluster.Name)
	if err == nil {
		logrus.Infof("cluster %s ID by name: %s", cluster.Name, clusterID)
		mgmtCluster, err := client.Management.Cluster.ByID(clusterID)
		if err == nil {
			logrus.Infof("the cluster object for the cluster %s was: %v", cluster.Name, mgmtCluster)
		} else {
			logrus.Errorf("cluster %s ID %s error: %v", cluster.Name, clusterID, err)
		}
	} else {
		logrus.Errorf("cluster %s error getting ID by name: %v", cluster.Name, err)
	}

	err = nodestat.AllMachineReady(client, cluster.ID, defaults.ThirtyMinuteTimeout)
	reports.TimeoutClusterReport(cluster, err)
	if err != nil {
		return fmt.Errorf("error while waiting for machines to be ready: %w", err)
	}

	status := &apisV1.ClusterStatus{}
	err = v1.ConvertToK8sType(cluster.Status, status)
	reports.TimeoutClusterReport(cluster, err)
	if err != nil {
		return fmt.Errorf("error while converting status to K8s type: %w", err)
	}

	clusterSpec := &apisV1.ClusterSpec{}
	err = v1.ConvertToK8sType(cluster.Spec, clusterSpec)
	reports.TimeoutClusterReport(cluster, err)
	if err != nil {
		return fmt.Errorf("error while converting cluster spec to K8s type: %w", err)
	}
	/*
		if clusterSpec.DefaultPodSecurityAdmissionConfigurationTemplateName != "" && len(clusterSpec.DefaultPodSecurityAdmissionConfigurationTemplateName) > 0 {
			err = psact.CreateNginxDeployment(client, status.ClusterName, clusterSpec.DefaultPodSecurityAdmissionConfigurationTemplateName)
			reports.TimeoutClusterReport(cluster, err)
			if err != nil {
				return fmt.Errorf("error while creating nginx deployment: %w", err)
			}
		}
	*/

	/*
		This doesn't work if you have a mirror...
		if clusterSpec.RKEConfig.Registries != nil {
			for registryName := range clusterSpec.RKEConfig.Registries.Configs {
				havePrefix, err := registries.CheckAllClusterPodsForRegistryPrefix(client, status.ClusterName, registryName)
				reports.TimeoutClusterReport(cluster, err)
				if !havePrefix {
					return fmt.Errorf("found cluster (%s) pods that do not have the expected registry prefix %s: %w", status.ClusterName, registryName, err)
				}
				if err != nil {
					return fmt.Errorf("error while checking pods for registry prefix: %w", err)
				}
			}
		}
	*/
	if clusterSpec.LocalClusterAuthEndpoint.Enabled {
		mgmtClusterObject, err := adminClient.Management.Cluster.ByID(status.ClusterName)
		reports.TimeoutClusterReport(cluster, err)
		if err != nil {
			return fmt.Errorf("error while retrieving mgmt cluster by ID: %w", err)
		}
		err = VerifyACE(adminClient, mgmtClusterObject)
		if err != nil {
			return fmt.Errorf("error while verifying ACE: %w", err)
		}
	}

	if status.ClusterName == "c-m-5ndtql67" {
		podErrors := pods.StatusPods(client, status.ClusterName)
		if len(podErrors) > 0 {
			errorStrings := make([]string, len(podErrors))
			for i, e := range podErrors {
				errorStrings[i] = e.Error()
			}
			return fmt.Errorf("encountered pod errors: %s", strings.Join(errorStrings, ";"))
		}
	}
	return nil
}

func VerifyACE(client *rancher.Client, cluster *mgmtv3.Cluster) error {
	client, err := client.ReLogin()
	if err != nil {
		return err
	}

	kubeConfig, err := kubeconfig.GetKubeconfig(client, cluster.ID)
	if err != nil {
		return err
	}

	original, err := client.SwitchContext(cluster.Name, kubeConfig)
	if err != nil {
		return err
	}

	originalResp, err := original.Resource(corev1.SchemeGroupVersion.WithResource("pods")).Namespace("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, pod := range originalResp.Items {
		logrus.Infof("Cluster: (%s) Pod: (%s)", cluster.Name, pod.GetName())
	}

	// each control plane has a context. For ACE, we should check these contexts
	contexts, err := kubeconfig.GetContexts(kubeConfig)
	if err != nil {
		return err
	}
	var contextNames []string
	for context := range contexts {
		if strings.Contains(context, "pool") {
			contextNames = append(contextNames, context)
		}
	}

	for _, contextName := range contextNames {
		dynamic, err := client.SwitchContext(contextName, kubeConfig)
		if err != nil {
			return err
		}
		resp, err := dynamic.Resource(corev1.SchemeGroupVersion.WithResource("pods")).Namespace("").List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return err
		}
		logrus.Infof("Cluster: (%s) - Switched Context to (%s)", cluster.Name, contextName)
		for _, pod := range resp.Items {
			logrus.Infof("Cluster: (%s) Context: (%s) Pod: (%s)", cluster.Name, contextName, pod.GetName())
		}
	}
	return nil
}
