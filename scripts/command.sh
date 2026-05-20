#!/bin/bash

set -e

LOG_DIR="${1}"
RUN_TRACKER="${2}"
LOG_PREFIX="${3}"
LOG_SUFFIX="${4}"
NEW_RUN="${5}"
shift 5
CMD=("${@}")

if [ -z "${LOG_DIR}" ] || [ -z "${RUN_TRACKER}" ] || [ -z "${LOG_PREFIX}" ] || [ -z "${LOG_SUFFIX}" ] || [ -z "${CMD}" ]; then
  echo "Incorrect syntax! Expected format: $0 <LOG_DIR> <RUN_TRACKER> <LOG_PREFIX> <LOG_SUFFIX> <NEW_RUN> <CMD>";
  exit 1;
fi

# Create log directory if it doesn't exist
mkdir -p "${LOG_DIR}"

RUN_TRACKER_PATH="${LOG_DIR}/${RUN_TRACKER}"

if [ -f "${RUN_TRACKER_PATH}" ]; then
  RUN_COUNT_FILE_CONTENT=$(cat "${RUN_TRACKER_PATH}");
  case "${RUN_COUNT_FILE_CONTENT}" in
      ''|*[!0-9]*)
        echo "Run count found in ${RUN_TRACKER_PATH} is invalid (${RUN_COUNT_FILE_CONTENT})! Please fix or delete this file before trying again.";
        exit 1;
      ;;
      *)
        if [ "${NEW_RUN}" == "true" ]; then
            RUN_COUNT=$(("${RUN_COUNT_FILE_CONTENT}" + 1))
          else
            RUN_COUNT=$(("${RUN_COUNT_FILE_CONTENT}"))
        fi
      ;;
  esac
  else
  RUN_COUNT=0;
fi

MAX_ATTEMPTS=10

for ATTEMPT in $(seq 0 ${MAX_ATTEMPTS}); do
  CMD_LOG_PATH="${LOG_DIR}/${LOG_PREFIX}-${RUN_COUNT}.${ATTEMPT}${LOG_SUFFIX}"
  if [ ! -f "${CMD_LOG_PATH}" ]; then
    break;
  fi
  if [ "${ATTEMPT}" == "${MAX_ATTEMPTS}" ]; then
    echo "Maximum attempts have been reached for this run.";
    exit 1;
  fi
done

CMD_LATEST_LOG_LINK_PATH="${LOG_DIR}/last-${LOG_PREFIX}${LOG_SUFFIX}"

echo "${RUN_COUNT}" > ${RUN_TRACKER_PATH}

LOG_MSG="Running (${CMD[*]}) [${RUN_COUNT}] @ $(date)"
echo "${LOG_MSG}" > "${CMD_LOG_PATH}"
echo "${LOG_MSG}";
echo "Writing to log file: ${CMD_LOG_PATH}";
rm -f ${CMD_LATEST_LOG_LINK_PATH}
ln -s $(pwd)/${CMD_LOG_PATH} ${CMD_LATEST_LOG_LINK_PATH}

echo "Linking latest log file to: ${CMD_LATEST_LOG_LINK_PATH}";

# Check if screen is available
if ! command -v screen &> /dev/null; then
  echo "Warning: 'screen' command not found. Running command directly without screen."
  echo "Install screen for better session management: apt-get install screen"
  ${CMD[@]} 2>&1 | tee "${CMD_LOG_PATH}"
  exit $?
fi

# Run command in screen session, capturing exit code via a sentinel file
SCREEN_SESSION="${LOG_PREFIX}-${RUN_COUNT}.${ATTEMPT}"
EXIT_CODE_FILE="${LOG_DIR}/${LOG_PREFIX}-${RUN_COUNT}.${ATTEMPT}.exitcode"
rm -f "${EXIT_CODE_FILE}"
screen -S "${SCREEN_SESSION}" -d -m -L -Logfile "${CMD_LOG_PATH}" \
  bash -c "${CMD[*]}; echo \$? > \"${EXIT_CODE_FILE}\""

echo "Command running in screen session: ${SCREEN_SESSION}"
echo "To reattach: screen -r ${SCREEN_SESSION}"
echo "To list sessions: screen -ls"
echo ""
echo "Following log output (Ctrl+C to stop following, command continues in background):"
echo "----------------------------------------"

# Give screen a moment to start
sleep 1

SCREEN_PID=$(screen -ls | awk -v s="${SCREEN_SESSION}" '$0 ~ s {split($1,a,"."); print a[1]; exit}')

# Follow the log file. tail --pid makes it auto-stop when the screen session
# exits, so the user doesn't have to Ctrl+C. If the wrapped command finished
# before we could read the screen PID (e.g. it was nearly instantaneous),
# skip tail entirely and go straight to reading the sentinel.
#
# Install an INT trap before tail: SIGINT is delivered to the whole foreground
# process group, so without a trap bash itself would exit (set +e does not
# help — it only suppresses errexit, not signal-induced termination). With the
# trap, Ctrl+C stops the follow and lets us exit cleanly, releasing the user's
# terminal so they can disconnect from the host. The screen session was
# started detached (-d -m), so it keeps running independently.
INTERRUPTED=0
on_int() {
  INTERRUPTED=1
  echo ""
  echo "Detached from log follow. Command continues in screen session: ${SCREEN_SESSION}"
  echo "  Reattach:  screen -r ${SCREEN_SESSION}"
  echo "  Log file:  ${CMD_LATEST_LOG_LINK_PATH}"
  echo "  Exit code: ${EXIT_CODE_FILE} (written when the command finishes)"
}
trap on_int INT

set +e
if [ -n "${SCREEN_PID}" ]; then
  tail --pid="${SCREEN_PID}" -f "${CMD_LATEST_LOG_LINK_PATH}"
fi
set -e

trap - INT

if [ "${INTERRUPTED}" -eq 1 ]; then
  exit 130
fi

if [ -f "${EXIT_CODE_FILE}" ]; then
  EXIT_CODE=$(cat "${EXIT_CODE_FILE}")
  rm -f "${EXIT_CODE_FILE}"
else
  echo "Warning: exit-code sentinel ${EXIT_CODE_FILE} not found; assuming failure" >&2
  EXIT_CODE=1
fi
exit "${EXIT_CODE}"
