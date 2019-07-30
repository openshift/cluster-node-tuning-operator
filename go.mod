module github.com/openshift/cluster-node-tuning-operator

go 1.12

require (
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-logr/zapr v0.1.1 // indirect
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/groupcache v0.0.0-20190702054246-869f871628b6 // indirect
	github.com/gomodules/jsonpatch v2.0.0+incompatible // indirect
	github.com/google/gofuzz v1.0.0 // indirect
	github.com/googleapis/gnostic v0.3.0 // indirect
	github.com/hashicorp/golang-lru v0.5.1 // indirect
	github.com/imdario/mergo v0.3.7 // indirect
	github.com/kevinburke/go-bindata v3.13.0+incompatible
	github.com/openshift/api v0.0.0-20190422184234-e939c41e1a45
	github.com/openshift/client-go v0.0.0-20190412095722-0255926f5393 // indirect
	github.com/openshift/library-go v0.0.0-20190422170926-a47c6814c02c
	github.com/pborman/uuid v1.2.0 // indirect
	github.com/prometheus/client_golang v1.0.0 // indirect
	go.uber.org/atomic v1.4.0 // indirect
	go.uber.org/zap v1.10.0 // indirect
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45 // indirect
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4 // indirect
	gopkg.in/yaml.v2 v2.2.2

	k8s.io/api v0.0.0-20190704095032-f4ca3d3bdf1d
	k8s.io/apimachinery v0.0.0-20190704094733-8f6ac2502e51
	k8s.io/client-go v11.0.1-0.20190708175433-62e1c231c5dc+incompatible

	k8s.io/klog v0.3.3
	k8s.io/kube-openapi v0.0.0-20190401085232-94e1e7b7574c // indirect
	k8s.io/utils v0.0.0-20190712204705-3dccf664f023 // indirect
	sigs.k8s.io/controller-runtime v0.2.0-beta.4
	sigs.k8s.io/controller-tools v0.2.0-beta.4
)
