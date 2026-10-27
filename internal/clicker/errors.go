package clicker

import "fmt"

// clickerErr builds a service error in the "clicker: CODE: message" shape the
// page maps on. msg is already user-facing.
func clickerErr(code, msg string) error {
	return fmt.Errorf("clicker: %s: %s", code, msg)
}

// clickerErrf is clickerErr with formatting.
func clickerErrf(code, format string, args ...any) error {
	return clickerErr(code, fmt.Sprintf(format, args...))
}
