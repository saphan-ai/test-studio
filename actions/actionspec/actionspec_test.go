package actionspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validAction is the narrowed shape, written out in full. Every negative control below mutates
// exactly one field of it.
const validAction = `name: go-build
description: Compile every Go package and report one JUnit case per package.
inputs:
  packages:
    description: Package patterns to build.
    default: ./...
    required: false
outputs:
  junit:
    description: Path of the JUnit report, relative to the job's work directory.
    value: go-build.junit.xml
runs:
  using: composite
  steps:
    - run: go-build ${{ inputs.packages }}
      shell: saphan-proc
`

// writeAction writes body as <tmp>/<id>/action.yml and returns the file path.
func writeAction(t *testing.T, id, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "action.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func codes(rs []Refusal) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Code)
	}
	return out
}

func TestTheCatalogueActionsAreAdmitted(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"go-build", "go-vet"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			rs, err := Validate(filepath.Join("..", id, "action.yml"))
			if err != nil {
				t.Fatal(err)
			}
			if len(rs) != 0 {
				t.Fatalf("actions/%s/action.yml is refused: %v", id, rs)
			}
		})
	}
}

func TestTheNarrowedShapeIsAdmittedBeforeAnyMutation(t *testing.T) {
	t.Parallel()
	rs, err := Validate(writeAction(t, "go-build", validAction))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 0 {
		t.Fatalf("the unmutated shape is refused: %v", rs)
	}
}

func TestEachFieldOutsideTheNarrowingIsRefusedByName(t *testing.T) {
	t.Parallel()
	replace := func(old, new string) func(string) string {
		return func(s string) string {
			if !strings.Contains(s, old) {
				t.Fatalf("mutation target %q is not in the base file", old)
			}
			return strings.Replace(s, old, new, 1)
		}
	}
	cases := []struct {
		name    string
		mutate  func(string) string // mutates exactly the field the refusal names
		code    string
		keyPart string
	}{
		{"branding is legal upstream, outside the narrowing", func(s string) string { return s + "branding:\n  color: blue\n  icon: zap\n" }, CodeUnknownTopLevelKey, "branding"},
		{"shell bash", replace("shell: saphan-proc", "shell: bash"), CodeStepShell, "shell"},
		{"a second step", func(s string) string { return s + "    - run: go-build ./...\n      shell: saphan-proc\n" }, CodeStepCount, "steps"},
		{"outputs.junit removed", replace("  junit:\n    description: Path of the JUnit report, relative to the job's work directory.\n    value: go-build.junit.xml\n", "  report:\n    description: x\n    value: go-build.junit.xml\n"), CodeJunitOutputMissing, "outputs.junit"},
		{"runs.using node20", replace("using: composite", "using: node20"), CodeRunsUsing, "using"},
		{"a pipe in run", replace("run: go-build ${{ inputs.packages }}", "run: go-build ${{ inputs.packages }} | tee x"), CodeRunMetacharacter, "run"},
		{"an unterminated substitution", replace("run: go-build ${{ inputs.packages }}", "run: go-build ${{ inputs.packages"), CodeRunUnterminated, "run"},
		{"an empty quoted token", replace("run: go-build ${{ inputs.packages }}", "run: go-build \"\" ${{ inputs.packages }}"), CodeRunEmptyToken, "run"},

		{"a substitution inside a token", replace("run: go-build ${{ inputs.packages }}", "run: go-build --pkg=${{ inputs.packages }}"), CodeRunSubstitutionInToken, "run"},
		{"an undeclared input", replace("inputs.packages }}", "inputs.nosuch }}"), CodeRunUndeclaredInput, "nosuch"},
		{"a uses step", replace("- run: go-build ${{ inputs.packages }}\n      shell: saphan-proc", "- uses: actions/checkout@v4"), CodeRunsUses, "uses"},
		{"a junit value that is not literal", replace("value: go-build.junit.xml", "value: ${{ inputs.packages }}"), CodeJunitValueNotLiteral, "outputs.junit.value"},
		{"a junit value in a subdir", replace("value: go-build.junit.xml", "value: sub/go-build.junit.xml"), CodeJunitValueNotTheReportPath, "outputs.junit.value"},
		{"a junit value of another name", replace("value: go-build.junit.xml", "value: report.xml"), CodeJunitValueNotTheReportPath, "outputs.junit.value"},
		{"a junit value with a dot", replace("value: go-build.junit.xml", "value: ./go-build.junit.xml"), CodeJunitValueNotTheReportPath, "outputs.junit.value"},
		{"a junit value left blank", replace("value: go-build.junit.xml", "value: \"\""), CodeJunitValueNotLiteral, "outputs.junit.value"},

		{"an input key outside the contract", replace("    required: false\n", "    required: false\n    deprecationMessage: no\n"), CodeInputKey, "deprecationMessage"},
		{"a runner id that is not the action's directory", replace("run: go-build", "run: go-other"), CodeRunnerID, "go-other"},
		{"an author key", func(s string) string { return "author: someone\n" + s }, CodeUnknownTopLevelKey, "author"},

		{"a second YAML document", func(s string) string { return s + "---\nfoo: bar\n" }, CodeNotAMapping, "document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeAction(t, "go-build", tc.mutate(validAction))
			rs, err := Validate(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rs {
				if r.Code == tc.code && strings.Contains(r.Key, tc.keyPart) && r.File == path {
					if !strings.Contains(r.String(), tc.code) || !strings.Contains(r.String(), path) {
						t.Fatalf("the refusal text does not name the code and the file: %q", r.String())
					}
					return
				}
			}
			t.Fatalf("no refusal %s naming %q for file %s; got %v", tc.code, tc.keyPart, path, codes(rs))
		})
	}
}

func TestTokenizeSplitsOnWhitespaceGroupsQuotesAndKeepsASubstitutionWhole(t *testing.T) {
	t.Parallel()
	toks, err := tokenize(`go-build "a b" ${{ inputs.packages }} plain`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go-build", "a b", "${{ inputs.packages }}", "plain"}
	if len(toks) != len(want) {
		t.Fatalf("tokens = %+v, want %q", toks, want)
	}
	for i := range want {
		if toks[i].text != want[i] {
			t.Fatalf("token %d = %q, want %q", i, toks[i].text, want[i])
		}
	}
	if _, err := tokenize(`go-build "unterminated`); err == nil {
		t.Fatal("an unterminated quote must be refused")
	}
}

func TestADuplicateKeyAndAnUnreadableFileAreRefusedNotCrashed(t *testing.T) {
	t.Parallel()
	rs, err := Validate(writeAction(t, "go-build", validAction+"name: again\n"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rs {
		found = found || r.Code == CodeDuplicateKey
	}
	if !found {
		t.Fatalf("a duplicate top-level key is not refused: %v", codes(rs))
	}
	if _, err := Validate(filepath.Join(t.TempDir(), "go-build", "action.yml")); err == nil {
		t.Fatal("a missing file must be an error, not a pass")
	}
}
