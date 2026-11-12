// Package gocheck is the shared body of the go-build and go-vet catalogue runners. A runner runs
// one Go check over the packages its arguments select and always leaves a JUnit report in the
// work directory whose cases are the packages it checked.
//
// Exit codes: 0 every case passed, 1 a case failed, 2 the runner itself could not run (the reason
// is named on stderr, and the report then holds one error case).
package gocheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/saphan-ai/test-studio/conformance/junit"
)

// The exit codes of a runner.
const (
	ExitPass   = 0
	ExitFail   = 1
	ExitRunner = 2
)

const (
	// DefaultPattern selects every package of the module when no argument is given.
	DefaultPattern = "./..."
	// MaxFailureText bounds the failure text one case carries, in bytes.
	MaxFailureText = 64 * 1024
	// GoTimeout bounds one invocation of the go command.
	GoTimeout = 15 * time.Minute
	// runnerCase names the single error case of a report left by a runner that could not run.
	runnerCase = "runner"
	// caseTime is the time attribute of every case: the go command reports no per-package time.
	caseTime = "0.000"
)

// Kind is one catalogue runner: its id, the go subcommand it runs, and which packages it checks.
type Kind struct {
	// ID is the catalogue runner id, also the report's base name and the suite name.
	ID string
	// Sub is the go subcommand.
	Sub string
	// testFiles makes a package with only test files a subject of the check.
	testFiles bool
}

var (
	// Build is the go-build runner.
	Build = Kind{ID: "go-build", Sub: "build"}
	// Vet is the go-vet runner.
	Vet = Kind{ID: "go-vet", Sub: "vet", testFiles: true}
)

// ReportName is the file the runner writes in the work directory.
func (k Kind) ReportName() string { return k.ID + ".junit.xml" }

// listed is the part of `go list -json` the runner reads. A package whose imports cannot be
// resolved is listed with DepsErrors even when its top-level Error is nil; such a package is a
// load error the go command never compiles, so the runner reports it from go list output and
// never hands it to the go subcommand.
type listed struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
	Error        *struct{ Err string }
	DepsErrors   []struct {
		ImportStack []string
		Pos         string
		Err         string
	}
}

func (k Kind) subject(p listed) bool {
	if p.Error != nil || len(p.GoFiles) > 0 || len(p.CgoFiles) > 0 {
		return true
	}
	return k.testFiles && (len(p.TestGoFiles) > 0 || len(p.XTestGoFiles) > 0)
}

// Main runs the runner against the current directory with the process arguments.
func Main(k Kind, args []string, stderr io.Writer) int {
	return Run(k, ".", args, stderr)
}

// Run checks the packages the patterns select (DefaultPattern when there are none) in workDir,
// writes the report there and returns the exit code.
func Run(k Kind, workDir string, patterns []string, stderr io.Writer) int {
	if len(patterns) == 0 {
		patterns = []string{DefaultPattern}
	}
	if flag := workSkippingGoflag(workDir); flag != "" {
		return runnerFailure(k, workDir, fmt.Errorf("go: GOFLAGS carries %q, which makes the go command skip work while exiting 0; the runner refuses to run it", flag), stderr)
	}
	cases, err := check(k, workDir, patterns)
	if err != nil {
		return runnerFailure(k, workDir, err, stderr)
	}
	s := junit.Suite{Name: k.ID, Tests: len(cases), Cases: cases}
	failed := false
	for _, c := range cases {
		if c.Failure != nil {
			s.Failures++
			failed = true
		}
	}
	if err := junit.Write(filepath.Join(workDir, k.ReportName()), s); err != nil {
		fmt.Fprintf(stderr, "%s: cannot write %s: %v\n", k.ID, k.ReportName(), err)
		return ExitRunner
	}
	if failed {
		return ExitFail
	}
	return ExitPass
}

// runnerFailure names the reason on stderr and still leaves a report holding one error case.
func runnerFailure(k Kind, workDir string, cause error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s: cannot run: %v\n", k.ID, cause)
	s := junit.Suite{Name: k.ID, Tests: 1, Errors: 1, Cases: []junit.Case{{
		Name: runnerCase, Time: caseTime, Error: &junit.Problem{Message: cause.Error(), Text: cause.Error()},
	}}}
	if err := junit.Write(filepath.Join(workDir, k.ReportName()), s); err != nil {
		fmt.Fprintf(stderr, "%s: cannot write %s: %v\n", k.ID, k.ReportName(), err)
	}
	return ExitRunner
}

// check lists the packages and returns one case per subject package. The default is not-pass: a
// subject starts from "not checked" and becomes pass only when the go command's output positively
// shows that package was checked and reported nothing for it. Every subject left not checked is a
// failing case named by its import path, with a failure text that says why (load error, an error
// the go command reported for the package, or that the go command stopped before reaching it).
//
// The go subcommand is run once over every load-clean subject. When it exits 0 it checked every
// subject and reported nothing, so all subjects pass on that single positive evidence. When it
// does not exit 0 it stops at the first failure and never reports on the rest, so each subject is
// then run on its own: exit 0 is again positive evidence of a clean package, anything else is a
// failure carrying that package's own go command output.
//
// Case names come from go list output only. Header forms the go command prints for test variants
// (# <path>, # [<path>], # <path> [<path>.test], # <path>_test [<path>.test]) never name a case:
// a package whose import path ends in _test is the external test variant of the package named
// without the suffix, so its findings are folded onto the owning package's case and no _test
// case is ever emitted.
func check(k Kind, workDir string, patterns []string) ([]junit.Case, error) {
	for _, p := range patterns {
		if p == "" || strings.HasPrefix(p, "-") || strings.ContainsRune(p, 0) {
			return nil, fmt.Errorf("pattern %q is not a package pattern (it must not be empty or start with '-')", p)
		}
	}
	pkgs, err := list(workDir, patterns)
	if err != nil {
		return nil, err
	}
	var subjects, unreachable []listed
	for _, p := range pkgs {
		if !k.subject(p) {
			continue
		}
		if p.Error != nil || len(p.DepsErrors) > 0 {
			unreachable = append(unreachable, p)
			continue
		}
		subjects = append(subjects, p)
	}
	if len(subjects) == 0 && len(unreachable) == 0 {
		return nil, fmt.Errorf("no package to check matched %s", strings.Join(patterns, " "))
	}
	combinedFailed := false
	if len(subjects) > 0 {
		dirs := make([]string, 0, len(subjects))
		for _, p := range subjects {
			dirs = append(dirs, p.Dir)
		}
		_, _, exit, err := goRun(workDir, append([]string{k.Sub, "--"}, dirs...), true)
		if err != nil {
			return nil, err
		}
		combinedFailed = exit != 0
	}
	// results is keyed by the case name: go list's ImportPath, exactly as printed. Test-variant
	// spellings never share a case: go list lists each package under its own path, and a package
	// whose path ends in _test is a real subject under its own path, never folded onto the path
	// without the suffix. go-vet headers (# <p>, # [<p>.test], # <p>_test [<p>.test]) never
	// name a case: a test variant reaches its owning package only through go list output, never
	// by editing a bare path. order records first appearance.
	results := map[string]string{}
	order := []string{}
	add := func(p listed, text string) {
		key := p.ImportPath
		if _, ok := results[key]; !ok {
			order = append(order, key)
			results[key] = ""
		}
		if text != "" {
			if results[key] != "" {
				results[key] += "\n" + text
			} else {
				results[key] = text
			}
		}
	}
	for _, p := range subjects {
		var text string
		if combinedFailed {
			out, _, exit, err := goRun(p.Dir, []string{k.Sub, "."}, true)
			if err != nil {
				return nil, err
			}
			if exit != 0 {
				text = strings.TrimSpace(string(out))
			}
		}
		add(p, text)
	}
	for _, p := range unreachable {
		var text []string
		if p.Error != nil {
			text = append(text, p.Error.Err)
		}
		for _, e := range p.DepsErrors {
			text = append(text, e.Err)
		}
		add(p, strings.Join(text, "\n"))
	}
	var cases []junit.Case
	for _, key := range order {
		cases = append(cases, makeCase(key, []string{results[key]}))
	}
	return cases, nil
}

func makeCase(name string, text []string) junit.Case {
	c := junit.Case{Name: name, Time: caseTime}
	joined := strings.TrimSpace(strings.Join(text, "\n"))
	if joined == "" {
		return c
	}
	if len(joined) > MaxFailureText {
		joined = joined[:MaxFailureText] + "\n[output cut at the runner's size limit]"
	}
	first, _, _ := strings.Cut(joined, "\n")
	c.Failure = &junit.Problem{Message: first, Text: joined}
	return c
}

// list runs `go list -e -json` and decodes the stream of package objects.
func list(workDir string, patterns []string) ([]listed, error) {
	out, listStderr, exit, err := goRun(workDir, append([]string{"list", "-e", "-json", "--"}, patterns...), false)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []listed
	for {
		var p listed
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list gave output that is not a package list (exit %d): %s", exit, firstLine(out))
		}
		pkgs = append(pkgs, p)
	}
	if exit != 0 && len(pkgs) == 0 {
		return nil, fmt.Errorf("go list could not list %s: %s", strings.Join(patterns, " "), firstLine(listStderr))
	}
	return pkgs, nil
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return s
}

// goRun runs the go command in dir and returns its combined output and exit code. An error is
// returned only when the command could not be run to an exit: not found, not started, timed out.
func goRun(dir string, args []string, combined bool) ([]byte, []byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GoTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		out, serr := stdoutBuf.Bytes(), stderrBuf.Bytes()
		if combined {
			out = append(out, serr...)
			serr = nil
		}
		return out, serr, 0, nil
	case ctx.Err() != nil:
		return nil, nil, 0, fmt.Errorf("go %s did not finish within %s", args[0], GoTimeout)
	case errors.As(err, &exit):
		out, serr := stdoutBuf.Bytes(), stderrBuf.Bytes()
		if combined {
			out = append(out, serr...)
			serr = nil
		}
		return out, serr, exit.ExitCode(), nil
	default:
		return nil, nil, 0, fmt.Errorf("cannot start go: %w", err)
	}
}

// goEnvGoflags reports the effective GOFLAGS: `go env GOFLAGS`, which also reports values
// saved with `go env -w`, falling back to the process environment when go itself cannot run
// (a work-skipping GOFLAGS breaks go env as well).
func goEnvGoflags(workDir string) string {
	cmd := exec.Command("go", "env", "GOFLAGS")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return os.Getenv("GOFLAGS")
	}
	return strings.TrimSpace(string(out))
}

// workSkippingGoflag returns a flag in the effective GOFLAGS that makes the go command skip
// work while still exiting 0, or "" when none is set. The set is derived from the pinned
// toolchain's own documentation: `go help build` documents -n as "print the commands but do
// not run them" -- it exits 0 without compiling anything, which is exactly B-2. Other flags
// (for example -toolexec) may change how tools run or fail on their own, but they do not
// silently skip the work, so they are not refused here; a failed go list names its cause
// instead (see list).
func workSkippingGoflag(workDir string) string {
	for _, tok := range strings.Fields(goEnvGoflags(workDir)) {
		tok = strings.Trim(tok, `"'`)
		if tok == "-n" {
			return tok
		}
	}
	return ""
}
