status ?= onetime
version ?= 1.14.6
logging ?= false
kubefed ?= true
coredns ?= 1.5.2
lhmode ?= plugin
deploytool ?= helm
globalnet ?= false

TARGETS := $(shell ls scripts)
SCRIPTS_DIR ?= /opt/shipyard/scripts

.dapper:
	@echo Downloading dapper
	@curl -sL https://releases.rancher.com/dapper/latest/dapper-`uname -s`-`uname -m` > .dapper.tmp
	@@chmod +x .dapper.tmp
	@./.dapper.tmp -v
	@mv .dapper.tmp .dapper

cleanup: .dapper
	./.dapper -m bind $(SCRIPTS_DIR)/cleanup.sh

clusters: ci
	./.dapper -m bind $(SCRIPTS_DIR)/clusters.sh --k8s_version $(version) --globalnet $(globalnet)

deploy: clusters
	DAPPER_ENV="OPERATOR_IMAGE" ./.dapper -m bind $(SCRIPTS_DIR)/deploy.sh --globalnet $(globalnet) --deploytool $(deploytool)

e2e: build deploy
	./.dapper -m bind scripts/kind-e2e/e2e.sh --status $(status) --logging $(logging) --kubefed $(kubefed) --lhmode $(lhmode)

$(TARGETS): .dapper
	./.dapper -m bind $@ $(status) $(version) $(logging) $(kubefed) $(coredns) $(lhmode)

.DEFAULT_GOAL := ci

.PHONY: $(TARGETS)
