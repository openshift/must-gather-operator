FIPS_ENABLED=true
TESTTARGETS := $(shell ${GOENV} go list -e ./... | grep -E -v "/(vendor)/" | grep -E -v "/(test/e2e|test/apis)")

include boilerplate/generated-includes.mk

.PHONY: boilerplate-update
boilerplate-update:
	@boilerplate/update


##@ Kube API Linter
##
## kube-api-linter (sigs.k8s.io/kube-api-linter) is not part of the boilerplate
## golangci-lint config, so it's built and run as a separate golangci-lint
## plugin binary via a custom-gcl build. Chained onto `lint` below so `make lint`
## (as run in CI) always includes it.

bin/golangci-lint-kube-api-linter: .custom-gcl.yml
	${CONVENTION_DIR}/ensure.sh golangci-lint
	golangci-lint custom

.PHONY: kube-api-lint
kube-api-lint: bin/golangci-lint-kube-api-linter ## Run kube-api-linter against the API types
	${GOENV} GOLANGCI_LINT_CACHE=${GOLANGCI_LINT_CACHE} ./bin/golangci-lint-kube-api-linter run -c .golangci.yml ./...

##@ OLM Bundle

.PHONY: bundle
bundle: generate ## Sync generated CRD into the OLM bundle directory.
	cp deploy/crds/operator.openshift.io_mustgathers.yaml bundle/manifests/stable/operator.openshift.io_mustgathers.yaml

##@ CRD Validation Tests (envtest)
# CRD uses CEL format() library (Kube 1.31+), so we need a newer envtest than the boilerplate default.
ENVTEST_K8S_VERSION_APIS ?= 1.35.0

.PHONY: test-apis test
test: go-test test-apis
test-apis: setup-envtest ## Run CRD validation tests against an envtest API server
	@if ! ASSETS_PATH=$$($(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION_APIS) --arch amd64 --os $$(go env GOOS) --bin-dir /tmp/envtest-binaries -p path 2>&1); then \
		echo "Failed to setup envtest: $$ASSETS_PATH"; \
		exit 1; \
	fi; \
	${GOENV} KUBEBUILDER_ASSETS="$$ASSETS_PATH" go test -v -timeout 30m ./test/apis/...

.PHONY: lint
lint: kube-api-lint olm-deploy-yaml-validate go-check


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