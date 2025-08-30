FROM quay.io/redhat-services-prod/openshift/boilerplate:image-v8.0.0 AS builder

# Copy and download the dependecies so that they are cached locally in the stages.
WORKDIR /go/src/github.com/openshift/must-gather-operator

COPY . /go/src/github.com/openshift/must-gather-operator

RUN make go-build

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.6-1755695350

RUN microdnf install -y tar gzip openssh-clients wget shadow-utils procps sshpass nc && \
    microdnf clean all

COPY --from=builder /go/src/github.com/openshift/must-gather-operator/build/_output/bin/must-gather-operator /usr/local/bin/must-gather-operator
COPY --from=builder /go/src/github.com/openshift/must-gather-operator/build/bin /usr/local/bin

USER 65534:65534
ENTRYPOINT ["/usr/local/bin/must-gather-operator"]
