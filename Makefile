# Include boilerplate Makefiles (https://github.com/openshift/boilerplate)
include boilerplate/generated-includes.mk

# Include shared Makefiles
# TODO: Remove redundant Makefiles once boilerplate supports thier functions
# Note: Order matters here, to override targets from boilerplate until supported.
include project.mk
include standard.mk
include functions.mk

# Extend Makefile after here

default: generate-syncset gobuild

.PHONY: generate-syncset
generate-syncset:
	if [ "${IN_CONTAINER}" == "true" ]; then \
		$(CONTAINER_ENGINE) run --rm -v `pwd -P`:`pwd -P` python:2.7.15 /bin/sh -c "cd `pwd`; pip install oyaml; `pwd`/${GEN_SYNCSET}"; \
	else \
		${GEN_SYNCSET}; \
	fi

# boilerplate updater
.PHONY: update-boilerplate
update-boilerplate:
	@boilerplate/update
