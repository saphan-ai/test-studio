// Package actionspec validates a catalogue action's action.yml. A file is admitted when it is
// valid against the vendored SchemaStore github-action schema (draft-07) and sits inside the
// narrowing spec/TOOL_ACTION_V1.md states. Anything outside the narrowing is refused by name,
// with the key and the file. The schema is vendored and embedded: the validator never fetches it.
package actionspec

import (
	"bytes"
	_ "embed" // the vendored schema is embedded in the binary
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

//go:embed github-action.schema.json
var schemaBytes []byte

// schemaResource is the name the embedded schema is registered under.
const schemaResource = "github-action.schema.json"

// ProcShell is the one step shell the contract admits: the engine runs the step as argv, never
// through a shell.
const ProcShell = "saphan-proc"

// JunitOutput is the output every catalogue action must declare.
const JunitOutput = "junit"

// The refusal codes. A code is the name a refusal is known by.
const (
	CodeYAMLSyntax                 = "yaml-syntax"
	CodeNotAMapping                = "not-a-mapping"
	CodeDuplicateKey               = "duplicate-key"
	CodeAlias                      = "yaml-alias"
	CodeSchema                     = "schema-violation"
	CodeUnknownTopLevelKey         = "unknown-top-level-key"
	CodeInputKey                   = "input-key-outside-contract"
	CodeOutputKey                  = "output-key-outside-contract"
	CodeJunitOutputMissing         = "junit-output-missing"
	CodeJunitValueNotLiteral       = "junit-value-not-literal"
	CodeJunitValueNotTheReportPath = "junit-value-not-the-report-path"
	CodeRunsKey                    = "runs-key-outside-contract"
	CodeRunsUsing                  = "runs-using-not-composite"
	CodeRunsUses                   = "runs-uses"
	CodeStepCount                  = "runs-steps-count"
	CodeStepKey                    = "step-key-outside-contract"
	CodeStepShell                  = "step-shell-not-saphan-proc"
	CodeStepRunMissing             = "step-run-missing"
	CodeRunUnterminated            = "run-unterminated"
	CodeRunMetacharacter           = "run-shell-metacharacter"
	CodeRunSubstitutionInToken     = "run-substitution-inside-token"
	CodeRunExpression              = "run-expression-unsupported"
	CodeRunUndeclaredInput         = "run-undeclared-input"
	CodeRunnerID                   = "run-runner-id-mismatch"
	CodeRunEmptyToken              = "run-empty-token"
)

// Refusal is one thing the validator refused: the file, the refusal's name, the key it is about
// and a sentence saying what was measured and what to change.
type Refusal struct {
	File    string
	Code    string
	Key     string
	Message string
}

// String renders the refusal on one line, naming the file, the code and the key.
func (r Refusal) String() string {
	return fmt.Sprintf("%s: refused %s at %s: %s", r.File, r.Code, r.Key, r.Message)
}

var (
	schemaOnce sync.Once
	schema     *jsonschema.Schema
	schemaErr  error
)

// compiled compiles the embedded schema once per process.
func compiled() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.Draft = jsonschema.Draft7
		if err := c.AddResource(schemaResource, bytes.NewReader(schemaBytes)); err != nil {
			schemaErr = err
			return
		}
		schema, schemaErr = c.Compile(schemaResource)
	})
	return schema, schemaErr
}

// Validate reads the action.yml at path and returns every refusal, in document order. An error is
// returned only when the file cannot be read or the embedded schema cannot be compiled; a file
// that is merely outside the contract is a list of refusals, and an admitted file is an empty one.
func Validate(path string) ([]Refusal, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	sch, err := compiled()
	if err != nil {
		return nil, fmt.Errorf("cannot compile the vendored schema: %w", err)
	}
	v := &validation{file: path, id: filepath.Base(filepath.Dir(abs))}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		v.refuse(CodeYAMLSyntax, "document", "the file is not YAML: %v", err)
		return v.refusals, nil
	}
	if err := dec.Decode(&yaml.Node{}); err != io.EOF {
		v.refuse(CodeNotAMapping, "document", "the file holds more than one YAML document; the contract admits one")
		return v.refusals, nil
	}
	doc := v.toAny(root.Content[0], "")
	top, ok := doc.(map[string]any)
	if !ok {
		v.refuse(CodeNotAMapping, "document", "the top level must be a mapping of action metadata keys")
		return v.refusals, nil
	}
	if err := sch.Validate(doc); err != nil {
		v.schemaProblems(err)
	}
	v.narrow(top)
	return v.refusals, nil
}

type validation struct {
	file     string
	id       string // the directory name of the action, which the runner id must equal
	refusals []Refusal
}

func (v *validation) refuse(code, key, format string, args ...any) {
	v.refusals = append(v.refusals, Refusal{File: v.file, Code: code, Key: key, Message: fmt.Sprintf(format, args...)})
}

// toAny converts a YAML node to plain Go values and refuses a duplicate key or an alias, which a
// map would otherwise hide.
func (v *validation) toAny(n *yaml.Node, path string) any {
	switch n.Kind {
	case yaml.AliasNode:
		v.refuse(CodeAlias, orDoc(path), "a YAML alias is not admitted; write the value out")
		return nil
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			child := join(path, key)
			if _, dup := m[key]; dup {
				v.refuse(CodeDuplicateKey, child, "the key %q appears twice; a map would keep only one of them", key)
			}
			m[key] = v.toAny(n.Content[i+1], child)
		}
		return m
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for i, c := range n.Content {
			out = append(out, v.toAny(c, fmt.Sprintf("%s[%d]", path, i)))
		}
		return out
	default:
		var x any
		if err := n.Decode(&x); err != nil {
			return n.Value
		}
		return x
	}
}

func orDoc(path string) string {
	if path == "" {
		return "document"
	}
	return path
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// schemaProblems turns the leaf causes of a schema error into refusals.
func (v *validation) schemaProblems(err error) {
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		v.refuse(CodeSchema, "document", "%v", err)
		return
	}
	seen := map[string]bool{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			key := strings.ReplaceAll(strings.TrimPrefix(e.InstanceLocation, "/"), "/", ".")
			line := key + "|" + e.Message
			if !seen[line] {
				seen[line] = true
				v.refuse(CodeSchema, orDoc(key), "outside the SchemaStore github-action schema: %s", e.Message)
			}
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func asMap(x any) (map[string]any, bool) {
	m, ok := x.(map[string]any)
	return m, ok
}

// narrow enforces the contract on top of the schema.
func (v *validation) narrow(top map[string]any) {
	allowed := map[string]bool{"name": true, "description": true, "inputs": true, "outputs": true, "runs": true}
	for _, k := range sortedKeys(top) {
		if !allowed[k] {
			v.refuse(CodeUnknownTopLevelKey, k, "the top-level key %q is outside the contract; admitted keys are name, description, inputs, outputs and runs", k)
		}
	}
	declared := v.narrowInputs(top["inputs"])
	v.narrowOutputs(top["outputs"])
	if runs, ok := asMap(top["runs"]); ok {
		v.narrowRuns(runs, declared)
	}
}

// narrowInputs admits description, default and required on each input and returns the input ids.
func (v *validation) narrowInputs(x any) map[string]bool {
	declared := map[string]bool{}
	inputs, ok := asMap(x)
	if !ok {
		return declared
	}
	for _, id := range sortedKeys(inputs) {
		declared[id] = true
		in, ok := asMap(inputs[id])
		if !ok {
			continue
		}
		for _, k := range sortedKeys(in) {
			if k != "description" && k != "default" && k != "required" {
				v.refuse(CodeInputKey, "inputs."+id+"."+k, "the input key %q is outside the contract; an input carries description, default and required", k)
			}
		}
	}
	return declared
}

// narrowOutputs requires outputs.junit and a literal relative path for its value.
func (v *validation) narrowOutputs(x any) {
	outputs, _ := asMap(x)
	junit, ok := asMap(outputs[JunitOutput])
	if !ok {
		v.refuse(CodeJunitOutputMissing, "outputs.junit", "the action declares no output named %q; the engine reads the JUnit report from it", JunitOutput)
	}
	for _, id := range sortedKeys(outputs) {
		out, ok := asMap(outputs[id])
		if !ok {
			continue
		}
		for _, k := range sortedKeys(out) {
			if k != "description" && k != "value" {
				v.refuse(CodeOutputKey, "outputs."+id+"."+k, "the output key %q is outside the contract; an output carries description and value", k)
			}
		}
	}
	if !ok {
		return
	}
	val, isString := junit["value"].(string)
	switch {
	case !isString || val == "":
		v.refuse(CodeJunitValueNotLiteral, "outputs.junit.value", "outputs.junit.value is missing; it must be the literal path of the JUnit report")
	case strings.Contains(val, "${{") || strings.ContainsAny(val, "\\\n"):
		v.refuse(CodeJunitValueNotLiteral, "outputs.junit.value", "outputs.junit.value %q is not a literal path; write the report's relative path out", val)
	case filepath.IsAbs(val) || strings.HasPrefix(val, "/") || hasDotDot(val):
		v.refuse(CodeJunitValueNotLiteral, "outputs.junit.value", "outputs.junit.value %q is not a path relative to the job's work directory", val)
	case val != v.id+".junit.xml":
		v.refuse(CodeJunitValueNotTheReportPath, "outputs.junit.value", "outputs.junit.value %q is not the report path; the runner writes %q", val, v.id+".junit.xml")
	}
}

func hasDotDot(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// narrowRuns admits only a composite run of exactly one saphan-proc step.
func (v *validation) narrowRuns(runs map[string]any, declared map[string]bool) {
	for _, k := range sortedKeys(runs) {
		switch k {
		case "using", "steps":
		case "uses":
			v.refuse(CodeRunsUses, "runs.uses", "a catalogue action runs its own runner and uses no other action")
		default:
			v.refuse(CodeRunsKey, "runs."+k, "the runs key %q is outside the contract; runs carries using and steps", k)
		}
	}
	if using, _ := runs["using"].(string); using != "composite" {
		v.refuse(CodeRunsUsing, "runs.using", "runs.using is %q; the contract admits only composite", using)
	}
	steps, ok := runs["steps"].([]any)
	if !ok {
		return
	}
	if len(steps) != 1 {
		v.refuse(CodeStepCount, "runs.steps", "runs.steps holds %d steps; the contract admits exactly one", len(steps))
	}
	for i, s := range steps {
		if step, ok := asMap(s); ok {
			v.narrowStep(fmt.Sprintf("runs.steps[%d]", i), step, declared)
		}
	}
}

func (v *validation) narrowStep(at string, step map[string]any, declared map[string]bool) {
	for _, k := range sortedKeys(step) {
		switch k {
		case "run", "shell":
		case "uses":
			v.refuse(CodeRunsUses, at+".uses", "a catalogue action runs its own runner and uses no other action")
		default:
			v.refuse(CodeStepKey, at+"."+k, "the step key %q is outside the contract; a step carries run and shell", k)
		}
	}
	if shell, _ := step["shell"].(string); shell != ProcShell {
		v.refuse(CodeStepShell, at+".shell", "shell is %q; the engine starts no shell and the contract admits only %s", shell, ProcShell)
	}
	run, ok := step["run"].(string)
	if !ok {
		if _, uses := step["uses"]; !uses {
			v.refuse(CodeStepRunMissing, at+".run", "the step has no run string")
		}
		return
	}
	v.narrowRun(at+".run", run, declared)
}

// forbidden are the characters the engine refuses in a run string, because it splits the string
// into argv itself: no shell syntax, no variables, no globbing, no escapes, no single quotes.
const forbidden = "|;&<>`$*?[]{}~\\'\n\r"

var (
	exprPattern  = regexp.MustCompile(`\$\{\{.*?\}\}`)
	inputPattern = regexp.MustCompile(`^inputs\.([A-Za-z_][A-Za-z0-9_-]*)$`)
	runnerID     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// narrowRun checks one run string the way the engine will split it.
// The tokenizer runs first: a ${{ without a closing }} (or an unclosed quote)
// is run-unterminated, ahead of the metacharacter scan, so an unterminated
// substitution is refused as run-unterminated as spec/TOOL_ACTION_V1.md says.
func (v *validation) narrowRun(at, run string, declared map[string]bool) {
	toks, err := tokenize(run)
	if err != nil {
		v.refuse(CodeRunUnterminated, at, "%v", err)
		return
	}
	masked := exprPattern.ReplaceAllString(run, " ")
	for _, r := range masked {
		if strings.ContainsRune(forbidden, r) {
			v.refuse(CodeRunMetacharacter, at, "run contains %q, which is shell syntax; the engine starts no shell, so write plain words", string(r))
			return
		}
	}
	for _, t := range toks {
		if t.text == "" {
			v.refuse(CodeRunEmptyToken, at, "run has an empty token; the engine would pass an empty argv word; remove the empty quotes")
			return
		}
	}
	if len(toks) == 0 {
		v.refuse(CodeRunnerID, at, "run is empty; token 0 must be the runner id %q", v.id)
		return
	}
	if toks[0].text != v.id || !runnerID.MatchString(toks[0].text) || toks[0].expr {
		v.refuse(CodeRunnerID, at+" ("+toks[0].text+")", "token 0 is %q; it must be the runner id %q, the name of this action's directory", toks[0].text, v.id)
	}
	for _, t := range toks {
		switch {
		case t.inner:
			v.refuse(CodeRunSubstitutionInToken, at, "the substitution in %q shares a token with other text; ${{ inputs.<name> }} must be a whole token", t.text)
		case t.expr:
			m := inputPattern.FindStringSubmatch(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(t.text, "${{"), "}}")))
			switch {
			case m == nil:
				v.refuse(CodeRunExpression, at, "the expression %q is not supported; only ${{ inputs.<name> }} is substituted", t.text)
			case !declared[m[1]]:
				v.refuse(CodeRunUndeclaredInput, at+" (inputs."+m[1]+")", "run refers to input %q, which the action does not declare", m[1])
			}
		}
	}
}

// token is one argv word of a run string.
type token struct {
	text  string
	expr  bool // the whole token is one ${{ … }} substitution
	inner bool // the token holds a ${{ … }} substitution beside other text
}

// tokenize splits a run string the way the engine does: on whitespace, with "double quotes"
// grouping a word, and a ${{ … }} substitution read as one piece whatever it holds inside.
func tokenize(s string) ([]token, error) {
	var toks []token
	for i := 0; i < len(s); {
		if isSpace(s[i]) {
			i++
			continue
		}
		var t token
		var pieces int
		for i < len(s) && !isSpace(s[i]) {
			switch {
			case strings.HasPrefix(s[i:], "${{"):
				end := strings.Index(s[i:], "}}")
				if end < 0 {
					return nil, fmt.Errorf("run has a ${{ with no closing }}")
				}
				t.text += s[i : i+end+2]
				t.expr = true
				i += end + 2
				pieces++
			case s[i] == '"':
				end := strings.IndexByte(s[i+1:], '"')
				if end < 0 {
					return nil, fmt.Errorf("run has a double quote that is never closed")
				}
				quoted := s[i+1 : i+1+end]
				if strings.Contains(quoted, "${{") {
					t.inner = true
				}
				t.text += quoted
				i += end + 2
				pieces++
			default:
				t.text += s[i : i+1]
				i++
				pieces++
			}
		}
		if t.expr && (pieces > 1 || !strings.HasPrefix(t.text, "${{")) {
			t.inner, t.expr = true, false
		}
		toks = append(toks, t)
	}
	return toks, nil
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }
