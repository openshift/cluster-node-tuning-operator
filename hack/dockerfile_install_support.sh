#!/bin/bash

set -euo pipefail
set -o xtrace

source /etc/os-release
if [[ "${ID}" == "centos" ]]; then

  # CentOS OKD build
  BUILD_INSTALL_PKGS="gcc git rpm-build make desktop-file-utils patch dnf-plugins-core"
  dnf install --setopt=tsflags=nodocs -y ${BUILD_INSTALL_PKGS}
  cd /root/assets/tuned/tuned
  LC_COLLATE=C cat ../patches/*.diff | patch -Np1
  dnf build-dep tuned.spec -y
  make rpm PYTHON=/usr/bin/python3
  rm -rf /root/rpmbuild/RPMS/noarch/{tuned-gtk*,tuned-utils*,tuned-profiles-compat*};
  dnf --setopt=protected_packages= history -y undo 0  # Remove builddep

  INSTALL_PKGS="nmap-ncat procps-ng pciutils"
  cp -r /root/assets/bin /usr/local/bin
  cp -r /root/rpmbuild/RPMS/noarch /root/rpms
  mkdir -p /etc/grub.d/ /boot /var/lib/ocp-tuned
  dnf install --setopt=tsflags=nodocs -y ${INSTALL_PKGS}
  rpm -V ${INSTALL_PKGS}
  dnf --setopt=tsflags=nodocs -y install /root/rpms/*.rpm
  rm -rf /etc/tuned/recommend.d
  echo auto > /etc/tuned/profile_mode
  sed -Ei 's|^#?\s*enable_unix_socket\s*=.*$|enable_unix_socket = 1|;s|^#?\s*rollback\s*=.*$|rollback = not_on_exit|;s|^#?\s*profile_dirs\s*=.*$|profile_dirs = /var/lib/ocp-tuned/profiles,/usr/lib/tuned|' /etc/tuned/tuned-main.conf;

  # Clean up build tools to remove image footprint
  dnf remove --setopt=protected_packages= -y ${BUILD_INSTALL_PKGS}
  dnf autoremove -y

else

  # RHEL OCP build
  INSTALL_PKGS=" \
     tuned tuned-profiles-atomic tuned-profiles-cpu-partitioning tuned-profiles-mssql tuned-profiles-nfv tuned-profiles-nfv-guest \
     tuned-profiles-nfv-host tuned-profiles-openshift tuned-profiles-oracle tuned-profiles-postgresql tuned-profiles-realtime \
     tuned-profiles-sap tuned-profiles-sap-hana tuned-profiles-spectrumscale \
     nmap-ncat procps-ng pciutils"
  cp -r /root/assets/bin /usr/local/bin
  mkdir -p /etc/grub.d/ /boot /var/lib/ocp-tuned
  dnf install --setopt=tsflags=nodocs -y ${INSTALL_PKGS}
  rm -rf /etc/tuned/recommend.d
  echo auto > /etc/tuned/profile_mode
  sed -Ei 's|^#?\s*enable_unix_socket\s*=.*$|enable_unix_socket = 1|;s|^#?\s*rollback\s*=.*$|rollback = not_on_exit|;s|^#?\s*profile_dirs\s*=.*$|profile_dirs = /var/lib/ocp-tuned/profiles,/usr/lib/tuned|' /etc/tuned/tuned-main.conf;

fi

touch /etc/sysctl.conf
