FROM registry.ci.openshift.org/openshift/release:rhel-9-release-golang-1.24-openshift-4.20 AS builder
WORKDIR /go/src/github.com/openshift/cluster-node-tuning-operator
COPY . .

ARG GOARCH

RUN make update-tuned-submodule
RUN make build

FROM quay.io/centos/centos:stream9
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/_output/cluster-node-tuning-operator /usr/bin/
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/_output/performance-profile-creator /usr/bin/
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/_output/gather-sysinfo /usr/bin/

ENV ASSETS_DIR=/root/assets
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/assets $ASSETS_DIR

COPY hack/dockerfile_install_support.sh /tmp
RUN /bin/bash /tmp/dockerfile_install_support.sh

COPY manifests/*.yaml manifests/image-references /manifests/
ENV HOME=/run/ocp-tuned
ENV SYSTEMD_IGNORE_CHROOT=1
WORKDIR ${HOME}

RUN dnf clean all && \
    rm -rf /var/cache/yum ~/patches /root/rpms && \
    useradd -r -u 499 cluster-node-tuning-operator
ENTRYPOINT ["/usr/bin/cluster-node-tuning-operator"]
LABEL io.k8s.display-name="OpenShift cluster-node-tuning-operator" \
    io.k8s.description="This is a component of OpenShift and manages the lifecycle of node-level tuning." \
    io.openshift.release.operator=true \
    io.openshift.tags="openshift,tests,e2e,e2e-extension"
