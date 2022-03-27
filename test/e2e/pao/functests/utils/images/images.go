package images

import (
	"fmt"
	"os"
)

var registry string
var cnfTestsImage string

func init() {
	registry = os.Getenv("IMAGE_REGISTRY")
	cnfTestsImage = os.Getenv("CNF_TESTS_IMAGE")

	if cnfTestsImage == "" {
		cnfTestsImage = "cnf-tests:4.9"
	}

	if registry == "" {
		registry = "quay.io/openshift-kni/"
	}
}

// Test returns the image to be used for tests
func Test() string {
	return fmt.Sprintf("%s%s", registry, cnfTestsImage)
}
