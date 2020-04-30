version ?= 1.14.6
coredns ?= 1.5.2
deploytool ?= helm

TARGETS := $(shell ls scripts | grep -v deploy)
SCRIPTS_DIR ?= /opt/shipyard/scripts

ifeq ($(deploytool),operator)
DEPLOY_ARGS += --delpoytool operator --deploytool_broker_args '--service-discovery'
else
DEPLOY_ARGS += --deploytool helm --deploytool_broker_args '--set submariner.serviceDiscovery=true' --deploytool_submariner_args '--set submariner.serviceDiscovery=true,lighthouse.image.repository=localhost:5000/lighthouse-agent,serviceAccounts.lighthouse.create=true'
endif


.dapper:
	@echo Downloading dapper
	@curl -sL https://releases.rancher.com/dapper/latest/dapper-`uname -s`-`uname -m` > .dapper.tmp
	@@chmod +x .dapper.tmp
	@./.dapper.tmp -v
	@mv .dapper.tmp .dapper

cleanup: .dapper
	./.dapper -m bind $(SCRIPTS_DIR)/cleanup.sh

clusters:
	./.dapper -m bind $(SCRIPTS_DIR)/clusters.sh --k8s_version $(version)

deploy: build clusters
	DAPPER_ENV="OPERATOR_IMAGE" ./.dapper -m bind $@ $(DEPLOY_ARGS)

e2e: deploy
	./.dapper -m bind scripts/kind-e2e/e2e.sh

$(TARGETS): .dapper
	./.dapper -m bind $@ $(version) $(coredns)

.DEFAULT_GOAL := ci

.PHONY: $(TARGETS)
