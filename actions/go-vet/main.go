// Command go-vet is the catalogue runner of the go-vet action: it vets the Go packages its
// arguments select and writes go-vet.junit.xml in the work directory, one case per package.
// Exit 0: no package has a finding. Exit 1: a package has one. Exit 2: the runner could not run.
package main

import (
	"os"

	"github.com/saphan-ai/test-studio/actions/internal/gocheck"
)

func main() { os.Exit(gocheck.Main(gocheck.Vet, os.Args[1:], os.Stderr)) }
