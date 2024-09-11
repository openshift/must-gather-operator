FROM quay.io/app-sre/boilerplate:image-v5.0.0 AS builder
ENV OPERATOR=/usr/local/bin/must-gather-operator \
    OPERATOR_BIN=must-gather-operator \
    USER_UID=1001 \
    USER_NAME=must-gather-operator

RUN mkdir /src

# Copy and download the dependecies so that they are cached locally in the stages.
COPY go.mod /src
COPY go.sum /src
WORKDIR /src
RUN go mod download

COPY . /src

RUN make go-build

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.10-1052.1724178568
ENV OPERATOR=/usr/local/bin/must-gather-operator \
    OPERATOR_BIN=must-gather-operator \
    USER_UID=1001 \
    USER_NAME=must-gather-operator

RUN microdnf install tar gzip openssh-clients wget shadow-utils procps sshpass && \
    microdnf clean all

COPY --from=builder /src/build/_output/bin/${OPERATOR_BIN} /usr/local/bin/${OPERATOR_BIN}
COPY --from=builder /src/build/bin /usr/local/bin

RUN /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
