module github.com/openshift/cluster-node-tuning-operator

go 1.23.0

toolchain go1.23.4

require (
	github.com/RHsyseng/operator-utils v1.4.13
	github.com/coreos/go-systemd v0.0.0-20191104093116-d3cd4ed1dbcf
	github.com/coreos/ignition v0.35.0
	github.com/coreos/ignition/v2 v2.21.0
	github.com/docker/go-units v0.5.0
	github.com/go-logr/stdr v1.2.2
	github.com/google/go-cmp v0.7.0
	github.com/jaypipes/ghw v0.16.0
	github.com/kevinburke/go-bindata v3.24.0+incompatible
	github.com/onsi/ginkgo/v2 v2.22.1
	github.com/onsi/gomega v1.36.2
	github.com/openshift-kni/debug-tools v0.2.3
	github.com/openshift-kni/k8sreporter v1.0.6
	github.com/openshift/api v0.0.0-20250407083304-00e6260bd8b8
	github.com/openshift/build-machinery-go v0.0.0-20250211133638-a00a772ae1a2
	github.com/openshift/client-go v0.0.0-20250402181141-b3bad3b645f2
	github.com/openshift/custom-resource-status v1.1.3-0.20220503160415-f2fdb4999d87
	github.com/openshift/hypershift/api v0.0.0-20240604072534-cd2d5291e2b7
	github.com/openshift/library-go v0.0.0-20240419113445-f1541d628746
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.21.1
	github.com/spf13/cobra v1.9.1
	github.com/spf13/pflag v1.0.6
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/ini.v1 v1.67.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.32.6
	k8s.io/apiextensions-apiserver v0.32.3
	k8s.io/apimachinery v0.32.6
	k8s.io/client-go v0.32.6
	k8s.io/code-generator v0.32.3
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.130.1
	k8s.io/kubelet v0.32.6
	k8s.io/utils v0.0.0-20250321185631-1f6e0b77f77e
	kubevirt.io/qe-tools v0.1.8
	sigs.k8s.io/cluster-api v1.8.11
	sigs.k8s.io/controller-runtime v0.19.7
	sigs.k8s.io/controller-tools v0.17.3
	sigs.k8s.io/yaml v1.4.0
)

require (
	github.com/StackExchange/wmi v1.2.1 // indirect
	github.com/asaskevich/govalidator v0.0.0-20230301143203-a9d515a09cc2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.5.0 // indirect
	github.com/coreos/vcontext v0.0.0-20231102161604-685dc7299dc5 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emicklei/go-restful/v3 v3.12.1 // indirect
	github.com/evanphx/json-patch v5.9.0+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/analysis v0.21.4 // indirect
	github.com/go-openapi/errors v0.20.3 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/loads v0.21.2 // indirect
	github.com/go-openapi/spec v0.20.7 // indirect
	github.com/go-openapi/strfmt v0.21.3 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-openapi/validate v0.22.0 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/gobuffalo/flect v1.0.3 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/cadvisor v0.52.1 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20241210010833-40e02aabc2ad // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.1 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jaypipes/pcidb v1.0.1 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mistifyio/go-zfs v2.1.2-0.20190413222219-f784269be439+incompatible // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/moby/spdystream v0.5.0 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mxk/go-flowrate v0.0.0-20140419014527-cca7078d478f // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.62.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/robfig/cron v1.2.0 // indirect
	github.com/vincent-petithory/dataurl v1.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.mongodb.org/mongo-driver v1.11.1 // indirect
	go.opentelemetry.io/otel v1.34.0 // indirect
	go.opentelemetry.io/otel/trace v1.34.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	go4.org v0.0.0-20230225012048-214862532bf5 // indirect
	golang.org/x/exp v0.0.0-20240719175910-8a7402abbf56 // indirect
	golang.org/x/mod v0.23.0 // indirect
	golang.org/x/net v0.37.0 // indirect
	golang.org/x/oauth2 v0.28.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/term v0.30.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.10.0 // indirect
	golang.org/x/tools v0.30.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250227231956-55c901821b1e // indirect
	google.golang.org/grpc v1.70.0 // indirect
	google.golang.org/protobuf v1.36.5 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	howett.net/plist v1.0.1 // indirect
	k8s.io/apiserver v0.32.3 // indirect
	k8s.io/component-base v0.32.3 // indirect
	k8s.io/gengo/v2 v2.0.0-20240911193312-2b36238f13e9 // indirect
	k8s.io/kube-aggregator v0.32.3 // indirect
	k8s.io/kube-openapi v0.0.0-20241105132330-32ad38e42d3f // indirect
	sigs.k8s.io/json v0.0.0-20241014173422-cfa47c3a1cc8 // indirect
	sigs.k8s.io/kube-storage-version-migrator v0.0.6-0.20230721195810-5c8923c5ff96 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.6.0 // indirect
)

// Pinned to kubernetes-1.32.3
replace (
	k8s.io/api => k8s.io/api v0.32.3
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.32.3
	k8s.io/apimachinery => k8s.io/apimachinery v0.32.3
	k8s.io/apiserver => k8s.io/apiserver v0.32.3
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.32.3
	k8s.io/client-go => k8s.io/client-go v0.32.3
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.32.3
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.32.3
	k8s.io/code-generator => k8s.io/code-generator v0.32.3
	k8s.io/component-base => k8s.io/component-base v0.32.3
	k8s.io/component-helpers => k8s.io/component-helpers v0.32.3
	k8s.io/controller-manager => k8s.io/controller-manager v0.32.3
	k8s.io/cri-api => k8s.io/cri-api v0.32.3
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.32.3
	k8s.io/dynamic-resource-allocation => k8s.io/dynamic-resource-allocation v0.32.3
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.32.3
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.32.3
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20241105132330-32ad38e42d3f
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.32.3
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.32.3
	k8s.io/kubectl => k8s.io/kubectl v0.32.3
	k8s.io/kubelet => k8s.io/kubelet v0.32.3
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.32.3
	k8s.io/metrics => k8s.io/metrics v0.32.3
	k8s.io/mount-utils => k8s.io/mount-utils v0.32.3
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.32.3
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.32.3
)
