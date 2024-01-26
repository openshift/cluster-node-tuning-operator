package validate

import (
	"flag"
	"fmt"
	"os"

	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog"

	performanceprofilev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
)

var (
	manifestScheme = runtime.NewScheme()
	codecFactory   serializer.CodecFactory
	runtimeDecoder runtime.Decoder
)

func init() {
	utilruntime.Must(performanceprofilev2.AddToScheme(manifestScheme))

	codecFactory = serializer.NewCodecFactory(manifestScheme)
	runtimeDecoder = codecFactory.UniversalDecoder(
		performanceprofilev2.GroupVersion,
	)
}

type validateOpts struct {
	inDir string
}

func NewValidateCommand() *cobra.Command {
	options := validateOpts{}

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate PerformanceProfiles manifests",
		Run: func(cmd *cobra.Command, args []string) {
			if err := options.Validate(); err != nil {
				klog.Fatal(err)
			}

			if err := options.Run(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	addKlogFlags(cmd)
	options.AddFlags(cmd.Flags())
	return cmd
}

func (r *validateOpts) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&r.inDir, "input-dir", "", "Input path for the directory with the PerformanceProfiles to validate. (Can be a comma separated list of directories.)")
	//fs.StringVar(&r.assetsOutDir, "asset-output-dir", r.assetsOutDir, "Output path for the rendered manifests.")
	// environment variables has precedence over standard input
	r.readFlagsFromEnv()
}

func (r *validateOpts) readFlagsFromEnv() {
	if inDir := os.Getenv("ASSET_INPUT_DIR"); len(inDir) > 0 {
		r.inDir = inDir
	}
}

func (r *validateOpts) Validate() error {
	if len(r.inDir) == 0 {
		return fmt.Errorf("input-dir must be specified")
	}
	return nil
}

type result struct {
	perfProfiles *performanceprofilev2.PerformanceProfile
	filename     string
	problems     []error
	skipped      bool
}


func (r *validateOpts) Run() error {
	klog.Info("Validating PerformanceProfiles from: ", r.inDir)

	// Read asset directory fileInfo
	filePaths, err := util.ListFiles(r.inDir)
	klog.V(4).Infof("listed files: %v", filePaths)
	if err != nil {
		return err
	}

	var results []*result

	klog.Info("Iterating over listed files ... ")
	for _, path := range filePaths {
		retval := &result{
			filename: path,
		}
		results = append(results, retval)

		func (){
			file, err := os.Open(path)
			if err != nil {
				klog.Infof("error opening %s ... ", file.Name())
				retval.problems = append(retval.problems, fmt.Errorf("error opening %s: %w", file.Name(), err))
				return
			}
			defer file.Close()

			manifests, err := util.ParseManifests(file.Name(), file)
			if err != nil {
				klog.Infof("error parsing %s ... ", file.Name())
				retval.problems = append(retval.problems, fmt.Errorf("error parsing manifests from %s: %w", file.Name(), err))
				return
			}

			// Decode manifest files
			klog.V(4).Infof("decoding manifests for file %s...", path)
			for idx, m := range manifests {
				obji, err := runtime.Decode(runtimeDecoder, m.Raw)
				if err != nil {
					klog.Infof("error decoding %s ... ", file.Name())
					if runtime.IsNotRegisteredError(err) {
						retval.problems = append(retval.problems, fmt.Errorf("skipping path %q [%d] manifest because it is not part of expected api group: %w", file.Name(), idx+1, err))
						klog.V(4).Infof("skipping path %q [%d] manifest because it is not part of expected api group: %v", file.Name(), idx+1, err)
						continue
					}
					retval.problems = append(retval.problems, fmt.Errorf("error parsing %q [%d] manifest: %w", file.Name(), idx+1, err))
					continue
				}

				switch obj := obji.(type) {
				case *performanceprofilev2.PerformanceProfile:
					klog.Infof("PP decoded from file %s ... ", file.Name())
					retval.perfProfiles = obj
					errList := obj.ValidateBasicFields()
					if len(errList) > 0 {
						retval.problems = append(retval.problems, errList.ToAggregate().Errors()...)
					}
				default:
					klog.Infof("skipping %q [%d] manifest because of unhandled %T\n", file.Name(), idx+1, obji)
					retval.skipped = true
				}
			}
		}()
	}

	var ret error = nil
	for _, r := range results {
		klog.Infof("** Filename: %q", r.filename)
		if r.skipped {
			klog.Infof("\tfile %s skipped ", r.filename)
			continue
		}
		if r.perfProfiles != nil {
			klog.Infof("\tPP name: %q", r.perfProfiles.Name)
		}
		if len(r.problems) == 0 {
			klog.Infof("\tValidation: OK")
		} else {
			ret = fmt.Errorf("Found errors while validating PerformanceProfiles")

			klog.Infof("\tValidation: NOK")
			klog.Infof("\tProblems:")
			for _, e := range r.problems {
				klog.Infof("\t\t- %v", e)
			}
		}
	}
	return ret
}

func addKlogFlags(cmd *cobra.Command) {
	fs := flag.NewFlagSet("", flag.PanicOnError)
	klog.InitFlags(fs)
	cmd.Flags().AddGoFlagSet(fs)
}
