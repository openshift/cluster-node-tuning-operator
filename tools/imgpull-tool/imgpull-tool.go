package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/onsi/ginkgo"

	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/images"
)

var (
	namespace = flag.String("namespace", "default", "pnamespace to use for helper daemonset")
)

func main() {
	flag.Parse()
	if namespace == nil || *namespace == "" {
		log.Fatal("missing namespace for the helper daemonset")
	}

	if !testclient.ClientsEnabled {
		os.Exit(1)
	}

	// ugly hack to get logs from utils
	ginkgo.GinkgoWriter = os.Stderr

	ds, err := images.PrePull(testclient.Client, images.Test(), *namespace, "imgpull-tool-cnf-tests")
	if err != nil {
		log.Printf("DaemonSet %q %q for %q status: %v", ds.Namespace, ds.Name, images.Test(), ds.Status)
		log.Fatal(fmt.Sprintf("prepull image %q failed: %v", images.Test(), err))
	}
}
