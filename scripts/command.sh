#!/bin/bash

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
#${CMD[@]} &> "${CMD_LOG_PATH}"
screen -S ${LOG_PREFIX}-${RUN_COUNT}.${ATTEMPT}${LOG_SUFFIX} -d -m -L -Logfile ${CMD_LOG_PATH} ${CMD[@]}
#screen -S ${LOG_PREFIX}-${RUN_COUNT}.${ATTEMPT}${LOG_SUFFIX} -d -m bash -c "${CMD[@]} 2>&1 tee ${CMD_LOG_PATH}"
tail -f ${CMD_LATEST_LOG_LINK_PATH}
