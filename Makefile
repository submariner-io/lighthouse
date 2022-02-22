BASE_BRANCH ?= devel
export BASE_BRANCH

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

BINARIES := bin/lighthouse-agent bin/lighthouse-coredns
IMAGES := lighthouse-agent lighthouse-coredns
PRELOAD_IMAGES := submariner-gateway submariner-operator submariner-route-agent $(IMAGES)
SETTINGS = $(DAPPER_SOURCE)/.shipyard.e2e.yml

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e / -e deploy)
override E2E_ARGS += cluster1 cluster2 cluster3
override UNIT_TEST_ARGS += test/e2e
override DEPLOY_ARGS += --service_discovery

# Targets to make

# Explicitly depend on the binary, since if it doesn't exist Shipyard won't find it
package/.image.lighthouse-agent: bin/lighthouse-agent

package/.image.lighthouse-coredns: bin/lighthouse-coredns

build: $(BINARIES)

bin/lighthouse-agent: vendor/modules.txt $(shell find pkg/agent)
	${SCRIPTS_DIR}/compile.sh $@ pkg/agent/main.go $(BUILD_ARGS)

bin/lighthouse-coredns: vendor/modules.txt $(shell find pkg/coredns)
	${SCRIPTS_DIR}/compile.sh $@ pkg/coredns/main.go $(BUILD_ARGS)

licensecheck: BUILD_ARGS=--noupx
licensecheck: $(BINARIES) bin/lichen
	bin/lichen -c .lichen.yaml $(BINARIES)

bin/lichen: vendor/modules.txt
	mkdir -p $(@D)
	go build -o $@ github.com/uw-labs/lichen

# Lighthouse-specific upgrade test:
# deploy latest, start nginx service, export it, upgrade, check service
upgrade-e2e: deploy-latest export-nginx deploy check-nginx e2e

# This relies on deploy-latest to get the original subctl
export-nginx: deploy-latest
	sed s/nginx-demo/nginx-upgrade/ /opt/shipyard/scripts/resources/nginx-demo.yaml | KUBECONFIG=output/kubeconfigs/kind-config-cluster1 kubectl apply -f -
	KUBECONFIG=output/kubeconfigs/kind-config-cluster1 ~/.local/bin/subctl export service nginx-upgrade -n default

check-nginx:
	KUBECONFIG=output/kubeconfigs/kind-config-cluster1 kubectl get serviceexports.multicluster.x-k8s.io -n default nginx-upgrade
	KUBECONFIG=output/kubeconfigs/kind-config-cluster2 kubectl get serviceimports.multicluster.x-k8s.io -n submariner-operator nginx-upgrade-default-cluster1

$(TARGETS): vendor/modules.txt
	./scripts/$@

.PHONY: $(TARGETS)

else

# Not running in Dapper

Makefile.dapper:
	@echo Downloading $@
	@curl -sfLO https://raw.githubusercontent.com/submariner-io/shipyard/$(BASE_BRANCH)/$@

include Makefile.dapper

endif

# Disable rebuilding Makefile
Makefile Makefile.inc: ;
