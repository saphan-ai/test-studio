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
	all := map[string]string{"go.mod": "module example.invalid/fx\n\ngo 1.26\n"}
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

// assertOneCasePerSubject asserts the report holds one case per subject package, that every case
// is named by go list's ImportPath (no header test-variant forms leak into case names) and that
// the counts match the number of subject packages. A directory whose path ends in _test is a
// real subject and may be a case under its own path: _test is never stripped from a name.
func assertOneCasePerSubject(t *testing.T, s junit.Suite, subjects int) {
	t.Helper()
	if len(s.Cases) != subjects {
		t.Fatalf("cases = %v, want one case per subject package (%d)", caseNames(s), subjects)
	}
	for _, c := range s.Cases {
		if strings.Contains(c.Name, "[") || c.Name == "(output not attributed to a package)" {
			t.Fatalf("a header-derived or phantom case was reported: %q", c.Name)
		}
	}
}

func caseNames(s junit.Suite) []string {
	names := make([]string, 0, len(s.Cases))
	for _, c := range s.Cases {
		names = append(names, c.Name)
	}
	return names
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
	assertOneCasePerSubject(t, s, 2)
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
	assertOneCasePerSubject(t, s, 2)
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
			assertOneCasePerSubject(t, s, 2)
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
		})
	}
}

// TestVetTestFileCompileErrorNamesOneCaseByPackage reproduces B2: a _test.go that does
// not compile under go-vet. go vet prints "# <path>" and "# [<path>]" headers for the
// test variant; the runner must file the error under the package's import path: exactly
// one failing case, named by the package, no phantom [<path>] case.
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
	assertOneCasePerSubject(t, s, 1)
	if len(got) != 1 || !got["example.invalid/fx/t"] {
		t.Fatalf("cases = %v, want exactly one failing case: example.invalid/fx/t", got)
	}
	if s.Failures != 1 || s.Tests != 1 {
		t.Fatalf("suite counts tests=%d failures=%d, want 1 and 1", s.Tests, s.Failures)
	}
}

// TestDependencySkippedPackageIsNotPassed reproduces N2: good imports bad, which does not
// compile. The go command stops at bad and never checks good, so good must be a failing case
// named example.invalid/fx/good, never pass, under both runners. The failure text says why:
// the dependency failed and the package was not reached.
func TestDependencySkippedPackageIsNotPassed(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"bad/bad.go": `package bad

var X int = "s"

`,
		"good/good.go": `package good

import "example.invalid/fx/bad"

var G = 1

`,
	}
	for _, k := range []Kind{Build, Vet} {
		t.Run(k.ID, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, files)
			var stderr bytes.Buffer
			if code := Run(k, dir, nil, &stderr); code != ExitFail {
				t.Fatalf("%s exit %d on a package importing a broken one, want 1; stderr: %s", k.ID, code, stderr.String())
			}
			s := readSuite(t, dir, k.ReportName())
			got := outcomes(s)
			assertOneCasePerSubject(t, s, 2)
			want := map[string]bool{"example.invalid/fx/bad": true, "example.invalid/fx/good": true}
			if len(got) != len(want) {
				t.Fatalf("cases = %v, want both packages failed: %v", got, want)
			}
			for name, failed := range want {
				if got[name] != failed {
					t.Fatalf("case %q failed = %v, want %v", name, got[name], failed)
				}
			}
			if s.Failures != 2 || s.Tests != 2 {
				t.Fatalf("suite counts tests=%d failures=%d, want 2 and 2", s.Tests, s.Failures)
			}
			good := caseByName(s, "example.invalid/fx/good")
			if good == nil || good.Failure == nil {
				t.Fatalf("example.invalid/fx/good is not a failing case: %+v", good)
			}
			if !strings.Contains(good.Failure.Text, "bad") && !strings.Contains(good.Failure.Text, "not checked") {
				t.Fatalf("the good case's failure text does not say the dependency failed: %q", good.Failure.Text)
			}
		})
	}
}

// TestExternalTestCompileErrorFailsTheOwningPackage reproduces N1 as a true external test: x
// ships x.go and x_ext_test.go (package x_test) in the same directory, and the external test
// does not compile. go list lists x alone, so the case is example.invalid/fx/x and no x_test
// case ever exists; go vet compiles the test variant and fails under that one case, while go
// build ignores test files and passes x.
func TestExternalTestCompileErrorFailsTheOwningPackage(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, map[string]string{
		"x/x.go": `package x

func Add(a, b int) int { return a + b }

`,
		"x/x_ext_test.go": `package x_test

var X int = "s"

`,
	})
	if code := Run(Build, dir, nil, &bytes.Buffer{}); code != ExitPass {
		t.Fatalf("build exit %d on a package whose external test does not compile; build does not compile test files", code)
	}
	got := outcomes(readSuite(t, dir, Build.ReportName()))
	if len(got) != 1 || got["example.invalid/fx/x"] {
		t.Fatalf("cases = %v, want exactly one passing case: example.invalid/fx/x", got)
	}
	var stderr bytes.Buffer
	if code := Run(Vet, dir, nil, &stderr); code != ExitFail {
		t.Fatalf("vet exit %d on an uncompileable external test, want 1; stderr: %s", code, stderr.String())
	}
	s := readSuite(t, dir, Vet.ReportName())
	got = outcomes(s)
	assertOneCasePerSubject(t, s, 1)
	if len(got) != 1 || !got["example.invalid/fx/x"] {
		t.Fatalf("cases = %v, want exactly one failing case: example.invalid/fx/x", got)
	}
	if s.Failures != 1 || s.Tests != 1 {
		t.Fatalf("suite counts tests=%d failures=%d, want 1 and 1", s.Tests, s.Failures)
	}
}

// TestPackageWithOnlyBuildConstrainedFileFailsByImportPath: a package whose only file is
// excluded by a build constraint still yields one failing case named by its import path.
// go list lists the file, so the package is a subject; the go command reports the
// misplaced directive under its path and the runner fails it.
func TestPackageWithOnlyBuildConstrainedFileFailsByImportPath(t *testing.T) {
	t.Parallel()
	for _, k := range []Kind{Build, Vet} {
		t.Run(k.ID, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, map[string]string{
				"only/only.go": `package only

//go:build ignore

var Only = 1

`,
			})
			var stderr bytes.Buffer
			if code := Run(k, dir, nil, &stderr); code != ExitFail {
				t.Fatalf("%s exit %d on a build-constrained-only package, want 1; stderr: %s", k.ID, code, stderr.String())
			}
			s := readSuite(t, dir, k.ReportName())
			got := outcomes(s)
			assertOneCasePerSubject(t, s, 1)
			if len(got) != 1 || !got["example.invalid/fx/only"] {
				t.Fatalf("cases = %v, want exactly one failing case: example.invalid/fx/only", got)
			}
			if s.Failures != 1 || s.Tests != 1 {
				t.Fatalf("suite counts tests=%d failures=%d, want 1 and 1", s.Tests, s.Failures)
			}
		})
	}
}

// TestEmptyModuleNoPackageCases: an empty module has zero subject packages, so the report
// holds no package case and no phantom case; the runner itself reports nothing to check.
func TestEmptyModuleNoPackageCases(t *testing.T) {
	t.Parallel()
	for _, k := range []Kind{Build, Vet} {
		t.Run(k.ID, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, map[string]string{})
			var stderr bytes.Buffer
			code := Run(k, dir, nil, &stderr)
			if code != ExitRunner {
				t.Fatalf("%s exit %d on an empty module, want %d; stderr: %s", k.ID, code, ExitRunner, stderr.String())
			}
			s := readSuite(t, dir, k.ReportName())
			if s.Errors != 1 || len(s.Cases) != 1 || s.Cases[0].Name != "runner" || s.Cases[0].Error == nil {
				t.Fatalf("an empty module leaves one runner error case, got %+v", s)
			}
			for _, c := range s.Cases {
				if strings.Contains(c.Name, "[") || strings.HasSuffix(c.Name, "_test") || strings.Contains(c.Name, ".test") {
					t.Fatalf("a phantom case was reported: %q", c.Name)
				}
			}
		})
	}
}

// TestDirectoryNamedTestIsASubjectUnderItsOwnPath reproduces B-1: a sibling directory named
// foo_test is a real package (package bad) that does not compile. Cases are keyed by go list's
// ImportPath, so foo passes under its own path and foo_test fails under its own path carrying
// foo_test's own error: no _test folding onto foo, and no foo_test case under foo.
func TestDirectoryNamedTestIsASubjectUnderItsOwnPath(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, map[string]string{
		"foo/foo.go": `package foo

func Add(a, b int) int { return a + b }

`,
		"foo_test/bad.go": `package bad

var X int = "s"

`,
	})
	for _, k := range []Kind{Build, Vet} {
		t.Run(k.ID, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			if code := Run(k, dir, nil, &stderr); code != ExitFail {
				t.Fatalf("%s exit %d on a clean package beside a broken _test directory, want 1; stderr: %s", k.ID, code, stderr.String())
			}
			s := readSuite(t, dir, k.ReportName())
			got := outcomes(s)
			assertOneCasePerSubject(t, s, 2)
			want := map[string]bool{"example.invalid/fx/foo": false, "example.invalid/fx/foo_test": true}
			if len(got) != len(want) {
				t.Fatalf("cases = %v, want exactly the two directories, foo passing and foo_test failing: %v", got, want)
			}
			for name, failed := range want {
				if got[name] != failed {
					t.Fatalf("case %q failed = %v, want %v", name, got[name], failed)
				}
			}
			if s.Failures != 1 || s.Tests != 2 {
				t.Fatalf("suite counts tests=%d failures=%d, want 2 and 1", s.Tests, s.Failures)
			}
			c := caseByName(s, "example.invalid/fx/foo_test")
			if c == nil || c.Failure == nil || !strings.Contains(c.Failure.Text, "bad.go") {
				t.Fatalf("the foo_test case does not carry its own compiler output: %+v", c)
			}
		})
	}
}

// TestDirectoryNamedTestCleanPassesUnderItsOwnPath is the clean half of B-1: foo_test/x.go
// alone compiles, so both runners report example.invalid/fx/foo_test as a passing case under
// its own path with no folding onto foo.
func TestDirectoryNamedTestCleanPassesUnderItsOwnPath(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, map[string]string{
		"foo/foo.go": okPackage,
		"foo_test/x.go": `package foo_test

// X only has to compile; it names nothing.

var X = 1

`,
	})
	for _, k := range []Kind{Build, Vet} {
		t.Run(k.ID, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			if code := Run(k, dir, nil, &stderr); code != ExitPass {
				t.Fatalf("%s exit %d on two clean packages, want 0; stderr: %s", k.ID, code, stderr.String())
			}
			got := outcomes(readSuite(t, dir, k.ReportName()))
			want := map[string]bool{"example.invalid/fx/foo": false, "example.invalid/fx/foo_test": false}
			if len(got) != len(want) || got["example.invalid/fx/foo"] || got["example.invalid/fx/foo_test"] {
				t.Fatalf("cases = %v, want every package passed: %v", got, want)
			}
		})
	}
}

// TestWorkSkippingGoflagIsRefusedByName: GOFLAGS carrying the go command's -n flag set true, in
// any spelling the go command accepts, makes go print commands and run none, so a broken module
// would pass with exit 0. The runner refuses it by name: exit 2, one runner error case, the
// token named on stderr. A false spelling is not refused: go then runs and the package fails.
func TestWorkSkippingGoflagIsRefusedByName(t *testing.T) {
	refused := []struct{ flags, token string }{
		{"-n", "-n"}, {"--n", "--n"}, {"-n=true", "-n=true"}, {"--n=true", "--n=true"},
		{"-n=1", "-n=1"}, {"-n=T", "-n=T"}, {"-n=TRUE", "-n=TRUE"},
		{"-x -n=true", "-n=true"}, {"-n=maybe", "-n=maybe"},
	}
	notRefused := []string{"-n=false", "--n=0", "-n=F", ""}
	for _, k := range []Kind{Build, Vet} {
		for _, r := range refused {
			t.Run(k.ID+"/refused/"+r.flags, func(t *testing.T) {
				dir := writeModule(t, map[string]string{"bad/bad.go": brokenPackage})
				t.Setenv("GOFLAGS", r.flags)
				var stderr bytes.Buffer
				if code := Run(k, dir, nil, &stderr); code != ExitRunner {
					t.Fatalf("%s exit %d with GOFLAGS=%q on a broken package, want %d; stderr: %s", k.ID, code, r.flags, ExitRunner, stderr.String())
				}
				if !strings.HasPrefix(stderr.String(), k.ID+": ") || !strings.Contains(stderr.String(), `"`+r.token+`"`) {
					t.Fatalf("%s stderr %q does not name the work-skipping token %q", k.ID, stderr.String(), r.token)
				}
				s := readSuite(t, dir, k.ReportName())
				if s.Errors != 1 || len(s.Cases) != 1 || s.Cases[0].Name != runnerCase || s.Cases[0].Error == nil {
					t.Fatalf("a refused go leaves one runner error case, got %+v", s)
				}
			})
		}
		for _, flags := range notRefused {
			t.Run(k.ID+"/not-refused/"+flags, func(t *testing.T) {
				dir := writeModule(t, map[string]string{"bad/bad.go": brokenPackage})
				t.Setenv("GOFLAGS", flags)
				var stderr bytes.Buffer
				if code := Run(k, dir, nil, &stderr); code != ExitFail {
					t.Fatalf("%s exit %d with GOFLAGS=%q on a broken package, want %d; stderr: %s", k.ID, code, flags, ExitFail, stderr.String())
				}
				s := readSuite(t, dir, k.ReportName())
				if s.Failures != 1 || len(s.Cases) != 1 || s.Cases[0].Failure == nil {
					t.Fatalf("the broken package must fail, got %+v", s)
				}
			})
		}
	}
}

// TestGoListFailureNamesItsCause reproduces O-1: a failed go list must not drop its cause.
// With GOFLAGS=-toolexec=true the go command cannot run its tools and list exits non-zero with
// the reason on stderr; the runner fails exit 2 and carries go list's first stderr line.
func TestGoListFailureNamesItsCause(t *testing.T) {
	dir := writeModule(t, map[string]string{"ok/ok.go": okPackage})
	t.Setenv("GOFLAGS", "-toolexec=true")
	var stderr bytes.Buffer
	if code := Run(Vet, dir, nil, &stderr); code != ExitRunner {
		t.Fatalf("vet exit %d when go list fails, want %d; stderr: %s", code, ExitRunner, stderr.String())
	}
	want := "unexpected output"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr %q does not carry go list's cause (want %q)", stderr.String(), want)
	}
	s := readSuite(t, dir, Vet.ReportName())
	if s.Errors != 1 || len(s.Cases) != 1 || s.Cases[0].Name != runnerCase || s.Cases[0].Error == nil {
		t.Fatalf("a failed go list leaves one runner error case, got %+v", s)
	}
}

func caseByName(s junit.Suite, name string) *junit.Case {
	for i := range s.Cases {
		if s.Cases[i].Name == name {
			return &s.Cases[i]
		}
	}
	return nil
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
