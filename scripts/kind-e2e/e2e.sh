#!/usr/bin/env bash
set -e -o pipefail

source ${SCRIPTS_DIR}/lib/debug_functions
source ${SCRIPTS_DIR}/lib/deploy_funcs
source ${SCRIPTS_DIR}/lib/version
source ${SCRIPTS_DIR}/lib/utils
source ${SCRIPTS_DIR}/lib/cluster_settings

### Functions ###

function update_coredns_deployment() {
    echo "Updating coredns to patched image}..."
    kubectl set image -n kube-system deploy/coredns coredns=localhost:5000/lighthouse-coredns:local
    echo "Waiting for coredns deployment to be Ready on cluster${i}."
    kubectl rollout status -n kube-system deploy/coredns --timeout=60s
    echo "Updating coredns clusterrole in to cluster${i}..."
    cat <(kubectl get clusterrole system:coredns -n kube-system -o yaml) ${PRJ_ROOT}/scripts/kind-e2e/config/patch-coredns-clusterrole.yaml >/tmp/clusterroledns-${cluster}.yaml
    kubectl apply -n kube-system -f /tmp/clusterroledns-${cluster}.yaml
}

function update_coredns_configmap() {
    kubectl -n kube-system replace -f ${PRJ_ROOT}/scripts/kind-e2e/config/coredns-cm.yaml
    kubectl -n kube-system describe cm coredns
}

function install_service_export() {
    kubectl apply -f ${PRJ_ROOT}/package/lighthouse-crd.yaml
    kubectl get crds | grep serviceexport
}

function test_with_e2e_tests {
    cd ${DAPPER_SOURCE}/test/e2e

    go test -args -ginkgo.v -ginkgo.randomizeAllSpecs \
         -dp-context cluster1  -dp-context cluster2 -dp-context cluster3  \
        -report-dir ${DAPPER_OUTPUT}/junit 2>&1 | \
        tee ${DAPPER_OUTPUT}/e2e-tests.log
}

### Main ###

declare_kubeconfig
PRJ_ROOT=$(git rev-parse --show-toplevel)

run_subm_clusters update_coredns_configmap
import_image lighthouse-coredns
run_subm_clusters update_coredns_deployment

run_subm_clusters install_service_export

test_with_e2e_tests

cat << EOM
Your 3 virtual clusters are deployed and working properly with your local source code, and can be accessed with:

export KUBECONFIG=\$(echo \$(git rev-parse --show-toplevel)/output/kubeconfigs/kind-config-cluster{1..3} | sed 's/ /:/g')

$ kubectl config use-context cluster1 # or cluster2, cluster3..

To clean evertyhing up, just run: make cleanup
EOM

