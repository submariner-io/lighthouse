#!/usr/bin/env bash
set -e -o pipefail

source ${SCRIPTS_DIR}/lib/debug_functions
source ${SCRIPTS_DIR}/lib/utils

### Functions ###

function test_with_e2e_tests {
    set -o pipefail

    cd ${DAPPER_SOURCE}/test/e2e

    go test -args -ginkgo.v -ginkgo.randomizeAllSpecs \
         -dp-context cluster1  -dp-context cluster2 -dp-context cluster3  \
        -report-dir ${DAPPER_OUTPUT}/junit 2>&1 | \
        tee ${DAPPER_OUTPUT}/e2e-tests.log
}

### Main ###

declare_kubeconfig

test_with_e2e_tests

cat << EOM
Your 3 virtual clusters are deployed and working properly with your local source code, and can be accessed with:

export KUBECONFIG=\$(echo \$(git rev-parse --show-toplevel)/output/kubeconfigs/kind-config-cluster{1..3} | sed 's/ /:/g')

$ kubectl config use-context cluster1 # or cluster2, cluster3..

To clean evertyhing up, just run: make cleanup
EOM

