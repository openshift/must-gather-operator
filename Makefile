include boilerplate/generated-includes.mk

FIPS_ENABLED=true

.PHONY: boilerplate-update
boilerplate-update:
	@boilerplate/update