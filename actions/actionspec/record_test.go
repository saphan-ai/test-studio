package actionspec

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// recordSchemaPath is the existing tool-invocation/v1 record schema; this repository has one.
var recordSchemaPath = filepath.Join("..", "..", "conformance", "cmd", "tool-conformance", "tool-invocation-v1.schema.json")

type record struct {
	ID      string   `yaml:"id"`
	Command []string `yaml:"command"`
	Pin     struct {
		Asset  string `yaml:"asset"`
		SHA256 string `yaml:"sha256"`
	} `yaml:"pin"`
	Output struct {
		Format string `yaml:"format"`
		Path   string `yaml:"path"`
	} `yaml:"output"`
	Fixture struct {
		Repo string `yaml:"repo"`
	} `yaml:"fixture"`
}

func loadRecord(t *testing.T, id string) (record, any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", id, "record.yaml"))
	if err != nil {
		t.Fatalf("actions/%s has no record.yaml beside its action.yml: %v", id, err)
	}
	var rec record
	var doc any
	if err := yaml.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return rec, doc
}

func TestEachRecordIsAValidToolInvocationAndAgreesWithItsAction(t *testing.T) {
	t.Parallel()
	schema, err := jsonschema.NewCompiler().Compile(recordSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"go-build", "go-vet"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			rec, doc := loadRecord(t, id)
			if err := schema.Validate(normalise(doc)); err != nil {
				t.Fatalf("actions/%s/record.yaml is not a tool-invocation/v1 record: %v", id, err)
			}
			if rec.ID != id || rec.Pin.Asset != id || len(rec.Command) == 0 || rec.Command[0] != id {
				t.Fatalf("record %+v does not name the runner %q as its id, asset and command[0]", rec, id)
			}
			if rec.Output.Format != "junit-xml" || rec.Output.Path != id+".junit.xml" {
				t.Fatalf("record output %+v must be the action's literal JUnit path %s.junit.xml", rec.Output, id)
			}
			action, err := os.ReadFile(filepath.Join("..", id, "action.yml"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(action), "value: "+rec.Output.Path+"\n") {
				t.Fatalf("actions/%s/action.yml does not declare outputs.junit.value %s", id, rec.Output.Path)
			}
			if _, err := os.Stat(filepath.Join("..", "..", "conformance", "testdata", "tool-fixtures", rec.Fixture.Repo)); err != nil {
				t.Fatalf("the record's fixture does not exist: %v", err)
			}
		})
	}
}

// normalise turns the map[string]interface{} keys yaml.v3 makes for integer keys (exit_map: {0: ...})
// into strings, the way the record schema reads them.
func normalise(x any) any {
	switch v := x.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range v {
			out[k] = normalise(e)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, e := range v {
			out[strings.TrimSpace(toString(k))] = normalise(e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = normalise(e)
		}
		return out
	default:
		return x
	}
}

func toString(k any) string {
	if s, ok := k.(string); ok {
		return s
	}
	b, _ := yaml.Marshal(k)
	return string(b)
}

// TestRecordPinsTheRunnerBinary builds each runner the way the contract says and compares the
// sha256 of the binary with the digest in its record. The digest depends on the toolchain and the
// platform, so the comparison runs only where both match actions/pin-toolchain.txt.
func TestRecordPinsTheRunnerBinary(t *testing.T) {
	pinned, err := os.ReadFile(filepath.Join("..", "pin-toolchain.txt"))
	if err != nil {
		t.Fatal(err)
	}
	here := runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
	if here != strings.TrimSpace(string(pinned)) {
		t.Skipf("the digests were taken with %q and this is %q; a digest is only comparable on the same toolchain and platform (no CI job covers the pinned one yet)", strings.TrimSpace(string(pinned)), here)
	}
	for _, id := range []string{"go-build", "go-vet"} {
		t.Run(id, func(t *testing.T) {
			rec, _ := loadRecord(t, id)
			bin := filepath.Join(t.TempDir(), id)
			cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-buildid=", "-o", bin, "../"+id)
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("cannot build %s: %v\n%s", id, err, out)
			}
			body, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(body)
			if got := hex.EncodeToString(sum[:]); got != rec.Pin.SHA256 {
				t.Fatalf("actions/%s/record.yaml pins sha256 %s but the runner builds to %s", id, rec.Pin.SHA256, got)
			}
		})
	}
}
