module github.com/openshift/cluster-node-tuning-operator

go 1.13

require (
	github.com/evanphx/json-patch v4.5.0+incompatible // indirect
	github.com/golang/groupcache v0.0.0-20180513044358-24b0969c4cb7 // indirect
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/imdario/mergo v0.3.6 // indirect
	github.com/kevinburke/go-bindata v3.16.0+incompatible
	github.com/openshift/api v0.0.0-20191217141120-791af96035a5
	github.com/openshift/client-go v0.0.0-20191216194936-57f413491e9e
	github.com/openshift/crd-schema-gen v1.0.0
	github.com/openshift/library-go v0.0.0-20200106191802-9821002633e8
	gopkg.in/yaml.v2 v2.2.4
	// kubernetes-1.17
	k8s.io/api v0.17.0
	k8s.io/apimachinery v0.17.1-beta.0
	k8s.io/client-go v0.17.0
	k8s.io/code-generator v0.17.0
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-tools v0.2.2 // indirect
)
