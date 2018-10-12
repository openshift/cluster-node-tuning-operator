FROM openshift/origin-release:golang-1.10 as builder
COPY . /go/src/github.com/openshift/cluster-node-tuning-operator/
RUN cd /go/src/github.com/openshift/cluster-node-tuning-operator && make build

FROM centos:7
LABEL io.k8s.display-name="OpenShift cluster-node-tuning-operator" \
      io.k8s.description="This is a component of OpenShift Container Platform and manages the lifecycle of node-level tuning." \
      io.openshift.release.operator=true

COPY --from=builder /go/src/github.com/openshift/cluster-node-tuning-operator/cluster-node-tuning-operator /usr/bin/
COPY manifests /manifests

RUN useradd cluster-node-tuning-operator
USER cluster-node-tuning-operator

ENTRYPOINT ["/usr/bin/cluster-node-tuning-operator"]
