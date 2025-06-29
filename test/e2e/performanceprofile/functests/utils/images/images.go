package images

import (
	"fmt"
	"os"
)

var registry string
var cnfTestsImage string

func init() {
	var ok bool
	registry, ok = os.LookupEnv("IMAGE_REGISTRY")
	if !ok {
		registry = "quay.io/openshift-kni/"
	}

	cnfTestsImage, ok = os.LookupEnv("CNF_TESTS_IMAGE")
	if !ok {
		cnfTestsImage = "cnf-tests:4.14"
	}
}

// Test returns the image to be used for tests
func Test() string {
	return fmt.Sprintf("%s%s", registry, cnfTestsImage)
}
