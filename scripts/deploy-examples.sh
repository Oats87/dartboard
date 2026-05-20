#!/bin/bash

# Example usage patterns for deploy.sh and destroy.sh scripts
# This file is for reference only - copy commands to use them

cat <<'EOF'
==============================================================================
DARTBOARD DEPLOY & DESTROY EXAMPLES
==============================================================================

The deploy.sh and destroy.sh scripts now share state via a lockfile
(logs/.dartboard-state) to ensure you destroy the same infrastructure you
deployed, preventing common misconfigurations.

------------------------------------------------------------------------------
BASIC WORKFLOW (Recommended)
------------------------------------------------------------------------------

1. Deploy with a dart file:
   ./scripts/deploy.sh -d darts/prairie-custom-huge-upstream.yaml

2. Destroy (automatically uses the same dart from step 1):
   ./scripts/destroy.sh

The destroy script will show you what dart was used for deployment and when.

------------------------------------------------------------------------------
DEPLOY EXAMPLES
------------------------------------------------------------------------------

1. Basic deploy with default dart file (darts/k3d.yaml):
   ./scripts/deploy.sh

2. Deploy with specific dart file:
   ./scripts/deploy.sh -d darts/aws.yaml
   ./scripts/deploy.sh -d darts/prairie-custom-huge-upstream.yaml

3. Deploy using environment variable:
   DART=darts/azure.yaml ./scripts/deploy.sh

4. Deploy with flags (skip charts):
   ./scripts/deploy.sh --skip-charts
   ./scripts/deploy.sh -d darts/harvester.yaml --skip-charts

5. Deploy with multiple flags:
   ./scripts/deploy.sh --skip-apply --skip-refresh

6. Custom log directory:
   LOG_DIR=my-logs ./scripts/deploy.sh -d darts/aws.yaml
   ./scripts/deploy.sh --log-dir custom-logs

7. Set default dart in your environment (add to ~/.bashrc or ~/.profile):
   export DART=darts/prairie-custom-huge-upstream.yaml
   # Then just run:
   ./scripts/deploy.sh

------------------------------------------------------------------------------
DESTROY EXAMPLES
------------------------------------------------------------------------------

1. Destroy using dart from last deployment (recommended):
   ./scripts/destroy.sh

2. Destroy with confirmation prompt (default):
   ./scripts/destroy.sh
   # You'll be prompted: "Are you sure you want to destroy? (yes/no)"

3. Force destroy without confirmation:
   ./scripts/destroy.sh --force

4. Override with specific dart file (not recommended):
   ./scripts/destroy.sh -d darts/different.yaml
   # Warning: This will warn you if it differs from deployment!

5. Destroy with environment variable override:
   DART=darts/azure.yaml ./scripts/destroy.sh

------------------------------------------------------------------------------
WORKFLOW EXAMPLES
------------------------------------------------------------------------------

# Full cycle - deploy and destroy
./scripts/deploy.sh -d darts/aws.yaml
# ... do your testing ...
./scripts/destroy.sh

# Deploy, redeploy, destroy (same dart automatically)
./scripts/deploy.sh -d darts/k3d.yaml
./scripts/deploy.sh  # redeploy with same dart (uses state file)
./scripts/destroy.sh # destroy with same dart

# Deploy with skip-charts, then destroy
./scripts/deploy.sh -d darts/harvester.yaml --skip-charts
./scripts/destroy.sh

# Multiple environments (use different log directories)
LOG_DIR=logs-dev ./scripts/deploy.sh -d darts/k3d.yaml
LOG_DIR=logs-staging ./scripts/deploy.sh -d darts/aws.yaml
# Later:
LOG_DIR=logs-dev ./scripts/destroy.sh
LOG_DIR=logs-staging ./scripts/destroy.sh

------------------------------------------------------------------------------
STATE FILE INFORMATION
------------------------------------------------------------------------------

State file location: logs/.dartboard-state

View current deployment state:
   cat logs/.dartboard-state

The state file contains:
  - DART: Path to the dart file used for deployment
  - TIMESTAMP: When the deployment occurred
  - DEPLOYED_BY: User who deployed

This file is automatically:
  - Created by deploy.sh
  - Read by destroy.sh
  - Deleted after successful destroy

------------------------------------------------------------------------------
TROUBLESHOOTING
------------------------------------------------------------------------------

Check what dart was last deployed:
   cat logs/.dartboard-state | grep "^DART="

List available dart files:
   ls -1 darts/*.yaml

View help:
   ./scripts/deploy.sh --help
   ./scripts/destroy.sh --help

Manually clean state (if needed):
   rm logs/.dartboard-state

Check if screen session is still running:
   screen -ls
   screen -r deploy-<run-number>  # reattach to session

==============================================================================
EOF
