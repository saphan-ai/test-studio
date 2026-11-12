package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExitCodesFollowTheVerdict(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "go-build")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "action.yml")
	if err := os.WriteFile(bad, []byte("name: go-build\ndescription: d\nbranding:\n  color: blue\nruns:\n  using: node20\n  main: x.js\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
	}{
		{"admitted", []string{filepath.Join("..", "..", "go-build", "action.yml")}, 0, "admitted"},
		{"refused", []string{bad}, 1, "unknown-top-level-key at branding"},
		{"unreadable", []string{filepath.Join(dir, "missing.yml")}, 2, ""},
		{"usage", nil, 2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errw bytes.Buffer
			if got := run(tc.args, &out, &errw); got != tc.wantCode {
				t.Fatalf("exit %d, want %d; stdout %q stderr %q", got, tc.wantCode, out.String(), errw.String())
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Fatalf("stdout %q does not hold %q", out.String(), tc.wantOut)
			}
		})
	}
}
