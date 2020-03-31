#!/bin/bash
set -e -x

trap "exit 1" SIGTERM SIGINT

if [ "${LIGHTHOUSE_DEBUG}" == "true" ]; then
    DEBUG="--debug -v=9"
else
    DEBUG="-v=4"
fi

exec lighthouse-agent ${DEBUG} -alsologtostderr