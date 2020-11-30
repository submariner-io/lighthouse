#!/bin/bash
set -e -x

trap "exit 1" SIGTERM SIGINT

LIGHTHOUSE_VERBOSITY=${LIGHTHOUSE_VERBOSITY:-1}

if [ "${LIGHTHOUSE_DEBUG}" == "true" ]; then
    DEBUG="-v=3"
else
    DEBUG="-v=${LIGHTHOUSE_VERBOSITY}"
fi
DEBUG="-v=5"
exec lighthouse-agent ${DEBUG} -alsologtostderr
