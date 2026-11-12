// Package bad is a fixture package that does not compile, on purpose.
package bad

// X is an int initialised with a string: a compile error.
var X int = "not an int"
