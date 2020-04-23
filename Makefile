status ?= onetime
version ?= 1.14.6
logging ?= false
coredns ?= 1.5.2
deploytool ?= helm
globalnet ?= false

TARGETS := $(shell ls scripts | grep -v deploy)
SCRIPTS_DIR ?= /opt/shipyard/scripts

.dapper:
	@echo Downloading dapper
	@curl -sL https://releases.rancher.com/dapper/latest/dapper-`uname -s`-`uname -m` > .dapper.tmp
	@@chmod +x .dapper.tmp
	@./.dapper.tmp -v
	@mv .dapper.tmp .dapper

cleanup: .dapper
	./.dapper -m bind $(SCRIPTS_DIR)/cleanup.sh

clusters:
	./.dapper -m bind $(SCRIPTS_DIR)/clusters.sh --k8s_version $(version) --globalnet $(globalnet)

deploy: build clusters
	DAPPER_ENV="OPERATOR_IMAGE" ./.dapper -m bind $@ --globalnet $(globalnet) --deploytool $(deploytool)

e2e: deploy
	./.dapper -m bind scripts/kind-e2e/e2e.sh --status $(status) --logging $(logging)

$(TARGETS): .dapper
	./.dapper -m bind $@ $(status) $(version) $(logging) $(coredns)

.DEFAULT_GOAL := ci

.PHONY: $(TARGETS)
