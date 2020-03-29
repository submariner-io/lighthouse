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

function setup_lighthouse_controller () {
    for i in 1 2 3; do
      echo "Installing lighthouse CRD in cluster${i}..."
      kubectl --context=cluster${i} apply -f ${PRJ_ROOT}/package/lighthouse-crd.yaml
    done
    kubefedctl enable MulticlusterService
    kubectl --context=cluster1 patch clusterrole  -n kube-federation-system  kubefed-role  --type json -p "$(cat ${PRJ_ROOT}/scripts/kind-e2e/config/patch-kubefed-clusterrole.yaml)"
    docker tag lighthouse-controller:${VERSION} lighthouse-controller:local
    kind --name cluster1 load docker-image lighthouse-controller:local
    kubectl --context=cluster1 apply -f ${PRJ_ROOT}/package/lighthouse-controller-deployment.yaml
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

function test_e2e_service_discovery() {
    echo "Updating coredns deployment on cluster2 with cluster3 service nginx service ip"
    if kubectl describe federatednamespace default > /dev/null 2>&1; then
        echo "Namespace default already federated..."
    else
        kubefedctl federate namespace default
    fi
    nginx_svc_ip_cluster3=$(kubectl --context=cluster3 get svc -l app=nginx-demo | awk 'FNR == 2 {print $3}')
    netshoot_pod=$(kubectl --context=cluster2 get pods -l app=netshoot | awk 'FNR == 2 {print $1}')
    kubectl --context=cluster2 -n kube-system set env deployment/coredns LIGHTHOUSE_SVCS="nginx-demo=${nginx_svc_ip_cluster3}"
    kubectl --context=cluster2 rollout status -n kube-system deploy/coredns --timeout=60s
    echo "Testing service discovery between clusters - $netshoot_pod cluster2 --> nginx service cluster3"
    attempt_counter=0
    max_attempts=5
    until $(kubectl --context=cluster2 exec -it ${netshoot_pod} -- curl --output /dev/null -m 30 --silent --head --fail nginx-demo); do
        if [[ ${attempt_counter} -eq ${max_attempts} ]];then
          echo "Max attempts reached, connection test failed!"
          exit 1
        fi
        attempt_counter=$(($attempt_counter+1))
    done
    echo "Service Discovery test was successful!"
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

function enable_kubefed() {
    if kubectl --context=cluster1 rollout status deploy/kubefed-controller-manager -n ${KUBEFED_NS} > /dev/null 2>&1; then
        echo "Kubefed already installed, skipping setup..."
    else
        helm init --client-only
        helm repo add kubefed-charts https://raw.githubusercontent.com/kubernetes-sigs/kubefed/master/charts
        helm --kube-context cluster1 install kubefed-charts/kubefed --version=0.1.0-rc2 --name kubefed --namespace ${KUBEFED_NS} --set controllermanager.replicaCount=1
        for i in 1 2 3; do
            kubefedctl join cluster${i} --cluster-context cluster${i} --host-cluster-context cluster1 --v=2
        done
        echo "Waiting for kubefed control plain to be ready..."
        kubectl --context=cluster1 wait --for=condition=Ready pods -l control-plane=controller-manager -n ${KUBEFED_NS} --timeout=120s
        kubectl --context=cluster1 wait --for=condition=Ready pods -l kubefed-admission-webhook=true -n ${KUBEFED_NS} --timeout=120s
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
KUBEFED_NS=kube-federation-system

if [[ $logging = true ]]; then
    enable_logging
fi

if [[ $kubefed = true ]]; then
    enable_kubefed
    setup_lighthouse_controller
    if [[ $lhmode = plugin ]]; then
        update_coredns_configmap
        update_coredns_deployment
    else
        deploy_lighthouse_dnsserver
    fi
    test_e2e_service_discovery
    test_with_e2e_tests
fi

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
