module github.com/openshift/cluster-node-tuning-operator

go 1.16

require (
	github.com/coreos/go-systemd v0.0.0-20190719114852-fd7a80b32e1f
	github.com/coreos/ignition v0.35.0
	github.com/coreos/ignition/v2 v2.7.0
	github.com/ghodss/yaml v1.0.0
	github.com/google/uuid v1.1.2
	github.com/kevinburke/go-bindata v3.16.0+incompatible
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	github.com/openshift/api v0.0.0-20210521075222-e273a339932a
	github.com/openshift/build-machinery-go v0.0.0-20210423112049-9415d7ebd33e
	github.com/openshift/client-go v0.0.0-20210521082421-73d9475a9142
	github.com/openshift/custom-resource-status v1.1.0
	github.com/openshift/library-go v0.0.0-20210521084623-7392ea9b02ca
	github.com/openshift/machine-config-operator v0.0.1-0.20210514234214-c415ce6aed25
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50 // indirect
	golang.org/x/net v0.0.0-20210525063256-abc453219eb5 // indirect
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/ini.v1 v1.51.0
	k8s.io/api v0.22.0
	k8s.io/apimachinery v0.22.0
	k8s.io/client-go v0.22.0
	k8s.io/code-generator v0.22.0
	k8s.io/klog/v2 v2.9.0
	k8s.io/kubelet v0.21.0-rc.0
	k8s.io/kubernetes v0.0.0-00010101000000-000000000000
	k8s.io/utils v0.0.0-20210802155522-efc7438f0176
	sigs.k8s.io/controller-runtime v0.9.2
	sigs.k8s.io/controller-tools v0.6.1
)

// Pinned to kubernetes-1.22.0
replace (
	k8s.io/api => k8s.io/api v0.22.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.22.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.22.0
	k8s.io/apiserver => k8s.io/apiserver v0.22.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.22.0
	k8s.io/client-go => k8s.io/client-go v0.22.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.22.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.22.0
	k8s.io/code-generator => k8s.io/code-generator v0.22.0
	k8s.io/component-base => k8s.io/component-base v0.22.0
	k8s.io/component-helpers => k8s.io/component-helpers v0.22.0
	k8s.io/controller-manager => k8s.io/controller-manager v0.22.0
	k8s.io/cri-api => k8s.io/cri-api v0.22.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.22.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.22.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.22.0
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.22.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.22.0
	k8s.io/kubectl => k8s.io/kubectl v0.22.0
	k8s.io/kubelet => k8s.io/kubelet v0.22.0
	k8s.io/kubernetes => k8s.io/kubernetes v1.22.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.22.0
	k8s.io/metrics => k8s.io/metrics v0.22.0
	k8s.io/mount-utils => k8s.io/mount-utils v0.22.0
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.22.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.22.0
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.9.2
	sigs.k8s.io/controller-tools => sigs.k8s.io/controller-tools v0.6.1
)
