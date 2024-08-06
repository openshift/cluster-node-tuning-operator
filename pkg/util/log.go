package util

import (
	"flag"
	"fmt"

	"k8s.io/klog/v2"
)

// SetLogLevel was lifted with minor modifications from library-go.  In their own words,
// it is a nasty hack and attempt to manipulate the global flags as klog does not expose
// a way to dynamically change the loglevel in runtime.
func SetLogLevel(targetLevel int) error {
	var level *klog.Level

	verbosity := fmt.Sprintf("%d", targetLevel)

	// First, if the '-v' was specified in command line, attempt to acquire the level pointer from it.
	if f := flag.CommandLine.Lookup("v"); f != nil {
		if flagValue, ok := f.Value.(*klog.Level); ok {
			level = flagValue
		}
	}

	// Second, if the '-v' was not set but is still present in flags defined for the command, attempt to acquire it
	// by visiting all flags.
	if level == nil {
		flag.VisitAll(func(f *flag.Flag) {
			if level != nil {
				return
			}
			if levelFlag, ok := f.Value.(*klog.Level); ok {
				level = levelFlag
			}
		})
	}

	if level != nil {
		return level.Set(verbosity)
	}

	// Third, if modifying the flag value (which is recommended by klog) fails, then fallback to modifying
	// the internal state of klog using the empty new level.
	var newLevel klog.Level
	if err := newLevel.Set(verbosity); err != nil {
		return fmt.Errorf("failed set klog.logging.verbosity %s: %v", verbosity, err)
	}

	return nil
}
