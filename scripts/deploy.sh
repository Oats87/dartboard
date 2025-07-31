#!/bin/bash

LOG_DIR="logs"
RUN_TRACKER=".deploy-count"
LOG_PREFIX="deploy"
LOG_SUFFIX=".log"
CMD="./dartboard -d darts/prairie-custom-huge.yaml deploy"

./scripts/command.sh ${LOG_DIR} ${RUN_TRACKER} ${LOG_PREFIX} ${LOG_SUFFIX} "true" ${CMD}