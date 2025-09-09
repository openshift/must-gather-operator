FIPS_ENABLED=true

include boilerplate/generated-includes.mk

.PHONY: boilerplate-update
boilerplate-update:
	@boilerplate/update

OPENAPI_GEN_PKG := k8s.io/kube-openapi/cmd/openapi-gen
CONTROLLER_GEN_PKG := sigs.k8s.io/controller-tools/cmd/controller-gen

# go-install-tool will 'go install' any package $2 and install it to $1.
define go-install-tool
@{ \
set -e ;\
echo "Downloading $(2)" ;\
GOBIN=$(shell dirname $(1)) go install $(2) ;\
echo "Installed in $(1)" ;\
rm -rf $$TMP_DIR ;\
}
endef

.PHONY: install-tools
install-tools:
	$(call go-install-tool, $(shell pwd)/bin/controller-gen, $(CONTROLLER_GEN_PKG))
	$(call go-install-tool, $(shell pwd)/bin/openapi-gen, $(OPENAPI_GEN_PKG))

