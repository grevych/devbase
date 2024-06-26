APP := devbase
OSS := true
_ := $(shell ./scripts/devbase.sh)

include .bootstrap/root/Makefile

ORB_DEV_TAG ?= first

.PHONY: build-orb
pre-build:: build-orb
build-orb:
	circleci orb pack orbs/shared > orb.yml

.PHONY: validate-orb
validate-orb: build-orb
	circleci orb validate orb.yml

.PHONY: publish-orb
publish-orb: validate-orb
	circleci orb publish orb.yml getoutreach/shared@dev:$(ORB_DEV_TAG)

## <<Stencil::Block(targets)>>
STABLE_ORB_VERSION = $(shell gh release list --limit 1 --exclude-drafts --exclude-pre-releases --json name --jq '.[].name | ltrimstr("v")')

post-stencil::
	perl -p -i -e "s/dev:first/$(STABLE_ORB_VERSION)/g" .circleci/config.yml
	./scripts/shell-wrapper.sh catalog-sync.sh
## <</Stencil::Block>>
