module github.com/openshift/cluster-node-tuning-operator

go 1.26.0

require (
	github.com/RHsyseng/operator-utils v1.4.13
	github.com/coreos/go-systemd v0.0.0-20191104093116-d3cd4ed1dbcf
	github.com/coreos/ignition/v2 v2.28.0
	github.com/docker/go-units v0.5.0
	github.com/go-logr/stdr v1.2.2
	github.com/google/go-cmp v0.7.0
	github.com/jaypipes/ghw v0.26.0
	github.com/kevinburke/go-bindata v3.24.0+incompatible
	// The version here is only the MVS floor required by controller-tools; the
	// implementation actually compiled is the OpenShift fork selected by the
	// replace directive below.  This does not mean ginkgo has been upgraded.
	github.com/onsi/ginkgo/v2 v2.33.0
	github.com/onsi/gomega v1.44.0
	github.com/openshift-eng/openshift-tests-extension v0.0.0-20261026111431-8782e72e5a1f
	github.com/openshift-kni/debug-tools v0.2.6
	github.com/openshift-kni/k8sreporter v1.1.0
	github.com/openshift/api v0.0.0-20261103105328-7912bdb54653
	github.com/openshift/build-machinery-go v0.0.0-20260902143904-520f675c892b
	github.com/openshift/client-go v0.0.0-20261027103500-48133ed95dad
	github.com/openshift/controller-runtime-common v0.0.0-20261026124434-bdfc411d93cd
	github.com/openshift/custom-resource-status v1.1.2
	github.com/openshift/hypershift/api v0.0.0-20261104055340-d3173a012ac0
	github.com/openshift/library-go v0.0.0-20261104061757-ddc03221f6b8
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.25.0
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	golang.org/x/sync v0.24.0
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/ini.v1 v1.67.3
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.37.2
	k8s.io/apiextensions-apiserver v0.37.2
	k8s.io/apimachinery v0.37.2
	k8s.io/client-go v0.37.2
	k8s.io/code-generator v0.37.2
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.140.0
	k8s.io/kubelet v0.37.2
	k8s.io/utils v0.0.0-20261023123148-ca04ba58ecab
	kubevirt.io/qe-tools v0.1.8
	sigs.k8s.io/controller-runtime v0.25.2
	sigs.k8s.io/controller-tools v0.23.0
	sigs.k8s.io/yaml v1.6.0
)

require (
	cel.dev/expr v0.25.3 // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/coreos/vcontext v0.0.0-20260306102053-7a68b5426c74 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emicklei/go-restful/v3 v3.14.0 // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/fxamacker/cbor/v2 v2.10.0 // indirect
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/analysis v1.0.1 // indirect
	github.com/go-openapi/errors v0.22.9 // indirect
	github.com/go-openapi/jsonpointer v1.0.2 // indirect
	github.com/go-openapi/jsonreference v1.0.3 // indirect
	github.com/go-openapi/loads v0.25.3 // indirect
	github.com/go-openapi/spec v1.0.1 // indirect
	github.com/go-openapi/strfmt v0.27.4 // indirect
	github.com/go-openapi/swag v0.29.2 // indirect
	github.com/go-openapi/swag/cmdutils v0.29.2 // indirect
	github.com/go-openapi/swag/conv v0.29.2 // indirect
	github.com/go-openapi/swag/fileutils v0.29.2 // indirect
	github.com/go-openapi/swag/jsonutils v0.29.2 // indirect
	github.com/go-openapi/swag/loading v0.29.2 // indirect
	github.com/go-openapi/swag/mangling v0.29.2 // indirect
	github.com/go-openapi/swag/netutils v0.29.2 // indirect
	github.com/go-openapi/swag/pools v0.29.2 // indirect
	github.com/go-openapi/swag/stringutils v0.29.2 // indirect
	github.com/go-openapi/swag/typeutils v0.29.2 // indirect
	github.com/go-openapi/swag/yamlutils v0.29.2 // indirect
	github.com/go-openapi/validate v1.0.1 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/gobuffalo/flect v1.0.3 // indirect
	github.com/google/cadvisor v0.57.0 // indirect
	github.com/google/cel-go v0.31.0 // indirect
	github.com/google/gnostic-models v0.7.1 // indirect
	github.com/google/pprof v0.0.0-20261102214848-0031fcd306b0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jaypipes/pcidb v1.1.1 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mattn/go-colorable v0.1.16 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/moby/spdystream v0.5.1 // indirect
	github.com/moby/sys/mountinfo v0.7.3 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/oklog/ulid/v2 v2.1.2 // indirect
	github.com/pandatix/go-abnf v0.5.5 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/common v0.73.1 // indirect
	github.com/prometheus/procfs v0.23.0 // indirect
	github.com/robfig/cron v1.2.0 // indirect
	github.com/vincent-petithory/dataurl v1.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	go.yaml.in/yaml/v2 v2.4.5 // indirect
	go.yaml.in/yaml/v3 v3.0.6 // indirect
	golang.org/x/exp v0.0.0-20261014143641-f0251eb4aa3e // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/term v0.47.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.5.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20261026165753-2ffab32bce7e // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261026165753-2ffab32bce7e // indirect
	google.golang.org/grpc v1.85.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	howett.net/plist v1.0.2-0.20250314012144-ee69052608d9 // indirect
	k8s.io/apiserver v0.37.2 // indirect
	k8s.io/component-base v0.37.2 // indirect
	k8s.io/gengo/v2 v2.0.0-20260408192533-25e2208e0dc3 // indirect
	k8s.io/kube-aggregator v0.37.2 // indirect
	k8s.io/kube-openapi v0.0.0-20260721132016-d427ff9ee9ad // indirect
	k8s.io/streaming v0.37.2 // indirect
	sigs.k8s.io/json v0.0.0-20260909141634-11ed52e25bc5 // indirect
	sigs.k8s.io/kube-storage-version-migrator v0.0.6-0.20230721195810-5c8923c5ff96 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.4.2 // indirect
)

// Required for openshift-tests-extension compatibility (uses OpenShift's ginkgo fork).
// Without this override cluster-node-tuning-operator-test-ext doesn't compile.
replace github.com/onsi/ginkgo/v2 => github.com/openshift/onsi-ginkgo/v2 v2.6.1-0.20260922200432-2b15107f443d
