#!/usr/bin/env bash
set -em

source $(dirname $0)/../lib/debug_functions

### Functions ###

function kind_clusters() {
    trap_commands
    status=$1
    version=$2
    pids=(-1 -1 -1)
    logs=()
    for i in 1 2 3; do
        if [[ $(kind get clusters | grep cluster${i} | wc -l) -gt 0  ]]; then
            echo "Cluster cluster${i} already exists, skipping cluster creation..."
        else
            logs[$i]=$(mktemp)
            echo Creating cluster${i}, logging to ${logs[$i]}...
            (
            if [[ -n ${version} ]]; then
                kind create cluster --image=kindest/node:v${version} --name=cluster${i} --wait=5m --config=${PRJ_ROOT}/scripts/kind-e2e/config/cluster${i}-config.yaml
            else
                kind create cluster --name=cluster${i} --wait=5m --config=${PRJ_ROOT}/scripts/kind-e2e/config/cluster${i}-config.yaml
            fi
            master_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster${i}-control-plane | head -n 1)
            sed -i -- "s/user: kubernetes-admin/user: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")
            sed -i -- "s/name: kubernetes-admin.*/name: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")
            sed -i -- "s/current-context: kubernetes-admin.*/current-context: cluster$i/g" $(kind get kubeconfig-path --name="cluster$i")

            if [[ ${status} = keep ]]; then
                cp -r $(kind get kubeconfig-path --name="cluster$i") ${PRJ_ROOT}/output/kind-config/local-dev/kind-config-cluster${i}
            fi

            sed -i -- "s/server: .*/server: https:\/\/$master_ip:6443/g" $(kind get kubeconfig-path --name="cluster$i")
            cp -r $(kind get kubeconfig-path --name="cluster$i") ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster${i}
            ) > ${logs[$i]} 2>&1 &
            set pids[$i] = $!
        fi
    done
    if [[ ${#logs[@]} -gt 0 ]]; then
        echo "(Watch the installation processes with \"tail -f ${logs[@]}\".)"
        for i in 1 2 3; do
            if [[ pids[$i] -gt -1 ]]; then
                wait ${pids[$i]}
                if [[ $? -ne 0 && $? -ne 127 ]]; then
                    echo Cluster $i creation failed:
                    cat $logs[$i]
                fi
                rm -f $logs[$i]
            fi
        done
    fi
}

function install_helm() {
    trap_commands
    helm init --client-only
    helm repo add submariner-latest https://releases.rancher.com/submariner-charts/latest
    pids=(-1 -1 -1)
    logs=()
    for i in 1 2 3; do
        if kubectl --context=cluster${i} -n kube-system rollout status deploy/tiller-deploy > /dev/null 2>&1; then
            echo "Helm already installed on cluster${i}, skipping helm installation..."
        else
            logs[$i]=$(mktemp)
            echo "Installing helm on cluster${i}, logging to ${logs[$i]}..."
            (
            kubectl --context=cluster${i} -n kube-system create serviceaccount tiller
            kubectl --context=cluster${i} create clusterrolebinding tiller --clusterrole=cluster-admin --serviceaccount=kube-system:tiller
            helm --kube-context cluster${i} init --service-account tiller
            kubectl --context=cluster${i} -n kube-system rollout status deploy/tiller-deploy
            ) > ${logs[$i]} 2>&1 &
            set pids[$i] = $!
        fi
    done
    if [[ ${#logs[@]} -gt 0 ]]; then
        echo "(Watch the installation processes with \"tail -f ${logs[@]}\".)"
        for i in 1 2 3; do
            if [[ pids[$i] -gt -1 ]]; then
                wait ${pids[$i]}
                if [[ $? -ne 0 && $? -ne 127 ]]; then
                    echo Cluster $i creation failed:
                    cat $logs[$i]
                fi
                rm -f $logs[$i]
            fi
        done
    fi
}

function setup_broker() {
    trap_commands
    if kubectl --context=cluster1 get crd clusters.submariner.io > /dev/null 2>&1; then
        echo "Submariner CRDs already exist, skipping broker creation..."
    else
        echo "Installing broker on cluster1."
        helm --kube-context cluster1 install submariner-latest/submariner-k8s-broker --name ${SUBMARINER_BROKER_NS} --namespace ${SUBMARINER_BROKER_NS}
    fi

    SUBMARINER_BROKER_URL=$(kubectl --context=cluster1 -n default get endpoints kubernetes -o jsonpath="{.subsets[0].addresses[0].ip}:{.subsets[0].ports[?(@.name=='https')].port}")
    SUBMARINER_BROKER_CA=$(kubectl --context=cluster1 -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data['ca\.crt']}")
    SUBMARINER_BROKER_TOKEN=$(kubectl --context=cluster1 -n ${SUBMARINER_BROKER_NS} get secrets -o jsonpath="{.items[?(@.metadata.annotations['kubernetes\.io/service-account\.name']=='${SUBMARINER_BROKER_NS}-client')].data.token}"|base64 --decode)
}

function setup_cluster2_gateway() {
    trap_commands
    if kubectl --context=cluster2 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s > /dev/null 2>&1; then
            echo "Submariner already installed, skipping submariner helm installation..."
        else
            echo "Installing submariner on cluster2..."
            worker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster2-worker | head -n 1)
            kubectl --context=cluster2 label node cluster2-worker "submariner.io/gateway=true" --overwrite
            helm --kube-context cluster2 install submariner-latest/submariner \
            --name submariner \
            --namespace submariner \
            --set ipsec.psk="${SUBMARINER_PSK}" \
            --set broker.server="${SUBMARINER_BROKER_URL}" \
            --set broker.token="${SUBMARINER_BROKER_TOKEN}" \
            --set broker.namespace="${SUBMARINER_BROKER_NS}" \
            --set broker.ca="${SUBMARINER_BROKER_CA}" \
            --set submariner.clusterId="cluster2" \
            --set submariner.clusterCidr="10.245.0.0/16" \
            --set submariner.serviceCidr="100.95.0.0/16" \
            --set submariner.natEnabled="false" \
            --set routeAgent.image.repository="rancher/submariner-route-agent" \
            --set routeAgent.image.tag="v0.0.1" \
            --set engine.image.repository="rancher/submariner" \
            --set engine.image.tag="v0.0.1"
            echo "Waiting for submariner pods to be Ready on cluster2..."
            kubectl --context=cluster2 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=120s
            kubectl --context=cluster2 wait --for=condition=Ready pods -l app=submariner-routeagent -n submariner --timeout=120s
            echo "Deploying netshoot on cluster2 worker: ${worker_ip}"
            kubectl --context=cluster2 apply -f ${PRJ_ROOT}/scripts/kind-e2e/config/netshoot.yaml
            echo "Waiting for netshoot pods to be Ready on cluster2."
            kubectl --context=cluster2 rollout status deploy/netshoot --timeout=120s
    fi
}

function setup_cluster3_gateway() {
    trap_commands
    if kubectl --context=cluster3 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=60s > /dev/null 2>&1; then
            echo "Submariner already installed, skipping submariner helm installation..."
        else
            echo "Installing submariner on cluster3..."
            worker_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' cluster3-worker | head -n 1)
            kubectl --context=cluster3 label node cluster3-worker "submariner.io/gateway=true" --overwrite
            helm --kube-context cluster3 install submariner-latest/submariner \
             --name submariner \
             --namespace submariner \
             --set ipsec.psk="${SUBMARINER_PSK}" \
             --set broker.server="${SUBMARINER_BROKER_URL}" \
             --set broker.token="${SUBMARINER_BROKER_TOKEN}" \
             --set broker.namespace="${SUBMARINER_BROKER_NS}" \
             --set broker.ca="${SUBMARINER_BROKER_CA}" \
             --set submariner.clusterId="cluster3" \
             --set submariner.clusterCidr="10.246.0.0/16" \
             --set submariner.serviceCidr="100.96.0.0/16" \
             --set submariner.natEnabled="false" \
            --set routeAgent.image.repository="rancher/submariner-route-agent" \
            --set routeAgent.image.tag="v0.0.1" \
            --set engine.image.repository="rancher/submariner" \
            --set engine.image.tag="v0.0.1"
            echo "Waiting for submariner pods to be Ready on cluster3..."
            kubectl --context=cluster3 wait --for=condition=Ready pods -l app=submariner-engine -n submariner --timeout=120s
            kubectl --context=cluster3 wait --for=condition=Ready pods -l app=submariner-routeagent -n submariner --timeout=120s
            echo "Deploying nginx on cluster3 worker: ${worker_ip}"
            kubectl --context=cluster3 apply -f ${PRJ_ROOT}/scripts/kind-e2e/config/nginx-demo.yaml
            echo "Waiting for nginx-demo deployment to be Ready on cluster3."
            kubectl --context=cluster3 rollout status deploy/nginx-demo --timeout=120s
    fi
}

function update_coredns_deployment() {
    trap_commands
    uuid=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w 8 | head -n 1)
    docker tag lighthouse-coredns:dev lighthouse-coredns:dev_${uuid}
    for i in 1 2 3; do
        echo "Updating coredns images in to cluster${i}..."
        kind --name cluster${i} load docker-image lighthouse-coredns:dev_${uuid} --loglevel=debug
        kubectl --context=cluster${i} set image -n kube-system deploy/coredns coredns=lighthouse-coredns:dev_${uuid}
        echo "Waiting for coredns deployment to be Ready on cluster${i}."
        kubectl --context=cluster${i} rollout status -n kube-system deploy/coredns --timeout=60s
    done
    docker rmi lighthouse-coredns:dev_${uuid}
}

function test_connection() {
    trap_commands
    nginx_svc_ip_cluster3=$(kubectl --context=cluster3 get svc -l app=nginx-demo | awk 'FNR == 2 {print $3}')
    netshoot_pod=$(kubectl --context=cluster2 get pods -l app=netshoot | awk 'FNR == 2 {print $1}')

    echo "Testing connectivity between clusters - $netshoot_pod cluster2 --> $nginx_svc_ip_cluster3 nginx service cluster3"

    attempt_counter=0
    max_attempts=5
    until $(kubectl --context=cluster2 exec -it ${netshoot_pod} -- curl --output /dev/null -m 30 --silent --head --fail ${nginx_svc_ip_cluster3}); do
        if [[ ${attempt_counter} -eq ${max_attempts} ]];then
          echo "Max attempts reached, connection test failed!"
          exit 1
        fi
        attempt_counter=$(($attempt_counter+1))
    done
    echo "Connection test was successful!"
}

function enable_logging() {
    trap_commands
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
    trap_commands
    if kubectl --context=cluster1 rollout status deploy/kubefed-controller-manager -n ${KUBEFED_NS} > /dev/null 2>&1; then
        echo "Kubefed already installed, skipping setup..."
    else
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
    trap_commands
    cd ../test/e2e

    # Setup the KUBECONFIG env
    export KUBECONFIG=$(echo ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster{1..3} | sed 's/ /:/g')

    go test -args -ginkgo.v -ginkgo.randomizeAllSpecs \
        -dp-context cluster2 -dp-context cluster3  \
        -report-dir ${DAPPER_SOURCE}/${DAPPER_OUTPUT}/junit 2>&1 | \
        tee ${DAPPER_SOURCE}/${DAPPER_OUTPUT}/e2e-tests.log
}

function cleanup {
    trap_commands
    for i in 1 2 3; do
      if [[ $(kind get clusters | grep cluster${i} | wc -l) -gt 0  ]]; then
        kind delete cluster --name=cluster${i};
      fi
    done

    if [[ $(docker ps -qf status=exited | wc -l) -gt 0 ]]; then
        echo Cleaning containers...
        docker ps -qf status=exited | xargs docker rm -f
    fi
    if [[ $(docker images -qf dangling=true | wc -l) -gt 0 ]]; then
        echo Cleaning images...
        docker images -qf dangling=true | xargs docker rmi -f
    fi
    if [[ $(docker volume ls -qf dangling=true | wc -l) -gt 0 ]]; then
        echo Cleaning volumes...
        docker volume ls -qf dangling=true | xargs docker volume rm -f
    fi
}

### Main ###

if [[ $1 = clean ]]; then
    cleanup
    exit 0
fi

if [[ $1 != keep ]]; then
    trap cleanup EXIT
fi

echo Starting with status: $1, k8s_version: $2, logging: $3, kubefed: $4, coredns=$5.
PRJ_ROOT=$(git rev-parse --show-toplevel)
mkdir -p ${PRJ_ROOT}/output/kind-config/dapper/ ${PRJ_ROOT}/output/kind-config/local-dev/
SUBMARINER_BROKER_NS=submariner-k8s-broker
SUBMARINER_PSK=$(cat /dev/urandom | LC_CTYPE=C tr -dc 'a-zA-Z0-9' | fold -w 64 | head -n 1)
KUBEFED_NS=kube-federation-system
export KUBECONFIG=$(echo ${PRJ_ROOT}/output/kind-config/dapper/kind-config-cluster{1..3} | sed 's/ /:/g')

kind_clusters "$@"
if [[ $3 = true ]]; then
    enable_logging
fi
install_helm
if [[ $4 = true ]]; then
    enable_kubefed
fi
setup_broker
setup_cluster2_gateway
setup_cluster3_gateway
test_connection
update_coredns_deployment
test_with_e2e_tests

if [[ $1 = keep ]]; then
    echo "your 3 virtual clusters are deployed and working properly."
    echo "clusters can be accessed with:"
    echo ""
    echo "export KUBECONFIG=\$(echo \$(git rev-parse --show-toplevel)/output/kind-config/local-dev/kind-config-cluster{1..3} | sed 's/ /:/g')"
    echo ""
    echo "$ kubectl config use-context cluster1 # or cluster2, cluster3.."
    echo ""
    echo "to cleanup, just run: make e2e status=clean"
fi
