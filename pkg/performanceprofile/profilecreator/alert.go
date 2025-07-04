package profilecreator

import (
	"log"
	"os"
)

var alertSink *log.Logger

func init() {
	// intentionally no flag for minimal output
	alertSink = log.New(os.Stderr, "PPC: ", 0)
}

// Alert raise status messages. `profilecreator` is meant to run interactively,
// so the default behavior is to bubble up the messages on the user console.
// It is however possible to reroute the messages to any log.Logger compliant backend
// see the function SetAlertSink. All Alerts are considered Info messages.
func Alert(format string, values ...any) {
	alertSink.Printf(format, values...)
}

// Call this function before any other else in the `profilecreator` package
func SetAlertSink(lh *log.Logger) {
	alertSink = lh
}

func GetAlertSink() *log.Logger {
	return alertSink
}
