SHELL := /usr/bin/env bash

# Image URL to use all building/pushing image targets
REGISTRY ?= quay.io
REPOSITORY ?= $(REGISTRY)/openshift/must-gather-operator

# Boilerplate Makefile includes
include boilerplate/generated-includes.mk

# Include shared Makefiles
# TODO: Remove once boilerplate supports generating OLM bundles
include functions.mk

# Extend Makefile after here
CONTAINER_ENGINE?=docker

.PHONY: lint
lint:
	golangci-lint run --disable-all -E errcheck

# Build the docker image
.PHONY: container-build
container-build:
	$(MAKE) build

.PHONY: container-push
container-push:
	$(MAKE) push

.PHONY: generate-syncset
generate-syncset:
	if [ "${IN_CONTAINER}" == "true" ]; then \
		$(CONTAINER_ENGINE) run --rm -v `pwd -P`:`pwd -P` python:2.7.15 /bin/sh -c "cd `pwd`; pip install oyaml; `pwd`/${GEN_SYNCSET}"; \
	else \
		${GEN_SYNCSET}; \
	fi

.PHONY: update-boilerplate
update-boilerplate:
	@boilerplate/update
