module github.com/openshift/cluster-node-tuning-operator

go 1.15

require (
	github.com/coreos/ignition v0.35.0 // indirect
	github.com/coreos/ignition/v2 v2.7.0
	github.com/kevinburke/go-bindata v3.16.0+incompatible
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	github.com/openshift/api v0.0.0-20210112145312-790e0a84e3e0
	github.com/openshift/client-go v0.0.0-20210112165513-ebc401615f47
	github.com/openshift/crd-schema-gen v1.0.0 // indirect
	github.com/openshift/library-go v0.0.0-20210113192829-cfbb3f4c80c2
	github.com/openshift/machine-config-operator v0.0.1-0.20210130231751-a060922f97f1
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.7.1
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50 // indirect
	go4.org v0.0.0-20201209231011-d4a079459e60 // indirect
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/ini.v1 v1.51.0
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	k8s.io/code-generator v0.20.2
	k8s.io/klog/v2 v2.4.0
	k8s.io/utils v0.0.0-20201110183641-67b214c5f920
	sigs.k8s.io/controller-tools v0.4.0
)

// Pinned to kubernetes-1.20.2
replace (
	k8s.io/api => k8s.io/api v0.20.2
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.20.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.20.2
	k8s.io/apiserver => k8s.io/apiserver v0.20.2
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.20.2
	k8s.io/client-go => k8s.io/client-go v0.20.2
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.20.2
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.20.2
	k8s.io/code-generator => k8s.io/code-generator v0.20.2
	k8s.io/component-base => k8s.io/component-base v0.20.2
	k8s.io/cri-api => k8s.io/cri-api v0.20.2
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.20.2
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.20.2
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.20.2
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.20.2
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.20.2
	k8s.io/kubectl => k8s.io/kubectl v0.20.2
	k8s.io/kubelet => k8s.io/kubelet v0.20.2
	k8s.io/kubernetes => k8s.io/kubernetes v1.20.2
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.20.2
	k8s.io/metrics => k8s.io/metrics v0.20.2
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.20.2
)

// Other pinned deps
replace (
	github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2 // Fixes for CVE-2021-3121; see RHBZ1921650
	github.com/openshift/machine-config-operator => github.com/openshift/machine-config-operator v0.0.1-0.20210130231751-a060922f97f1 // 4.7 as of 2021-02-01
	golang.org/x/text => golang.org/x/text v0.3.5 // Fixes for CVE-2020-28852; see RHBZ1913338
)
