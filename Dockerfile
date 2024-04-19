FROM registry.ci.openshift.org/openshift/release:golang-1.20 AS builder
WORKDIR /go/src/github.com/openshift/cluster-node-tuning-operator
COPY . .
RUN make update-tuned-submodule
RUN make build

FROM quay.io/centos/centos:stream9 as tuned
WORKDIR /root
COPY assets /root/assets
RUN INSTALL_PKGS=" \
      gcc git rpm-build make desktop-file-utils patch dnf-plugins-core \
      " && \
    dnf install --setopt=tsflags=nodocs -y $INSTALL_PKGS && \
    cd assets/tuned/tuned && \
    LC_COLLATE=C cat ../patches/*.diff | patch -Np1 && \
    dnf build-dep tuned.spec -y && \
    make rpm PYTHON=/usr/bin/python3 && \
    rm -rf /root/rpmbuild/RPMS/noarch/{tuned-gtk*,tuned-utils*,tuned-profiles-compat*}

FROM quay.io/centos/centos:stream9
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/_output/cluster-node-tuning-operator /usr/bin/
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/_output/performance-profile-creator /usr/bin/
COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/_output/gather-sysinfo /usr/bin/
COPY manifests/*.yaml manifests/image-references /manifests/
ENV APP_ROOT=/var/lib/ocp-tuned
ENV PATH=${APP_ROOT}/bin:${PATH}
ENV HOME=${APP_ROOT}
ENV SYSTEMD_IGNORE_CHROOT=1
WORKDIR ${APP_ROOT}
COPY --from=tuned   /root/assets ${APP_ROOT}
COPY --from=tuned   /root/rpmbuild/RPMS/noarch /root/rpms
RUN INSTALL_PKGS=" \
      nmap-ncat procps-ng pciutils \
      " && \
    mkdir -p /etc/grub.d/ /boot && \
    dnf install --setopt=tsflags=nodocs -y $INSTALL_PKGS && \
    rpm -V $INSTALL_PKGS && \
    dnf --setopt=tsflags=nodocs -y install /root/rpms/*.rpm && \
    rm -rf /var/lib/ocp-tuned/{tuned,performanceprofile} && \
    sed -Ei 's|^#?\s*enable_unix_socket\s*=.*$|enable_unix_socket = 1|;s|^#?\s*rollback\s*=.*$|rollback = not_on_exit|' /etc/tuned/tuned-main.conf && \
    touch /etc/sysctl.conf $APP_ROOT/provider && \
    dnf clean all && \
    rm -rf /var/cache/yum ~/patches /root/rpms && \
    useradd -r -u 499 cluster-node-tuning-operator
ENTRYPOINT ["/usr/bin/cluster-node-tuning-operator"]
LABEL io.k8s.display-name="OpenShift cluster-node-tuning-operator" \
      io.k8s.description="This is a component of OpenShift and manages the lifecycle of node-level tuning." \
      io.openshift.release.operator=true
