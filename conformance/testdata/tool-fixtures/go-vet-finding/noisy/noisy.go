// Package noisy is a fixture package that builds and has one vet finding, on purpose.
package noisy

import "fmt"

// F formats a string with a verb that does not match its argument.
func F() string { return fmt.Sprintf("%d", "x") }
