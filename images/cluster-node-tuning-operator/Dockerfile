FROM centos:7

RUN yum install -y golang make

ENV GOPATH /go
RUN mkdir $GOPATH

COPY . $GOPATH/src/github.com/openshift/cluster-node-tuning-operator

RUN cd $GOPATH/src/github.com/openshift/cluster-node-tuning-operator \
    && make build \
    && cp $GOPATH/src/github.com/openshift/cluster-node-tuning-operator/cluster-node-tuning-operator /usr/bin/

RUN yum remove -y golang make

ENTRYPOINT ["/usr/bin/cluster-node-tuning-operator"]

USER 1001

LABEL io.k8s.display-name="OpenShift cluster-node-tuning-operator" \
      io.k8s.description="This is a component of OpenShift Container Platform and manages the lifecycle of node-level tuning." \
      maintainer="Jiri Mencak <jmencak@redhat.com>"
