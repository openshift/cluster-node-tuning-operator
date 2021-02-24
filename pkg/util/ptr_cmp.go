package util

// PtrBoolEqual returns true if the booleans pointed to by
// 'a' and 'b' are equal or both 'a' and 'b' are nil.
func PtrBoolEqual(a, b *bool) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
