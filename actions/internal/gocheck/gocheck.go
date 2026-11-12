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
	// unattributedCase names the case that holds failure output no package header claimed.
	unattributedCase = "(output not attributed to a package)"
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

// listed is the part of `go list -json` the runner reads.
type listed struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
	Error        *struct{ Err string }
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

// check lists the packages, runs the go subcommand once over them and returns one case each.
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
	var subjects []listed
	for _, p := range pkgs {
		if k.subject(p) {
			subjects = append(subjects, p)
		}
	}
	if len(subjects) == 0 {
		return nil, fmt.Errorf("no package to check matched %s", strings.Join(patterns, " "))
	}
	out, exit, err := goRun(workDir, append([]string{k.Sub, "--"}, patterns...), true)
	if err != nil {
		return nil, err
	}
	sections := splitSections(out)
	sections.attributeByFile(workDir, subjects)
	var cases []junit.Case
	seen := map[string]bool{}
	for _, p := range subjects {
		seen[p.ImportPath] = true
		var text []string
		if p.Error != nil {
			text = append(text, p.Error.Err)
		}
		if s, ok := sections.byName[p.ImportPath]; ok {
			text = append(text, s)
		}
		cases = append(cases, makeCase(p.ImportPath, text))
	}
	for _, name := range sections.order {
		if !seen[name] && name != "" { // a package outside the patterns that failed
			cases = append(cases, makeCase(name, []string{sections.byName[name]}))
		}
	}
	// Unheaded continuation output (a line no "# <import path>" header claimed)
	// belongs to the previous attributed line's package: one broken package is
	// one failing case, not two.
	leftover := strings.TrimSpace(sections.byName[""])
	if leftover != "" && exit != 0 {
		attached := false
		for i := len(cases) - 1; i >= 0; i-- {
			if cases[i].Failure != nil {
				cases[i].Failure.Text = strings.TrimSpace(cases[i].Failure.Text + "\n" + leftover)
				attached = true
				break
			}
		}
		if !attached {
			cases = append(cases, makeCase(unattributedCase, []string{leftover}))
		}
	}
	if exit != 0 && !anyFailed(cases) {
		cases = append(cases, makeCase(unattributedCase, []string{fmt.Sprintf("go %s exited with status %d and named no package", k.Sub, exit)}))
	}
	return cases, nil
}

func anyFailed(cases []junit.Case) bool {
	for _, c := range cases {
		if c.Failure != nil {
			return true
		}
	}
	return false
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

// sectionSet is the compiler or vet output of one run, filed under its "# <import path>" headers.
type sectionSet struct {
	byName map[string]string
	order  []string
}

// attributeByFile moves the unheaded lines that start with "<file>:" (go vet prints its findings
// that way, with no "# <import path>" header) under the package whose directory holds the file.
// A line it cannot place stays unattributed.
func (s *sectionSet) attributeByFile(workDir string, subjects []listed) {
	root, err := filepath.Abs(workDir)
	if err != nil {
		return
	}
	dirs := map[string]string{}
	for _, p := range subjects {
		if p.Dir != "" {
			dirs[realPath(p.Dir)] = p.ImportPath
		}
	}
	var kept []string
	for _, line := range strings.Split(s.byName[""], "\n") {
		if line == "" {
			continue
		}
		file, _, found := strings.Cut(strings.TrimPrefix(line, "vet: "), ":")
		if !found {
			kept = append(kept, line)
			continue
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(root, file)
		}
		name, ok := dirs[realPath(filepath.Dir(file))]
		if !ok {
			kept = append(kept, line)
			continue
		}
		if _, seen := s.byName[name]; !seen {
			s.order = append(s.order, name)
		}
		s.byName[name] += line + "\n"
	}
	s.byName[""] = strings.Join(kept, "\n")
}

// realPath resolves symbolic links so two spellings of one directory compare equal.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// splitSections cuts go build or go vet output at its "# <import path>" headers. Text before the
// first header is filed under the empty name.
func splitSections(out []byte) sectionSet {
	set := sectionSet{byName: map[string]string{}}
	cur := ""
	file := func(name string) {
		if _, ok := set.byName[name]; !ok {
			set.byName[name] = ""
			set.order = append(set.order, name)
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "# ") {
			cur, _, _ = strings.Cut(strings.TrimPrefix(line, "# "), " ")
			file(cur)
			continue
		}
		if line == "" {
			continue
		}
		file(cur)
		set.byName[cur] += line + "\n"
	}
	return set
}

// list runs `go list -e -json` and decodes the stream of package objects.
func list(workDir string, patterns []string) ([]listed, error) {
	out, exit, err := goRun(workDir, append([]string{"list", "-e", "-json", "--"}, patterns...), false)
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
		return nil, fmt.Errorf("go list could not list %s: %s", strings.Join(patterns, " "), firstLine(out))
	}
	return pkgs, nil
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return s
}

// goRun runs the go command in dir and returns its combined output and exit code. An error is
// returned only when the command could not be run to an exit: not found, not started, timed out.
func goRun(dir string, args []string, combined bool) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GoTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	var out []byte
	var err error
	if combined {
		out, err = cmd.CombinedOutput()
	} else {
		out, err = cmd.Output() // stdout only: a warning on stderr must not corrupt the JSON stream
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out, 0, nil
	case ctx.Err() != nil:
		return nil, 0, fmt.Errorf("go %s did not finish within %s", args[0], GoTimeout)
	case errors.As(err, &exit):
		return out, exit.ExitCode(), nil
	default:
		return nil, 0, fmt.Errorf("cannot start go: %w", err)
	}
}
