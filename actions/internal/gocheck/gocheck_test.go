package gocheck

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/test-studio/conformance/junit"
)

// writeModule lays a throwaway Go module out in a temporary directory and returns it.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	all := map[string]string{"go.mod": "module example.invalid/fx\n\ngo 1.21\n"}
	for name, body := range files {
		all[name] = body
	}
	for name, body := range all {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const (
	okPackage      = "package ok\n\n// Add returns a+b.\nfunc Add(a, b int) int { return a + b }\n"
	brokenPackage  = "package bad\n\n// X does not compile.\nvar X int = \"not an int\"\n"
	vettablePkg    = "package noisy\n\nimport \"fmt\"\n\n// F has a format verb that does not match its argument.\nfunc F() string { return fmt.Sprintf(\"%d\", \"x\") }\n"
	cleanSecondPkg = "package second\n\n// Two returns 2.\nfunc Two() int { return 2 }\n"
)

// readSuite reads the JUnit report a run left in dir.
func readSuite(t *testing.T, dir, name string) junit.Suite {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("the run left no JUnit report: %v", err)
	}
	var s junit.Suite
	if err := xml.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the JUnit report does not parse: %v", err)
	}
	return s
}

// outcomes maps each case name to true when it failed.
func outcomes(s junit.Suite) map[string]bool {
	m := map[string]bool{}
	for _, c := range s.Cases {
		m[c.Name] = c.Failure != nil || c.Error != nil
	}
	return m
}

func TestCleanModuleEveryPackagePasses(t *testing.T) {
	t.Parallel()
	for _, k := range []Kind{Build, Vet} {
		t.Run(k.ID, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, map[string]string{"ok/ok.go": okPackage, "second/second.go": cleanSecondPkg})
			var stderr bytes.Buffer
			if code := Run(k, dir, nil, &stderr); code != ExitPass {
				t.Fatalf("exit %d on a clean module; stderr: %s", code, stderr.String())
			}
			got := outcomes(readSuite(t, dir, k.ReportName()))
			want := map[string]bool{"example.invalid/fx/ok": false, "example.invalid/fx/second": false}
			if len(got) != len(want) || got["example.invalid/fx/ok"] || got["example.invalid/fx/second"] {
				t.Fatalf("cases = %v, want every package passed: %v", got, want)
			}
		})
	}
}

func TestBuildFailsExactlyThePackageThatDoesNotCompile(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, map[string]string{"ok/ok.go": okPackage, "bad/bad.go": brokenPackage})
	var stderr bytes.Buffer
	if code := Run(Build, dir, nil, &stderr); code != ExitFail {
		t.Fatalf("exit %d, want 1; stderr: %s", code, stderr.String())
	}
	s := readSuite(t, dir, "go-build.junit.xml")
	got := outcomes(s)
	if len(got) != 2 || !got["example.invalid/fx/bad"] || got["example.invalid/fx/ok"] {
		t.Fatalf("cases = %v, want only example.invalid/fx/bad failed", got)
	}
	if s.Failures != 1 || s.Tests != 2 {
		t.Fatalf("suite counts tests=%d failures=%d, want 2 and 1", s.Tests, s.Failures)
	}
	for _, c := range s.Cases {
		if c.Name == "example.invalid/fx/bad" && !strings.Contains(c.Failure.Text, "not an int") {
			t.Fatalf("the failed case does not carry the compiler output: %q", c.Failure.Text)
		}
	}
}

func TestVetFailsExactlyThePackageWithAFindingAndBuildStillPasses(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, map[string]string{"ok/ok.go": okPackage, "noisy/noisy.go": vettablePkg})
	var stderr bytes.Buffer
	if code := Run(Vet, dir, nil, &stderr); code != ExitFail {
		t.Fatalf("vet exit %d, want 1; stderr: %s", code, stderr.String())
	}
	s := readSuite(t, dir, "go-vet.junit.xml")
	got := outcomes(s)
	if len(got) != 2 || !got["example.invalid/fx/noisy"] || got["example.invalid/fx/ok"] {
		t.Fatalf("cases = %v, want only example.invalid/fx/noisy failed", got)
	}
	for _, c := range s.Cases {
		if c.Name == "example.invalid/fx/noisy" && !strings.Contains(c.Failure.Text, "Sprintf format %d") {
			t.Fatalf("the failed case does not carry the vet output: %q", c.Failure.Text)
		}
	}
	if code := Run(Build, dir, nil, &stderr); code != ExitPass {
		t.Fatalf("a vet finding must not fail the build check; exit %d, stderr: %s", code, stderr.String())
	}
}

func TestPatternsSelectThePackagesChecked(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, map[string]string{"ok/ok.go": okPackage, "bad/bad.go": brokenPackage})
	if code := Run(Build, dir, []string{"./ok"}, &bytes.Buffer{}); code != ExitPass {
		t.Fatalf("exit %d, want 0 when only the healthy package is selected", code)
	}
	got := outcomes(readSuite(t, dir, "go-build.junit.xml"))
	if len(got) != 1 {
		t.Fatalf("cases = %v, want exactly the one selected package", got)
	}
}

func TestRunnerThatCannotRunExitsTwoNamesWhyAndStillLeavesAReport(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		files    map[string]string
		patterns []string
		wantErr  string
	}{
		{"flag as pattern", map[string]string{"ok/ok.go": okPackage}, []string{"-x"}, "pattern"},
		{"no package matches", map[string]string{"README": "x\n"}, nil, "no package"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, tc.files)
			var stderr bytes.Buffer
			if code := Run(Build, dir, tc.patterns, &stderr); code != ExitRunner {
				t.Fatalf("exit %d, want 2; stderr: %s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.wantErr) || !strings.HasPrefix(stderr.String(), "go-build: ") {
				t.Fatalf("stderr %q does not name the reason %q", stderr.String(), tc.wantErr)
			}
			s := readSuite(t, dir, "go-build.junit.xml")
			if s.Errors != 1 || len(s.Cases) != 1 || s.Cases[0].Error == nil {
				t.Fatalf("a runner that cannot run must leave one error case, got %+v", s)
			}
		})
	}
}

func TestOneBrokenPackageReportsOneFailingCase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		kind       Kind
		files      map[string]string
		patterns   []string
		wantFailed string // the one case expected to fail
		wantTests  int
	}{
		{
			name: "import-missing",
			kind: Build,
			files: map[string]string{
				"ok/ok.go": okPackage,
				"bad/bad.go": `package bad

import "example.invalid/nosuch"

var X = 1
`,
			},
			wantFailed: "example.invalid/fx/bad",
			wantTests:  2,
		},
		{
			name: "nosuch-pattern",
			kind: Build,
			files: map[string]string{
				"ok/ok.go": okPackage,
				"bad/bad.go": `package bad

var X int = "not an int"
`,
			},
			patterns:   []string{"./nonexistent"},
			wantFailed: "./nonexistent",
			wantTests:  1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, tc.files)
			var stderr bytes.Buffer
			if code := Run(tc.kind, dir, tc.patterns, &stderr); code != ExitFail {
				t.Fatalf("exit %d, want 1; stderr: %s", code, stderr.String())
			}
			s := readSuite(t, dir, tc.kind.ReportName())
			var failed []string
			for _, c := range s.Cases {
				if c.Failure != nil {
					failed = append(failed, c.Name)
				}
			}
			if len(failed) != 1 || failed[0] != tc.wantFailed {
				t.Fatalf("failed cases = %v, want exactly one: %s", failed, tc.wantFailed)
			}
			if s.Failures != 1 || s.Tests != tc.wantTests {
				t.Fatalf("suite counts tests=%d failures=%d, want %d and 1", s.Tests, s.Failures, tc.wantTests)
			}
		})
	}
}

func TestBuiltBinariesExitWithTheContractCodes(t *testing.T) {
	t.Parallel()
	broken := writeModule(t, map[string]string{"ok/ok.go": okPackage, "bad/bad.go": brokenPackage})
	for _, id := range []string{"go-build", "go-vet"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			bin := filepath.Join(t.TempDir(), id)
			build := exec.Command("go", "build", "-o", bin, "../../"+id)
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("cannot build the runner: %v\n%s", err, out)
			}
			dir := t.TempDir()
			copyTree(t, broken, dir)
			cmd := exec.Command(bin)
			cmd.Dir = dir
			err := cmd.Run()
			var exit *exec.ExitError
			if id == "go-build" {
				if !errors.As(err, &exit) || exit.ExitCode() != ExitFail {
					t.Fatalf("go-build on a broken package: err = %v, want exit 1", err)
				}
			} else if err != nil {
				// the broken package also fails vet: a type error is reported by vet too
				if !errors.As(err, &exit) || exit.ExitCode() != ExitFail {
					t.Fatalf("go-vet on a broken package: err = %v, want exit 1", err)
				}
			} else {
				t.Fatal("go-vet on a package that does not compile must not exit 0")
			}
			if _, err := os.Stat(filepath.Join(dir, id+".junit.xml")); err != nil {
				t.Fatalf("the binary left no report: %v", err)
			}
		})
	}
}

func TestTheRecordedFixturesGiveTheOutcomeTheirRecordsExpect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind    Kind
		fixture string
		failed  string
	}{
		{Build, "go-compile-error", "example.invalid/gocompileerror/bad"},
		{Vet, "go-vet-finding", "example.invalid/govetfinding/noisy"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			copyTree(t, filepath.Join("..", "..", "..", "conformance", "testdata", "tool-fixtures", tc.fixture), dir)
			if code := Run(tc.kind, dir, nil, &bytes.Buffer{}); code != ExitFail {
				t.Fatalf("exit %d, want 1: the record expects findings", code)
			}
			got := outcomes(readSuite(t, dir, tc.kind.ReportName()))
			if len(got) != 2 || !got[tc.failed] {
				t.Fatalf("cases = %v, want exactly %s failed", got, tc.failed)
			}
		})
	}
}

// copyTree copies the regular files of src into dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}
