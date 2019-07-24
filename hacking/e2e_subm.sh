#!/bin/bash
set -x
set -e

TOPDIR=$(git rev-parse --show-toplevel)
SUBMARINER_GIT=${SUBMARINER_GIT:-https://github.com/submariner-io/submariner}
SUBMARINER_BRANCH=${SUBMARINER_BRANCH:-master}

SUBMARINER_DIR=.tmp/submariner

cd $TOPDIR
if [[ ! -d $SUBMARINER_DIR ]]; then
    git clone $SUBMARINER_GIT $SUBMARINER_DIR
    cd $SUBMARINER_DIR
    git checkout origin/$SUBMARINER_BRANCH -B $SUBMARINER_BRANCH
    cd ../..
fi

cd $SUBMARINER_DIR

# We use version 1.14.1 of the kind image because it uses weave-net which 
# provides NetworkPolicy support and it's also compatible with submariner
# we also deploy federation
make e2e status=${1:-keep} version=1.14.1 kubefed=true

