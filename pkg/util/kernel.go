package util

// Checks for white-space characters in "C" and "POSIX" locales.
func isspace(b byte) bool {
	return b == ' ' || b == '\f' || b == '\n' || b == '\r' || b == '\t' || b == '\v'
}

// You can use " around spaces, but can't escape ".  See next_arg() in kernel code.
func nextArg(args string, start int) (int, int, int) {
	var (
		i, equals int
		stop      int
		inQuote   bool
	)

	// Skip leading spaces
	for start < len(args) && isspace(args[start]) {
		start++
	}

	i = start

	if start < len(args) && args[i] == '"' {
		i++
		inQuote = true
	}

	for ; i < len(args); i++ {
		if isspace(args[i]) && !inQuote {
			break
		}

		if equals == 0 && args[i] == '=' {
			equals = i
		}

		if args[i] == '"' {
			inQuote = !inQuote
		}
	}

	stop = i

	return start, equals, stop
}

// SplitKernelArguments splits kernel parameters into a slice of strings one parameter per string.
// Workaround for rhbz#1812605.
func SplitKernelArguments(args string) []string {
	return SplitKernelArgumentsWithout(args, nil)
}

// SplitKernelArgumentsWithout splits kernel parameters excluding any parameters in the 'exclude'
// slice into a slice of strings one parameter per string.
func SplitKernelArgumentsWithout(args string, exclude []string) []string {
	var (
		start, equals, stop int
		kv                  []string
	)

loop:
	for stop < len(args) {
		start, equals, stop = nextArg(args, stop)
		if len(exclude) != 0 {
			// Some parameters are being excluded.
			var key string

			if equals == 0 {
				key = args[start:stop]
			} else {
				key = args[start:equals]
			}
			for _, v := range exclude {
				if key == v {
					continue loop
				}
			}
		}
		if start != stop {
			kv = append(kv, args[start:stop])
		}
	}

	return kv
}

// KernelArgumentsEqual compares kernel arguments 'args1' and 'args2'
// excluding parameters 'exclude'.  Returns true when they're equal.
// Note the order of kernel arguments matters.  For example,
// "hugepagesz=1G hugepages=1" vs. "hugepages=1 hugepagesz=1G".
func KernelArgumentsEqual(args1, args2 string, exclude ...string) bool {
	a1 := SplitKernelArgumentsWithout(args1, exclude)
	a2 := SplitKernelArgumentsWithout(args2, exclude)

	return StringSlicesEqual(a1, a2)
}
