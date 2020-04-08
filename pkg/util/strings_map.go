package util

// MapOfStringsCopy returns a copy of a map of strings 'a'.
func MapOfStringsCopy(a map[string]string) map[string]string {
	b := map[string]string{}

	for k, v := range a {
		b[k] = v
	}

	return b
}

// MapOfStringsEqual returns true if maps of strings 'a' and 'b' are equal.
// reflect.DeepEqual is roughly 10x slower than this.
func MapOfStringsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}

	return true
}
