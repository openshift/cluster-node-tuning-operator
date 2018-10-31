package main

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"time"

	controller "github.com/openshift/cluster-node-tuning-operator/pkg/controller"
	sdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	k8sutil "github.com/operator-framework/operator-sdk/pkg/util/k8sutil"
	sdkVersion "github.com/operator-framework/operator-sdk/version"

	"github.com/sirupsen/logrus"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

const (
	resyncPeriodDefault int64 = 60
)

func printVersion() {
	logrus.Infof("Go Version: %s", runtime.Version())
	logrus.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
	logrus.Infof("operator-sdk Version: %v", sdkVersion.Version)
}

func main() {
	var resyncPeriodDuration int64 = resyncPeriodDefault
	printVersion()

	sdk.ExposeMetricsPort()

	resource := "tuned.openshift.io/v1alpha1"
	kind := "Tuned"
	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		logrus.Fatalf("failed to get watch namespace: %v", err)
	}
	if os.Getenv("RESYNC_PERIOD") != "" {
		resyncPeriodDuration, err = strconv.ParseInt(os.Getenv("RESYNC_PERIOD"), 10, 64)
		if err != nil {
			logrus.Errorf("Cannot parse RESYNC_PERIOD (%s), using %d", os.Getenv("RESYNC_PERIOD"), resyncPeriodDefault)
			resyncPeriodDuration = resyncPeriodDefault
		}
	}
	resyncPeriod := time.Duration(resyncPeriodDuration) * time.Second
	logrus.Infof("Watching %s, %s, %s, %d", resource, kind, namespace, resyncPeriod)
	sdk.Watch(resource, kind, namespace, resyncPeriod)
	sdk.Handle(controller.NewHandler())
	sdk.Run(context.TODO())
}
