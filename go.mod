module github.com/openshift/cluster-node-tuning-operator

go 1.18

require (
	github.com/RHsyseng/operator-utils v0.0.0-20200213165520-1a022eb07a43
	github.com/coreos/go-systemd v0.0.0-20190719114852-fd7a80b32e1f
	github.com/coreos/ignition v0.35.0
	github.com/coreos/ignition/v2 v2.9.0
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32
	github.com/google/go-cmp v0.5.9
	github.com/jaypipes/ghw v0.8.1-0.20210605191321-eb162add542b
	github.com/kevinburke/go-bindata v3.16.0+incompatible
	github.com/onsi/ginkgo/v2 v2.5.1
	github.com/onsi/gomega v1.24.0
	github.com/openshift/api v0.0.0-20220628012702-9368b92de0ab
	github.com/openshift/build-machinery-go v0.0.0-20211213093930-7e33a7eb4ce3
	github.com/openshift/client-go v0.0.0-20220603133046-984ee5ebedcf
	github.com/openshift/custom-resource-status v0.0.0-20200602122900-c002fd1547ca
	github.com/openshift/library-go v0.0.0-20220525173854-9b950a41acdc
	github.com/openshift/machine-config-operator v0.0.1-0.20220706180257-35d79621a587
	github.com/operator-framework/api v0.10.7
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.12.1
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.4.0
	github.com/spf13/pflag v1.0.5
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/ini.v1 v1.62.0
	k8s.io/api v0.24.2
	k8s.io/apiextensions-apiserver v0.24.1
	k8s.io/apimachinery v0.24.2
	k8s.io/client-go v0.24.2
	k8s.io/code-generator v0.24.2
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.60.1
	k8s.io/kubelet v0.24.1
	k8s.io/kubernetes v0.24.1
	k8s.io/utils v0.0.0-20220210201930-3a6ce19ff2f9
	kubevirt.io/qe-tools v0.1.8
	sigs.k8s.io/controller-runtime v0.11.0
	sigs.k8s.io/controller-tools v0.6.2
	sigs.k8s.io/yaml v1.3.0
)

require (
	github.com/PuerkitoBio/purell v1.1.1 // indirect
	github.com/PuerkitoBio/urlesc v0.0.0-20170810143723-de5bf2ad4578 // indirect
	github.com/StackExchange/wmi v1.2.1 // indirect
	github.com/asaskevich/govalidator v0.0.0-20200907205600-7a23bdc65eef // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/coreos/go-json v0.0.0-20170920214419-6a2fe990e083 // indirect
	github.com/coreos/go-semver v0.3.0 // indirect
	github.com/coreos/go-systemd/v22 v22.3.2 // indirect
	github.com/coreos/vcontext v0.0.0-20201120045928-b0e13dab675c // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/distribution v2.8.1+incompatible // indirect
	github.com/emicklei/go-restful v2.10.0+incompatible // indirect
	github.com/evanphx/json-patch v4.12.0+incompatible // indirect
	github.com/fatih/color v1.12.0 // indirect
	github.com/fsnotify/fsnotify v1.5.1 // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/go-ole/go-ole v1.2.5 // indirect
	github.com/go-openapi/analysis v0.21.2 // indirect
	github.com/go-openapi/errors v0.19.9 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.19.6 // indirect
	github.com/go-openapi/loads v0.21.1 // indirect
	github.com/go-openapi/spec v0.20.4 // indirect
	github.com/go-openapi/strfmt v0.21.2 // indirect
	github.com/go-openapi/swag v0.21.1 // indirect
	github.com/go-openapi/validate v0.21.0 // indirect
	github.com/go-stack/stack v1.8.0 // indirect
	github.com/gobuffalo/flect v0.2.3 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/gnostic v0.5.7-v3refs // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/uuid v1.2.0 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/jaypipes/pcidb v0.6.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.7.6 // indirect
	github.com/mattn/go-colorable v0.1.8 // indirect
	github.com/mattn/go-isatty v0.0.12 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.4.1 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/prometheus/common v0.32.1 // indirect
	github.com/prometheus/procfs v0.7.3 // indirect
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50 // indirect
	go.mongodb.org/mongo-driver v1.7.5 // indirect
	golang.org/x/mod v0.6.0 // indirect
	golang.org/x/net v0.1.0 // indirect
	golang.org/x/oauth2 v0.0.0-20220411215720-9780585627b5 // indirect
	golang.org/x/sys v0.2.0 // indirect
	golang.org/x/term v0.1.0 // indirect
	golang.org/x/text v0.4.0 // indirect
	golang.org/x/time v0.0.0-20220210224613-90d013bbcef8 // indirect
	golang.org/x/tools v0.2.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.2.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/protobuf v1.28.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	howett.net/plist v0.0.0-20181124034731-591f970eefbb // indirect
	k8s.io/apiserver v0.24.2 // indirect
	k8s.io/component-base v0.24.2 // indirect
	k8s.io/gengo v0.0.0-20211129171323-c02415ce4185 // indirect
	k8s.io/kube-openapi v0.0.0-20220328201542-3ee0da9b0b42 // indirect
	sigs.k8s.io/json v0.0.0-20211208200746-9f7c6b3444d2 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.1 // indirect
)

// Pinned to kubernetes-1.24.2
replace (
	k8s.io/api => k8s.io/api v0.24.2
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.24.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.24.2
	k8s.io/apiserver => k8s.io/apiserver v0.24.2
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.24.2
	k8s.io/client-go => k8s.io/client-go v0.24.2
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.24.2
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.24.2
	k8s.io/code-generator => k8s.io/code-generator v0.24.2
	k8s.io/component-base => k8s.io/component-base v0.24.2
	k8s.io/component-helpers => k8s.io/component-helpers v0.24.2
	k8s.io/controller-manager => k8s.io/controller-manager v0.24.2
	k8s.io/cri-api => k8s.io/cri-api v0.24.2
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.24.2
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.24.2
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.24.2
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.24.2
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.24.2
	k8s.io/kubectl => k8s.io/kubectl v0.24.2
	k8s.io/kubelet => k8s.io/kubelet v0.24.2
	k8s.io/kubernetes => k8s.io/kubernetes v1.24.2
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.24.2
	k8s.io/metrics => k8s.io/metrics v0.24.2
	k8s.io/mount-utils => k8s.io/mount-utils v0.24.2
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.24.2
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.24.2
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.11.1
	sigs.k8s.io/controller-tools => sigs.k8s.io/controller-tools v0.7.0
)

// Other PAO pinned deps
replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v14.2.0+incompatible
	github.com/coreos/prometheus-operator => github.com/coreos/prometheus-operator v0.40.0
	github.com/mtrmac/gpgme => github.com/mtrmac/gpgme v0.1.1
	github.com/openshift/machine-config-operator => github.com/openshift/machine-config-operator v0.0.1-0.20220203091316-d3010b34d344 // release-4.11
)

replace vbom.ml/util => github.com/fvbommel/util v0.0.0-20180919145318-efcd4e0f9787
