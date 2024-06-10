FROM registry.ci.openshift.org/openshift/release:rhel-9-release-golang-1.22-openshift-4.17 AS builder
WORKDIR /go/src/github.com/openshift/cluster-node-tuning-operator
COPY . .
RUN make update-tuned-submodule
RUN make build

FROM quay.io/centos/centos:stream9
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/_output/cluster-node-tuning-operator /usr/bin/
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/_output/performance-profile-creator /usr/bin/
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/_output/gather-sysinfo /usr/bin/

ENV ASSETS_DIR=/root/assets
COPY assets $ASSETS_DIR

COPY hack/dockerfile_install_support.sh /tmp
RUN /bin/bash /tmp/dockerfile_install_support.sh

COPY manifests/*.yaml manifests/image-references /manifests/
ENV APP_ROOT=/var/lib/ocp-tuned
ENV PATH=${APP_ROOT}/bin:${PATH}
ENV HOME=${APP_ROOT}
ENV SYSTEMD_IGNORE_CHROOT=1
WORKDIR ${APP_ROOT}
RUN dnf clean all && \
    rm -rf /var/cache/yum ~/patches /root/rpms && \
    useradd -r -u 499 cluster-node-tuning-operator
ENTRYPOINT ["/usr/bin/cluster-node-tuning-operator"]
LABEL io.k8s.display-name="OpenShift cluster-node-tuning-operator" \
      io.k8s.description="This is a component of OpenShift and manages the lifecycle of node-level tuning." \
      io.openshift.release.operator=true
