#!/usr/bin/env bash

## Process command line flags ##

source /usr/share/shflags/shflags
DEFINE_string 'kubefed' 'false' "Deploy with kubefed"
DEFINE_string 'lhmode' '' "Mode to deploy (plugin or not)"
DEFINE_string 'logging' 'false' "Deploy with logging"
DEFINE_string 'status' 'onetime' "Status flag (onetime, create, keep, clean)"
FLAGS "$@" || exit $?
eval set -- "${FLAGS_ARGV}"

kubefed="${FLAGS_kubefed}"
lhmode="${FLAGS_lhmode}"
logging="${FLAGS_logging}"
status="${FLAGS_status}"
echo "Running with: kubefed=${kubefed}, logging=${logging}, status=${status}"

set -em

source ${SCRIPTS_DIR}/lib/debug_functions
source ${SCRIPTS_DIR}/lib/version
source ${SCRIPTS_DIR}/lib/utils

### Functions ###

function setup_lighthouse () {
    for i in 1 2 3; do
      echo "Installing lighthouse CRD in cluster${i}..."
      kubectl --context=cluster${i} apply -f ${PRJ_ROOT}/package/lighthouse-crd.yaml
    done
    for i in 2 3; do
      echo "Installing lighthouse-agent in cluster${i}..."
      docker tag lighthouse-agent:${VERSION} lighthouse-agent:local
      kind --name cluster${i} load docker-image lighthouse-agent:local
      kubectl --context=cluster${i} apply -f ${PRJ_ROOT}/package/lighthouse-agent-deployment.yaml
    done
}

function update_coredns_deployment() {
    uuid=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w 8 | head -n 1)
    docker tag lighthouse-coredns:${VERSION} lighthouse-coredns:${VERSION}_${uuid}
    for i in 1 2 3; do
        echo "Updating coredns images in to cluster${i}..."
        kind --name cluster${i} load docker-image lighthouse-coredns:${VERSION}_${uuid} --loglevel=debug
        kubectl --context=cluster${i} set image -n kube-system deploy/coredns coredns=lighthouse-coredns:${VERSION}_${uuid}
        echo "Waiting for coredns deployment to be Ready on cluster${i}."
        kubectl --context=cluster${i} rollout status -n kube-system deploy/coredns --timeout=60s
        echo "Updating coredns clusterrole in to cluster${i}..."
        cat <(kubectl get --context=cluster${i} clusterrole system:coredns -n kube-system -o yaml) ${PRJ_ROOT}/scripts/kind-e2e/config/patch-coredns-clusterrole.yaml >/tmp/clusterroledns.yaml
        kubectl apply --context=cluster${i} -n kube-system -f /tmp/clusterroledns.yaml
    done
    docker rmi lighthouse-coredns:${VERSION}_${uuid}
}

function update_coredns_configmap() {
    for i in 2 3; do
        echo "Updating coredns configMap in cluster${i}..."
        kubectl --context=cluster${i} -n kube-system replace -f ${PRJ_ROOT}/scripts/kind-e2e/config/coredns-cm.yaml
        kubectl --context=cluster${i} -n kube-system describe cm coredns
    done

}

function deploy_lighthouse_dnsserver() {
    docker tag lighthouse-dnsserver:${VERSION} lighthouse-dnsserver:local
    for i in 2 3; do
        echo "Updating coredns clusterrole in to cluster${i}..."
        cat <(kubectl get --context=cluster${i} clusterrole system:coredns -n kube-system -o yaml) ${PRJ_ROOT}/scripts/kind-e2e/config/patch-coredns-clusterrole.yaml >/tmp/clusterroledns.yaml
        kubectl apply --context=cluster${i} -n kube-system -f /tmp/clusterroledns.yaml
        echo "Deploying Lighthouse DNS server in cluster${i}..."
        kind --name cluster${i} load docker-image lighthouse-dnsserver:local
        kubectl --context=cluster${i} apply -f ${PRJ_ROOT}/package/lighthouse-dnsserver-deployment.yaml
        lh_dnsserver_clusterip=$(kubectl --context=cluster${i} -n kube-system get svc -l app=lighthouse-dnsserver | awk 'FNR == 2 {print $3}')
        sed "s|forward\ .\ lighthouse-dnsserver|forward\ .\ $lh_dnsserver_clusterip|g" ${PRJ_ROOT}/scripts/kind-e2e/config/coredns-dnsserver-cm-cluster${i}.yaml > /tmp/coredns-dnsserver-cm-cluster${i}.yaml
        kubectl --context=cluster${i} -n kube-system replace -f  /tmp/coredns-dnsserver-cm-cluster${i}.yaml
    done
}

function enable_logging() {
    if kubectl --context=cluster1 rollout status deploy/kibana > /dev/null 2>&1; then
        echo "Elasticsearch stack already installed, skipping..."
    else
        echo "Installing Elasticsearch..."
        es_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster1-control-plane | head -n 1)
        kubectl --context=cluster1 apply -f ${PRJ_ROOT}/scripts/kind-e2e/config/elasticsearch.yaml
        kubectl --context=cluster1 apply -f ${PRJ_ROOT}/scripts/kind-e2e/config/filebeat.yaml
        echo "Waiting for Elasticsearch to be ready..."
        kubectl --context=cluster1 wait --for=condition=Ready pods -l app=elasticsearch --timeout=300s
        for i in 2 3; do
            kubectl --context=cluster${i} apply -f ${PRJ_ROOT}/scripts/kind-e2e/config/filebeat.yaml
            kubectl --context=cluster${i} set env daemonset/filebeat -n kube-system ELASTICSEARCH_HOST=${es_ip} ELASTICSEARCH_PORT=30000
        done
    fi
}

function test_with_e2e_tests {
    cd ${DAPPER_SOURCE}/test/e2e

    go test -args -ginkgo.v -ginkgo.randomizeAllSpecs \
         -dp-context cluster1  -dp-context cluster2 -dp-context cluster3  \
        -report-dir ${DAPPER_OUTPUT}/junit 2>&1 | \
        tee ${DAPPER_OUTPUT}/e2e-tests.log
}

function cleanup {
    "${SCRIPTS_DIR}"/cleanup.sh
}

### Main ###

declare_kubeconfig

if [[ $status = clean ]]; then
    cleanup
    exit 0
elif [[ $status = onetime ]]; then
    echo Status $status: Will cleanup on EXIT signal
    trap cleanup EXIT
elif [[ $status != keep && $status != create ]]; then
    echo Unknown status: $status
    cleanup
    exit 1
fi

PRJ_ROOT=$(git rev-parse --show-toplevel)

if [[ $logging = true ]]; then
    enable_logging
fi

setup_lighthouse
if [[ $lhmode = plugin ]]; then
    update_coredns_configmap
    update_coredns_deployment
else
    deploy_lighthouse_dnsserver
fi
#test_with_e2e_tests

if [[ $status = keep || $status = create ]]; then
    echo "your 3 virtual clusters are deployed and working properly."
    echo "clusters can be accessed with:"
    echo ""
    echo "export KUBECONFIG=\$(echo \$(git rev-parse --show-toplevel)/output/kubeconfigs/kind-config-cluster{1..3} | sed 's/ /:/g')"
    echo ""
    echo "$ kubectl config use-context cluster1 # or cluster2, cluster3.."
    echo ""
    echo "to cleanup, just run: make e2e status=clean"
fi
