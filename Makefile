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
##   make image-build-coverage image-push-coverage       # build & push coverage image
##   COVERAGE_IMAGE=<pullspec> hack/e2e-coverage.sh setup  # patch CSV/deployment
##   make test-e2e                                         # run E2E suite
##   make e2e-coverage-collect                             # collect + upload
##
## In CI, hack/e2e-coverage.sh handles setup and collection automatically.

COVERAGE_IMG ?= $(IMG)-e2e-coverage

# OpenShift cluster nodes are linux/amd64. When building on macOS (especially
# Apple Silicon), cross-build so the image can be pulled and run on the cluster.
# Override with COVERAGE_PLATFORM_FLAG= to disable, or set another platform.
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
COVERAGE_PLATFORM_FLAG ?= --platform=linux/amd64
else
COVERAGE_PLATFORM_FLAG ?=
endif

.PHONY: image-build-coverage
image-build-coverage: ## Build coverage-instrumented container image.
	$(CONTAINER_ENGINE) build $(COVERAGE_PLATFORM_FLAG) -f images/ci/Dockerfile.coverage -t $(COVERAGE_IMG) .

.PHONY: image-push-coverage
image-push-coverage: ## Push coverage-instrumented container image.
	$(CONTAINER_ENGINE) push $(COVERAGE_IMG)

.PHONY: e2e-coverage-collect
e2e-coverage-collect: ## Collect e2e coverage data and optionally upload to Codecov.
	ARTIFACT_DIR=$${ARTIFACT_DIR:-.} hack/e2e-coverage.sh collect