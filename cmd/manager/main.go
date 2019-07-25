package main

import (
	"context"
	"flag"
	"os"
	"runtime"

	"github.com/golang/glog"
	"github.com/openshift/cluster-node-tuning-operator/pkg/apis"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/pkg/controller"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util/leader"
	"github.com/openshift/cluster-node-tuning-operator/version"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
)

var (
	// Flags
	boolVersion = flag.Bool("version", false, "show program version and exit")
)

func printVersion() {
	glog.Infof("Go Version: %s", runtime.Version())
	glog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	glog.Infof("%s Version: %s", ntoconfig.OperatorName(), version.Version)
}

func main() {
	logsCoexist()

	printVersion()

	if *boolVersion {
		os.Exit(0)
	}

	operatorNamespace := os.Getenv("WATCH_NAMESPACE")
	if len(operatorNamespace) == 0 {
		glog.Fatalf("WATCH_NAMESPACE environment variable missing")
	}
	glog.Infof("Operator namespace: %s", operatorNamespace)

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		glog.Fatal(err)
	}

	ctx := context.TODO()

	// Become the leader before proceeding
	err = leader.Become(ctx, operatorNamespace, "node-tuning-operator-lock")
	if err != nil {
		glog.Fatal(err)
	}

	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := manager.New(cfg, manager.Options{Namespace: operatorNamespace})
	if err != nil {
		glog.Fatal(err)
	}

	glog.V(1).Infof("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		glog.Fatal(err)
	}

	// Setup all Controllers
	if err := controller.AddToManager(mgr); err != nil {
		glog.Fatal(err)
	}

	glog.Infof("Starting the Cmd.")

	// Start the Cmd
	glog.Fatal(mgr.Start(signals.SetupSignalHandler()))
}

func logsCoexist() {
	flag.Set("logtostderr", "true")
	flag.Parse()

	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)

	// Sync the glog and klog flags.
	flag.CommandLine.VisitAll(func(f1 *flag.Flag) {
		f2 := klogFlags.Lookup(f1.Name)
		if f2 != nil {
			value := f1.Value.String()
			f2.Value.Set(value)
		}
	})
}
