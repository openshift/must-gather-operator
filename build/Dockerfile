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
    JOB_TEMPLATE_FILE_NAME=/etc/templates/job.template.yaml

RUN microdnf install tar gzip openssh-clients wget shadow-utils procps unzip && \
    wget https://kojipkgs.fedoraproject.org/packages/sshpass/1.06/9.el8/x86_64/sshpass-1.06-9.el8.x86_64.rpm && \
    rpm -U sshpass-1.06-9.el8.x86_64.rpm && \
    rm -f sshpass-1.06-9.el8.x86_64.rpm && \
    microdnf clean all && \
    curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip" && \
    unzip awscliv2.zip && \
    ./aws/install && \
    rm -rf aws awscliv2.zip


COPY --from=builder /src/build/_output/bin/${OPERATOR_BIN} /usr/local/bin/${OPERATOR_BIN}
COPY --from=builder /src/build/bin /usr/local/bin
COPY --from=builder /src/build/templates /etc/templates

RUN /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
