TARGETS := $(shell ls scripts)

.dapper:
	@echo Downloading dapper
	@curl -sL https://releases.rancher.com/dapper/latest/dapper-`uname -s`-`uname -m` > .dapper.tmp
	@@chmod +x .dapper.tmp
	@./.dapper.tmp -v
	@mv .dapper.tmp .dapper

$(TARGETS): .dapper
	./.dapper -m bind $@

e2e: .dapper ./hacking/e2e_subm.sh
	./hacking/e2e_subm.sh
	./.dapper -m bind e2e


.DEFAULT_GOAL := ci

.PHONY: $(TARGETS)

