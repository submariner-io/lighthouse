coredns ?= 1.5.2

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e / -e deploy)
CLUSTER_SETTINGS_FLAG = --cluster_settings $(DAPPER_SOURCE)/scripts/cluster_settings
override CLUSTERS_ARGS += $(CLUSTER_SETTINGS_FLAG)
override DEPLOY_ARGS += $(CLUSTER_SETTINGS_FLAG)
E2E_ARGS=cluster1 cluster2

# Process extra flags from the `using=a,b,c` optional flag

ifneq (,$(filter helm,$(_using)))
override DEPLOY_ARGS += --deploytool_broker_args '--set submariner.serviceDiscovery=true' --deploytool_submariner_args '--set submariner.serviceDiscovery=true,lighthouse.image.repository=localhost:5000/lighthouse-agent,lighthouse.image.tag=local,lighthouseCoredns.image.repository=localhost:5000/lighthouse-coredns,lighthouseCoredns.image.tag=local,serviceAccounts.lighthouse.create=true'
else
override DEPLOY_ARGS += --deploytool_broker_args '--service-discovery'
endif

# Targets to make

build: vendor/modules.txt
	./scripts/build-agent $(BUILD_ARGS)
	./scripts/build-coredns $(coredns) $(BUILD_ARGS)

deploy: build clusters
	./scripts/$@ $(DEPLOY_ARGS)

$(TARGETS): vendor/modules.txt
	./scripts/$@

.PHONY: $(TARGETS)

else

# Not running in Dapper

include Makefile.dapper

endif

# Disable rebuilding Makefile
Makefile Makefile.dapper Makefile.inc: ;
