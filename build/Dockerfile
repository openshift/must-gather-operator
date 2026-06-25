FROM quay.io/redhat-services-prod/openshift/boilerplate:image-v8.3.4 AS builder

# Copy and download the dependecies so that they are cached locally in the stages.
WORKDIR /go/src/github.com/openshift/must-gather-operator

COPY . /go/src/github.com/openshift/must-gather-operator

RUN make go-build

FROM registry.redhat.io/ubi10/ubi-minimal:10.1

RUN microdnf install -y tar gzip openssh-clients wget shadow-utils procps sshpass nc && \
    microdnf clean all

COPY --from=builder /go/src/github.com/openshift/must-gather-operator/build/_output/bin/must-gather-operator /usr/local/bin/must-gather-operator
COPY --from=builder /go/src/github.com/openshift/must-gather-operator/build/bin /usr/local/bin

USER 65534:65534
ENTRYPOINT ["/usr/local/bin/must-gather-operator"]
