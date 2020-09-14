# Include boilerplate Makefiles (https://github.com/openshift/boilerplate)
include boilerplate/generated-includes.mk

# Include shared Makefiles
# TODO: Remove redundant Makefiles once boilerplate supports thier functions
include functions.mk

# boilerplate updater
.PHONY: update-boilerplate
update-boilerplate:
	@boilerplate/update

# Extend Makefile after here

TESTOPTS := -timeout 1m

default: generate-syncset gobuild

# TODO: Remove clean target once boilerplate supports cleaning bundles
.PHONY: clean
clean:
	rm -rf ./build/_output
	rm -rf bundles-staging/ bundles-production/ saas-*-bundle/

IN_CONTAINER?=false
SELECTOR_SYNC_SET_TEMPLATE_DIR?=hack/templates/
YAML_DIRECTORY?=deploy
GIT_ROOT?=$(shell git rev-parse --show-toplevel 2>&1)
SELECTOR_SYNC_SET_DESTINATION?=${GIT_ROOT}/hack/olm-registry/olm-artifacts-template.yaml
# WARNING: REPO_NAME will default to the current directory if there are no remotes
REPO_NAME?=$(shell basename $$((git config --get-regex remote\.*\.url 2>/dev/null | cut -d ' ' -f2 || pwd) | head -n1 | sed 's|.git||g'))
GEN_SYNCSET=hack/generate_template.py -t ${SELECTOR_SYNC_SET_TEMPLATE_DIR} -y ${YAML_DIRECTORY} -d ${SELECTOR_SYNC_SET_DESTINATION} -r ${REPO_NAME}

.PHONY: generate-syncset
generate-syncset:
	if [ "${IN_CONTAINER}" == "true" ]; then \
		$(CONTAINER_ENGINE) run --rm -v `pwd -P`:`pwd -P` python:2.7.15 /bin/sh -c "cd `pwd`; pip install oyaml; `pwd`/${GEN_SYNCSET}"; \
	else \
		${GEN_SYNCSET}; \
	fi

.PHONY: build-catalog-image
build-catalog-image:
	$(call create_push_catalog_image,staging,service/saas-$(OPERATOR_NAME)-bundle,$$APP_SRE_BOT_PUSH_TOKEN,false,service/saas-osd-operators,$(OPERATOR_NAME)-services/$(OPERATOR_NAME).yaml,hack/generate-operator-bundle.py,$(CATALOG_REGISTRY_ORGANIZATION))
	$(call create_push_catalog_image,production,service/saas-$(OPERATOR_NAME)-bundle,$$APP_SRE_BOT_PUSH_TOKEN,true,service/saas-osd-operators,$(OPERATOR_NAME)-services/$(OPERATOR_NAME).yaml,hack/generate-operator-bundle.py,$(CATALOG_REGISTRY_ORGANIZATION))
