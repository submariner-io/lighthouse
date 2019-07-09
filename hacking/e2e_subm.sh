#!/bin/sh
set -x
set -e

TOPDIR=$(git rev-parse --show-toplevel)
SUBMARINER_GIT=${SUBMARINER_GIT:-https://github.com/submariner-io/submariner}
SUBMARINER_BRANCH=${SUBMARINER_BRANCH:-master}

SUBMARINER_DIR=submariner

cd $TOPDIR
if [[ ! -d $SUBMARINER_DIR ]]; then
    git clone $SUBMARINER_GIT $SUBMARINER_DIR
    cd $SUBMARINER_DIR
    git checkout origin/$SUBMARINER_BRANCH -B $SUBMARINER_BRANCH
    cd ..
fi

cd $SUBMARINER_DIR

# We use version 1.14.1 of the kind image because it uses weave-net which 
# provides NetworkPolicy support and it's also compatible with submariner
# we also deploy federation
make e2e status=${1:-keep} version=1.14.1 kubefed=true

if [[ "{$1:-keep}" == "keep" ]]; then
    echo "your 3 virtual clusters are deployed and working properly with your local"
    echo "submariner source code, and can be accessed with:"
    echo ""
    echo "export KUBECONFIG=\$(echo $(git rev-parse --show-toplevel)/output/kind-config/local-dev/kind-config-cluster{1..3} | sed 's/ /:/g')"
    echo ""
    echo "$ kubectl config use-context cluster1 # or cluster2, cluster3.."
    echo ""
    echo "to cleanup, just run: make e2e status=clean"
fi
