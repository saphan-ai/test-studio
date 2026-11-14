// Command go-build is the catalogue runner of the go-build action: it compiles the Go packages
// its arguments select and writes go-build.junit.xml in the work directory, one case per package.
// Exit 0: every package built. Exit 1: a package did not. Exit 2: the runner could not run.
package main

import (
	"os"

	"github.com/saphan-ai/test-studio/actions/internal/gocheck"
)

func main() { os.Exit(gocheck.Main(gocheck.Build, os.Args[1:], os.Stderr)) }
