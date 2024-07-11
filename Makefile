FIPS_ENABLED=true
RELEASE_BRANCHED_BUILDS?=true

include boilerplate/generated-includes.mk

.PHONY: boilerplate-update
boilerplate-update:
	@boilerplate/update
