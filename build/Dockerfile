FROM quay.io/app-sre/boilerplate:image-v0.2.0 AS builder

ENV OPERATOR=/usr/local/bin/must-gather-operator \
    USER_UID=1001 \
    USER_NAME=must-gather-operator \
    JOB_TEMPLATE_FILE_NAME=/etc/templates/job.template.yaml

# install operator binary
COPY build/_output/bin/must-gather-operator ${OPERATOR}

RUN microdnf install tar gzip && microdnf clean all

COPY build/bin /usr/local/bin 
COPY build/templates /etc/templates
RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
