package util

// Checks for white-space characters in "C" and "POSIX" locales.
func isspace(b byte) bool {
	return b == ' ' || b == '\f' || b == '\n' || b == '\r' || b == '\t' || b == '\v'
}

// You can use " around spaces, but can't escape ".  See next_arg() in kernel code.
func nextArg(args string, start int) (int, int) {
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

	return start, stop
}

// SplitKernelArguments splits kernel parameters into a slice of strings one parameter per string.
// Workaround for rhbz#1812605.
func SplitKernelArguments(args string) []string {
	var (
		start, stop int
		kv          []string
	)

	for stop < len(args) {
		start, stop = nextArg(args, stop)
		if start != stop {
			kv = append(kv, args[start:stop])
		}
	}

	return kv
}
