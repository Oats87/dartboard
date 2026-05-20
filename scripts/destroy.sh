#!/bin/bash

set -e

# Script to destroy dartboard infrastructure with logging
# Usage: ./scripts/destroy.sh [options]
# Environment variables:
#   DART              - Path to dart file (default: from state file or darts/k3d.yaml)
#   LOG_DIR           - Directory for logs (default: logs)
#   DARTBOARD_BIN     - Path to dartboard binary (default: ./dartboard)

# Configuration with defaults
LOG_DIR="${LOG_DIR:-logs}"
RUN_TRACKER=".deploy-count"
LOG_PREFIX="destroy"
LOG_SUFFIX=".log"
DARTBOARD_BIN="${DARTBOARD_BIN:-./dartboard}"
STATE_FILE="${LOG_DIR}/.dartboard-state"

# Try to load dart from state file first
DART_FROM_STATE=""
if [ -f "${STATE_FILE}" ]; then
  # Source the state file to get DART variable
  source "${STATE_FILE}"
  DART_FROM_STATE="${DART}"
fi

# Use environment variable or command line arg, otherwise fall back to state file
DART="${DART:-${DART_FROM_STATE}}"
DART="${DART:-darts/k3d.yaml}"  # Final fallback

# Parse command line arguments
SHOW_HELP=false
FORCE=false
while [[ $# -gt 0 ]]; do
  case $1 in
    -d|--dart)
      DART="$2"
      shift 2
      ;;
    --log-dir)
      LOG_DIR="$2"
      shift 2
      ;;
    -f|--force)
      FORCE=true
      shift
      ;;
    -h|--help)
      SHOW_HELP=true
      shift
      ;;
    *)
      echo "Unknown option: $1"
      echo "Use --help for usage information"
      exit 1
      ;;
  esac
done

# Show help if requested
if [ "$SHOW_HELP" = true ]; then
  cat <<EOF
Usage: $0 [options]

Options:
  -d, --dart PATH       Path to dart file (default: from state file or \$DART or darts/k3d.yaml)
  --log-dir DIR         Directory for logs (default: \$LOG_DIR or logs)
  -f, --force           Skip confirmation prompt
  -h, --help            Show this help message

Environment Variables:
  DART                  Override dart file path
  LOG_DIR               Default log directory
  DARTBOARD_BIN         Path to dartboard binary (default: ./dartboard)

State File:
  This script automatically uses the dart file from the last deployment
  saved in ${STATE_FILE}
  Override with -d flag or DART environment variable if needed.

Examples:
  # Destroy using the dart from last deployment (recommended)
  $0

  # Destroy with specific dart file
  $0 -d darts/aws.yaml

  # Force destroy without confirmation
  $0 --force

  # Use environment variable to override
  DART=darts/azure.yaml $0
EOF
  exit 0
fi

# Validate dartboard binary exists
if [ ! -f "${DARTBOARD_BIN}" ]; then
  echo "Error: Dartboard binary not found at ${DARTBOARD_BIN}"
  echo "Build it with: make build"
  exit 1
fi

# Validate dart file exists
if [ ! -f "${DART}" ]; then
  echo "Error: Dart file not found at ${DART}"
  echo "Available dart files:"
  ls -1 darts/*.yaml 2>/dev/null || echo "  (none found in darts/)"
  exit 1
fi

# Create log directory if it doesn't exist
mkdir -p "${LOG_DIR}"

# Show what will be destroyed
echo "=================================================="
echo "DESTROY INFRASTRUCTURE"
echo "=================================================="
echo "Dart file: ${DART}"

if [ -f "${STATE_FILE}" ] && [ "${DART}" = "${DART_FROM_STATE}" ]; then
  echo "Source: State file (from last deployment)"
  if [ -n "${DART_FROM_STATE}" ]; then
    echo ""
    echo "Last deployment info:"
    grep "^TIMESTAMP=" "${STATE_FILE}" | sed 's/TIMESTAMP=/  Deployed at: /'
    grep "^DEPLOYED_BY=" "${STATE_FILE}" | sed 's/DEPLOYED_BY=/  Deployed by: /'
  fi
elif [ "${DART}" != "${DART_FROM_STATE}" ] && [ -n "${DART_FROM_STATE}" ]; then
  echo "Source: Manual override"
  echo ""
  echo "WARNING: This dart differs from the last deployment!"
  echo "  Last deployed dart: ${DART_FROM_STATE}"
  echo "  Destroying with:    ${DART}"
  echo ""
  echo "This may cause issues if infrastructure was deployed with a different dart."
else
  echo "Source: Manual specification (no deployment state found)"
fi

echo "Logs will be written to: ${LOG_DIR}/"
echo "=================================================="

# Confirmation prompt unless --force is used
if [ "$FORCE" = false ]; then
  echo ""
  read -p "Are you sure you want to destroy this infrastructure? (yes/no): " CONFIRM
  if [ "$CONFIRM" != "yes" ]; then
    echo "Destroy cancelled."
    exit 0
  fi
fi

# Build the command
CMD="${DARTBOARD_BIN} -d ${DART} destroy"

echo ""
echo "Command: ${CMD}"
echo ""

# Execute via command.sh
./scripts/command.sh "${LOG_DIR}" "${RUN_TRACKER}" "${LOG_PREFIX}" "${LOG_SUFFIX}" "false" ${CMD}

# On successful completion, clean up state file
if [ $? -eq 0 ] && [ -f "${STATE_FILE}" ]; then
  echo ""
  echo "Cleaning up state file..."
  rm -f "${STATE_FILE}"
fi