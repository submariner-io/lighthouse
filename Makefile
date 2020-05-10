coredns ?= 1.5.2
deploytool ?= helm

ifneq (,$(DAPPER_HOST_ARCH))

# Running in Dapper

include $(SHIPYARD_DIR)/Makefile.inc

TARGETS := $(shell ls -p scripts | grep -v -e / -e deploy)

ifeq ($(deploytool),operator)
DEPLOY_ARGS += --deploytool operator --deploytool_broker_args '--service-discovery'
else
DEPLOY_ARGS += --deploytool helm --deploytool_broker_args '--set submariner.serviceDiscovery=true' --deploytool_submariner_args '--set submariner.serviceDiscovery=true,lighthouse.image.repository=localhost:5000/lighthouse-agent,serviceAccounts.lighthouse.create=true'
endif

deploy: build clusters
	./scripts/$@ $(DEPLOY_ARGS)

e2e: deploy
	./scripts/kind-e2e/e2e.sh

$(TARGETS): vendor/modules.txt
	./scripts/$@ $(coredns)

.PHONY: $(TARGETS)

else

# Not running in Dapper

include Makefile.dapper

endif

# Disable rebuilding Makefile
Makefile Makefile.dapper Makefile.inc: ;
