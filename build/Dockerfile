FROM quay.io/app-sre/boilerplate:image-v3.0.3 AS builder
ENV OPERATOR=/usr/local/bin/must-gather-operator \
    OPERATOR_BIN=must-gather-operator \
    USER_UID=1001 \
    USER_NAME=must-gather-operator \
    JOB_TEMPLATE_FILE_NAME=/etc/templates/job.template.yaml

RUN mkdir /src

# Copy and download the dependecies so that they are cached locally in the stages.
COPY go.mod /src
COPY go.sum /src
WORKDIR /src
RUN go mod download

COPY . /src

RUN make go-build

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.7-1107
ENV OPERATOR=/usr/local/bin/must-gather-operator \
    OPERATOR_BIN=must-gather-operator \
    USER_UID=1001 \
    USER_NAME=must-gather-operator \
    JOB_TEMPLATE_FILE_NAME=/etc/templates/job.template.yaml \
    MUSTGATHER_CLEAN_URL="https://github.com/openshift/must-gather-clean/releases/download/v0.0.1" \
    SHA256_SUM="061bd25febb3bc2de96cd24a38a545bb1baf5431128c36d75a8bf749ae8bf436"

RUN microdnf install tar gzip openssh-clients wget shadow-utils procps && \
    wget https://kojipkgs.fedoraproject.org/packages/sshpass/1.06/9.el8/x86_64/sshpass-1.06-9.el8.x86_64.rpm && \
    wget ${MUSTGATHER_CLEAN_URL}/SHA256_SUM && \
    wget ${MUSTGATHER_CLEAN_URL}/must-gather-clean-linux-amd64.tar.gz && \
    tar xzf must-gather-clean-linux-amd64.tar.gz && \
    echo "${SHA256_SUM} must-gather-clean" | sha256sum --check && \
    chmod +x must-gather-clean && \
    mv ./must-gather-clean /usr/local/bin/must-gather-clean && \
    rm -f must-gather-clean-linux-amd64.tar.gz && \ 
    rpm -U sshpass-1.06-9.el8.x86_64.rpm && \
    rm -f sshpass-1.06-9.el8.x86_64.rpm && \
    microdnf clean all

COPY --from=builder /src/build/_output/bin/${OPERATOR_BIN} /usr/local/bin/${OPERATOR_BIN}
COPY --from=builder /src/build/bin /usr/local/bin
COPY --from=builder /src/build/templates /etc/templates

RUN /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
