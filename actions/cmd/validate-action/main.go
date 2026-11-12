// Command validate-action checks catalogue action.yml files against the contract in
// spec/TOOL_ACTION_V1.md. Each argument is the path of an action.yml; the directory that holds it
// is the action's id. Exit 0: every file is admitted. Exit 1: a file is refused, and every refusal
// is printed on stdout, one per line, naming the file, the code and the key. Exit 2: usage, or a
// file that cannot be read.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/saphan-ai/test-studio/actions/actionspec"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: validate-action <path/to/action.yml>...")
		return 2
	}
	code := 0
	for _, path := range args {
		refusals, err := actionspec.Validate(path)
		if err != nil {
			fmt.Fprintf(stderr, "validate-action: cannot validate %s: %v\n", path, err)
			return 2
		}
		for _, r := range refusals {
			fmt.Fprintln(stdout, r)
			code = 1
		}
		if len(refusals) == 0 {
			fmt.Fprintf(stdout, "%s: admitted\n", path)
		}
	}
	return code
}
