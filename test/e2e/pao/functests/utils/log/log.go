package log

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
)

func nowStamp() string {
	return time.Now().Format(time.StampMilli)
}

func logf(level string, format string, args ...interface{}) {
	fmt.Fprintf(ginkgo.GinkgoWriter, nowStamp()+": "+level+": "+format+"\n", args...)
}

func log(level string, args ...interface{}) {
	fmt.Fprint(ginkgo.GinkgoWriter, nowStamp()+": "+level+": ")
	fmt.Fprint(ginkgo.GinkgoWriter, args...)
	fmt.Fprint(ginkgo.GinkgoWriter, "\n")
}

// Info logs the info
func Info(args ...interface{}) {
	log("[INFO]", args...)
}

// Infof logs the info with arguments
func Infof(format string, args ...interface{}) {
	logf("[INFO]", format, args...)
}

// Warning logs the warning
func Warning(args ...interface{}) {
	log("[WARNING]", args...)
}

// Warningf logs the warning with arguments
func Warningf(format string, args ...interface{}) {
	logf("[WARNING]", format, args...)
}

// Error logs the warning
func Error(args ...interface{}) {
	log("[ERROR]", args...)
}

// Errorf logs the warning with arguments
func Errorf(format string, args ...interface{}) {
	logf("[ERROR]", format, args...)
}
