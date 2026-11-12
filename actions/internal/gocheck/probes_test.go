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
