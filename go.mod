module github.com/openshift/cluster-node-tuning-operator

go 1.12

require (
	github.com/evanphx/json-patch v4.5.0+incompatible // indirect
	github.com/golang/groupcache v0.0.0-20180513044358-24b0969c4cb7 // indirect
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/imdario/mergo v0.3.6 // indirect
	github.com/kevinburke/go-bindata v3.16.0+incompatible
	github.com/openshift/api v3.9.1-0.20191002145753-bb291c9def6c+incompatible
	github.com/openshift/client-go v0.0.0-20191022152013-2823239d2298
	github.com/openshift/crd-schema-gen v1.0.0
	github.com/openshift/library-go v0.0.0-20191024144423-664354b88b39
	gopkg.in/yaml.v2 v2.2.4

	// kubernetes-1.16.2
	k8s.io/api v0.0.0-20191109101512-6d4d1612ba53
	k8s.io/apiextensions-apiserver v0.0.0-20191109110701-3fdecfd8e730 // indirect
	k8s.io/apimachinery v0.0.0-20191109100837-dffb012825f2
	k8s.io/client-go v0.0.0-20191109102209-3c0d1af94be5
	k8s.io/code-generator v0.0.0-20191109100332-a9a0d9c0b3aa
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-tools v0.2.2 // indirect
)
