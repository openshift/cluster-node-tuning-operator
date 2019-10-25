module github.com/openshift/cluster-node-tuning-operator

go 1.12

require (
	github.com/appscode/jsonpatch v2.0.1+incompatible // indirect
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/kevinburke/go-bindata v3.16.0+incompatible
	github.com/openshift/api v3.9.1-0.20191002145753-bb291c9def6c+incompatible
	github.com/openshift/library-go v0.0.0-20191024144423-664354b88b39
	gopkg.in/yaml.v2 v2.2.4

	// kubernetes-1.16.2
	k8s.io/api v0.0.0-20191016110408-35e52d86657a
	k8s.io/apimachinery v0.0.0-20191004115801-a2eda9f80ab8
	k8s.io/client-go v0.0.0-20190918160344-1fbdaa4c8d90

	k8s.io/klog v0.4.0
	sigs.k8s.io/controller-runtime v0.3.1-0.20191016212439-2df793d02076
	sigs.k8s.io/controller-tools v0.2.2-0.20190919191502-76a25b63325a
)
