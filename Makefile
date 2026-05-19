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


##@ E2E Coverage
##
## Targets for building a coverage-instrumented operator image, collecting
## coverage data written during E2E tests, and uploading the report to Codecov.
##
## Typical flow (local):
##   make docker-build-coverage docker-push-coverage   # build & push coverage image
##   COVERAGE_IMAGE=<pullspec> hack/e2e-coverage.sh setup  # patch CSV
##   make test-e2e                                      # run E2E suite
##   make e2e-coverage-collect                           # collect + upload
##
## In CI, hack/e2e-coverage.sh handles setup and collection automatically.

COVERAGE_IMG ?= $(IMG)-e2e-coverage

.PHONY: docker-build-coverage
docker-build-coverage: ## Build coverage Docker image from images/ci/Dockerfile.coverage.
	$(CONTAINER_TOOL) build -f images/ci/Dockerfile.coverage -t $(COVERAGE_IMG) .

.PHONY: docker-push-coverage
docker-push-coverage: ## Push coverage Docker image.
	$(CONTAINER_TOOL) push $(COVERAGE_IMG)

.PHONY: e2e-coverage-collect
e2e-coverage-collect: ## Collect e2e coverage data and optionally upload to Codecov.
	ARTIFACT_DIR=$${ARTIFACT_DIR:-.} hack/e2e-coverage.sh collect