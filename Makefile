FIPS_ENABLED=true

include boilerplate/generated-includes.mk

.PHONY: boilerplate-update
boilerplate-update:
	@boilerplate/update


# Utilize Kind or modify the e2e tests to load the image locally, enabling compatibility with other vendors.
E2E_TIMEOUT ?= 1h
.PHONY: test-e2e  # Run the e2e tests against a Kind k8s instance that is spun up.
test-e2e:
	go test \
	-timeout $(E2E_TIMEOUT) \
	-count 1 \
	-v \
	-p 1 \
	-tags e2e \
	./test/e2e \
	-ginkgo.v \
	-ginkgo.show-node-events