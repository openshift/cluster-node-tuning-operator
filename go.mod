module github.com/openshift/cluster-node-tuning-operator

go 1.15

require (
	github.com/coreos/ignition v0.35.0 // indirect
	github.com/coreos/ignition/v2 v2.4.1
	github.com/kevinburke/go-bindata v3.16.0+incompatible
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	github.com/openshift/api v0.0.0-20200901182017-7ac89ba6b971
	github.com/openshift/build-machinery-go v0.0.0-20200819073603-48aa266c95f7 // indirect
	github.com/openshift/client-go v0.0.0-20200827190008-3062137373b5
	github.com/openshift/crd-schema-gen v1.0.0 // indirect
	github.com/openshift/library-go v0.0.0-20200911100307-610c6e9e90b8
	github.com/openshift/machine-config-operator v4.2.0-alpha.0.0.20190917115525-033375cbe820+incompatible
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.7.1
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50 // indirect
	golang.org/x/text v0.3.3 // indirect
	gopkg.in/fsnotify.v1 v1.4.7
	k8s.io/api v0.19.0
	k8s.io/apimachinery v0.19.0
	k8s.io/client-go v0.19.0
	k8s.io/code-generator v0.19.0
	k8s.io/klog/v2 v2.3.0
	k8s.io/utils v0.0.0-20200729134348-d5654de09c73
	sigs.k8s.io/controller-tools v0.4.0
)

// Pinned to kubernetes-1.19.0
replace (
	k8s.io/api => k8s.io/api v0.19.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.19.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.19.0
	k8s.io/apiserver => k8s.io/apiserver v0.19.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.19.0
	k8s.io/client-go => k8s.io/client-go v0.19.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.19.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.19.0
	k8s.io/code-generator => k8s.io/code-generator v0.19.0
	k8s.io/component-base => k8s.io/component-base v0.19.0
	k8s.io/cri-api => k8s.io/cri-api v0.19.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.19.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.19.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.19.0
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.19.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.19.0
	k8s.io/kubectl => k8s.io/kubectl v0.19.0
	k8s.io/kubelet => k8s.io/kubelet v0.19.0
	k8s.io/kubernetes => k8s.io/kubernetes v1.19.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.19.0
	k8s.io/metrics => k8s.io/metrics v0.19.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.19.0
)

// Other pinned deps
replace github.com/openshift/machine-config-operator => github.com/openshift/machine-config-operator v0.0.1-0.20200913004441-7eba765c69c9 // 4.6 as of 2020-09-15
