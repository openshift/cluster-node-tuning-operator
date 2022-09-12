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

// MapOfStringsContains returns true if map of strings a contains all
// entries of the map of strings b. Use MapOfStringsEqual for checking
// if two maps of strings are equal.
// For example
// MapOfStringsContains(map[string]string{"a": "a","b": "b"}, map[string]string{"a": "a"})
// will return true, but
// MapOfStringsContains(map[string]string{"a": "a"}, map[string]string{"a": "a","b": "b"})
// will return false.
func MapOfStringsContains(a, b map[string]string) bool {
	for k, v := range b {
		if w, ok := a[k]; !ok || v != w {
			return false
		}
	}
	return true
}
