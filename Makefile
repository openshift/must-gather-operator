SHELL := /usr/bin/env bash

# Image URL to use all building/pushing image targets
REGISTRY ?= quay.io
REPOSITORY ?= $(REGISTRY)/openshift/must-gather-operator

# Include boilerplate Makefiles (https://github.com/openshift/boilerplate)
include boilerplate/generated-includes.mk

# Include shared Makefiles
# TODO: Remove redundant Makefiles once boilerplate supports thier functions
# Note: Order matters here, to override targets from boilerplate until supported.
include project.mk
include standard.mk
include functions.mk

default: generate-syncset gobuild

# Extend Makefile after here
CONTAINER_ENGINE?=docker

.PHONY: lint
lint:
	golangci-lint run --disable-all -E errcheck

# Build the docker image
.PHONY: container-build
container-build:
	$(MAKE) build

# Push the docker image
.PHONY: container-push
container-push:
	$(MAKE) push

.PHONY: operator-sdk-generate
operator-sdk-generate: opgenerate

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
