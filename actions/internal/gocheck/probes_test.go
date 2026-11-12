package gocheck

import (
	"bytes"
	"strings"
	"testing"
)

// TestLoadErrorDoesNotMaskABrokenPackage reproduces B1: a module whose a/a.go imports
// a missing module and whose b/b.go does not compile. `go build ./...` stops at the
// load error and never compiles b, so the runner must not report b as pass. Both
// packages fail, one case each, named by import path, with no phantom case.
func TestLoadErrorDoesNotMaskABrokenPackage(t *testing.T) {
	t.Parallel()
	for _, k := range []Kind{Build, Vet} {
		t.Run(k.ID, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, map[string]string{
				"a/a.go": `package a

import "example.invalid/nosuch"

`,
				"b/b.go": `package b

var X int = "s"

`,
			})
			var stderr bytes.Buffer
			if code := Run(k, dir, nil, &stderr); code != ExitFail {
				t.Fatalf("exit %d on two broken packages, want 1; stderr: %s", code, stderr.String())
			}
			s := readSuite(t, dir, k.ReportName())
			got := outcomes(s)
			want := map[string]bool{"example.invalid/fx/a": true, "example.invalid/fx/b": true}
			if len(got) != len(want) {
				t.Fatalf("cases = %v, want exactly the two packages, both failed: %v", got, want)
			}
			for name, failed := range want {
				if got[name] != failed {
					t.Fatalf("case %q failed = %v, want %v", name, got[name], failed)
				}
			}
			if s.Failures != 2 || s.Tests != 2 {
				t.Fatalf("suite counts tests=%d failures=%d, want 2 and 2", s.Tests, s.Failures)
			}
			for _, c := range s.Cases {
				if strings.HasPrefix(c.Name, "[") || c.Name == unattributedCase {
					t.Fatalf("a phantom case was reported: %q", c.Name)
				}
			}
		})
	}
}

// TestVetTestFileCompileErrorNamesOneCaseByPackage reproduces B2: a _test.go that does
// not compile under go-vet. go vet prints "# <path>", "# [<path>]" and the error; the
// runner must file the error under the package's import path: exactly one failing case,
// named by the package, no phantom `[<path>]` case.
func TestVetTestFileCompileErrorNamesOneCaseByPackage(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, map[string]string{
		"t/t.go": `package t

func Add(a, b int) int { return a + b }

`,
		"t/t_test.go": `package t

var X int = "s"

`,
	})
	var stderr bytes.Buffer
	if code := Run(Vet, dir, nil, &stderr); code != ExitFail {
		t.Fatalf("vet exit %d on a broken test file, want 1; stderr: %s", code, stderr.String())
	}
	s := readSuite(t, dir, "go-vet.junit.xml")
	got := outcomes(s)
	if len(got) != 1 || !got["example.invalid/fx/t"] {
		t.Fatalf("cases = %v, want exactly one failing case: example.invalid/fx/t", got)
	}
	if s.Failures != 1 || s.Tests != 1 {
		t.Fatalf("suite counts tests=%d failures=%d, want 1 and 1", s.Tests, s.Failures)
	}
}
