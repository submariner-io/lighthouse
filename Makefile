BASE_BRANCH ?= devel
export BASE_BRANCH

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

gotodockerarch = $(patsubst arm,arm/v7,$(1))
dockertogoarch = $(patsubst arm/v7,arm,$(1))

nullstring :=
space := $(nullstring) # end of the line
comma := ,

PLATFORMS ?= linux/amd64,linux/arm64
BINARIES := lighthouse-agent lighthouse-coredns
ARCH_BINARIES := $(foreach platform,$(subst $(comma),$(space),$(PLATFORMS)),$(foreach binary,$(BINARIES),bin/$(call gotodockerarch,$(platform))/$(binary)))
IMAGES := lighthouse-agent lighthouse-coredns
export LOCAL_COMPONENTS := submariner-lighthouse-agent submariner-lighthouse-coredns
MULTIARCH_IMAGES := $(IMAGES)
SETTINGS = $(DAPPER_SOURCE)/.shipyard.e2e.yml

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e / -e deploy)
override E2E_ARGS += cluster1 cluster2 cluster3
override UNIT_TEST_ARGS += test/e2e
export LIGHTHOUSE = true
export LDFLAGS = -X main.version=$(VERSION)

# Targets to make

build: $(ARCH_BINARIES)

bin/%/lighthouse-agent: $(shell find pkg/agent)
	GOARCH=$(call dockertogoarch,$(patsubst bin/linux/%/,%,$(dir $@))) ${SCRIPTS_DIR}/compile.sh $@ ./pkg/agent

bin/%/lighthouse-coredns: $(shell find coredns)
	mkdir -p $(@D)
	cd coredns && GOARCH=$(call dockertogoarch,$(patsubst bin/linux/%/,%,$(dir $@))) ${SCRIPTS_DIR}/compile.sh $(CURDIR)/$@ .

e2e:

licensecheck: export BUILD_UPX = false
licensecheck: $(ARCH_BINARIES) bin/lichen
	bin/lichen -c .lichen.yaml $(ARCH_BINARIES)

bin/lichen:
	mkdir -p $(@D)
	cd tools && go build -o $(CURDIR)/$@ github.com/uw-labs/lichen

# Lighthouse-specific upgrade test:
# deploy latest, start nginx service, export it, upgrade, check service
upgrade-e2e: deploy-latest export-nginx deploy check-nginx e2e

# This relies on deploy-latest to get the original subctl
export-nginx: deploy-latest
	sed s/nginx-demo/nginx-upgrade/ /opt/shipyard/scripts/resources/nginx-demo.yaml | KUBECONFIG=output/kubeconfigs/kind-config-cluster1 kubectl apply -f -
	KUBECONFIG=output/kubeconfigs/kind-config-cluster1 ~/.local/bin/subctl export service nginx-upgrade -n default

check-nginx:
	kubectl get serviceexports.multicluster.x-k8s.io --kubeconfig output/kubeconfigs/kind-config-cluster1 -n default nginx-upgrade
	. $(SCRIPTS_DIR)/lib/utils && with_retries 10 sleep_on_fail 1s kubectl get serviceimports.multicluster.x-k8s.io --kubeconfig output/kubeconfigs/kind-config-cluster1 -n default nginx-upgrade

$(TARGETS):
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
