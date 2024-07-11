FIPS_ENABLED=true
RELEASE_BRANCHED_BUILDS?=true

# This needs to be hardcoded until https://issues.redhat.com/browse/SDCICD-1336 is fixed
RELEASE_BRANCH=release-4.16

include boilerplate/generated-includes.mk

.PHONY: boilerplate-update
boilerplate-update:
	@boilerplate/update
