#!/bin/sh
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
if [ -f "${SCRIPT_DIR}/../autorun.sh" ]; then
    sh "${SCRIPT_DIR}/../autorun.sh" "$@"
else
    sh "${SCRIPT_DIR}/autorun.sh" "$@"
fi
