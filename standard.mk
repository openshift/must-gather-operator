# ex, -v
TESTOPTS := -timeout 1m

# TODO: Remove clean target once boilerplate supports cleaning bundles
.PHONY: clean
clean:
	rm -rf ./build/_output
	rm -rf bundles-staging/ bundles-production/ saas-*-bundle/

.PHONY: build-catalog-image
build-catalog-image:
	$(call create_push_catalog_image,staging,service/saas-$(OPERATOR_NAME)-bundle,$$APP_SRE_BOT_PUSH_TOKEN,false,service/saas-osd-operators,$(OPERATOR_NAME)-services/$(OPERATOR_NAME).yaml,hack/generate-operator-bundle.py,$(CATALOG_REGISTRY_ORGANIZATION))
	$(call create_push_catalog_image,production,service/saas-$(OPERATOR_NAME)-bundle,$$APP_SRE_BOT_PUSH_TOKEN,true,service/saas-osd-operators,$(OPERATOR_NAME)-services/$(OPERATOR_NAME).yaml,hack/generate-operator-bundle.py,$(CATALOG_REGISTRY_ORGANIZATION))

