coredns ?= 1.5.2
deploytool ?= helm

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e / -e deploy)
E2E_ARGS=cluster1 cluster2 cluster3

ifeq ($(deploytool),operator)
DEPLOY_ARGS += --deploytool operator --deploytool_broker_args '--service-discovery'
else
DEPLOY_ARGS += --deploytool helm --deploytool_broker_args '--set submariner.serviceDiscovery=true' --deploytool_submariner_args '--set submariner.serviceDiscovery=true,lighthouse.image.repository=localhost:5000/lighthouse-agent,serviceAccounts.lighthouse.create=true'
endif

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
