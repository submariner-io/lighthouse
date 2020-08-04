coredns ?= 1.5.2

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e / -e deploy)
CLUSTER_SETTINGS_FLAG = --cluster_settings $(DAPPER_SOURCE)/scripts/cluster_settings
override CLUSTERS_ARGS += $(CLUSTER_SETTINGS_FLAG)
override DEPLOY_ARGS += $(CLUSTER_SETTINGS_FLAG)
override E2E_ARGS += cluster1 cluster2
override UNIT_TEST_ARGS += test/e2e
override VALIDATE_ARGS += --skip-dirs pkg/client

# Process extra flags from the `using=a,b,c` optional flag

ifneq (,$(filter helm,$(_using)))
override DEPLOY_ARGS += --deploytool_broker_args '--set submariner.serviceDiscovery=true' --deploytool_submariner_args '--set submariner.serviceDiscovery=true,lighthouse.image.repository=localhost:5000/lighthouse-agent,lighthouse.image.tag=local,lighthouseCoredns.image.repository=localhost:5000/lighthouse-coredns,lighthouseCoredns.image.tag=local,serviceAccounts.lighthouse.create=true'
else
override DEPLOY_ARGS += --deploytool_broker_args '--service-discovery'
endif

# Targets to make

images: package/.image.lighthouse-agent package/.image.lighthouse-coredns

# Explicitly depend on the binary, since if it doesn't exist Shipyard won't find it
package/.image.lighthouse-agent: bin/lighthouse-agent

package/.image.lighthouse-coredns: bin/lighthouse-coredns

bin/lighthouse-agent: vendor/modules.txt $(shell find pkg/agent)
	${SCRIPTS_DIR}/compile.sh $@ pkg/agent/main.go

bin/lighthouse-coredns: vendor/modules.txt $(shell find pkg/coredns)
	${SCRIPTS_DIR}/compile.sh $@ pkg/coredns/main.go

deploy: images clusters
	./scripts/$@ $(DEPLOY_ARGS)

test: unit-test

$(TARGETS): vendor/modules.txt
	./scripts/$@

.PHONY: $(TARGETS) images test validate

else

# Not running in Dapper

include Makefile.dapper

endif

# Disable rebuilding Makefile
Makefile Makefile.dapper Makefile.inc: ;
