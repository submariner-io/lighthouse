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
make e2e status=keep version=1.14.1 kubefed=true

