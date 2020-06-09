package util

// StringSlicesAsSetsEqual returns true if slices of
// strings 'a' and 'b' with their items sorted equally
// with duplicate items removed are the same.
func StringSlicesAsSetsEqual(a, b []string) bool {
	mA := map[string]bool{}
	mB := map[string]bool{}

	// Do not check length of the slices here, there may be multiple equal items in one of the arrays.
	// Such slices can still be "equal" when treated as sets.

	for _, v := range a {
		mA[v] = true
	}

	for _, v := range b {
		mB[v] = true
	}

	if len(mA) != len(mB) {
		return false
	}

	for _, v := range a {
		if !mB[v] {
			return false
		}
	}

	return true
}

// StringSlicesEqual returns true if slices of strings 'a' and 'b' are the same.
func StringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i, v := range a {
		if v != b[i] {
			return false
		}
	}

	return true
}
