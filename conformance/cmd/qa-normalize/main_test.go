package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/test-studio/conformance/internal/schemas"
)

const (
	dataDir    = "../../testdata/qa"
	schemaPath = "../../../spec/schemas/qa-result-v1.schema.json"
)

func data(name string) string { return filepath.Join(dataDir, name) }

// runQA runs the command with the given args plus --out in a temp dir and
// returns the exit code, stdout and the result path.
func runQA(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "result.json")
	var buf bytes.Buffer
	code := run(append(args, "--out", outPath), &buf)
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lines[len(lines)-1] != remarkLine {
		t.Fatalf("last line = %q, want the remark\n%s", lines[len(lines)-1], out)
	}
	return code, out, outPath
}

func k6(input string, extra ...string) []string {
	return append([]string{"--tool", "k6", "--tool-version", "0.0.0-test", "--input", data(input)}, extra...)
}

func readResult(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("result not written: %v", err)
	}
	if !bytes.HasSuffix(raw, []byte("\n")) || !bytes.Contains(raw, []byte("\n  \"schema\"")) {
		t.Fatalf("result is not 2-space indented with a trailing newline:\n%s", raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func requireNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("a result file was written at %s", path)
	}
}

func TestWithinBudgetOpenModelExitsZero(t *testing.T) {
	code, out, path := runQA(t, k6("k6-ok.json", "--open-model")...)
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "BUDGET qa.budget.latency_p99_ms limit 500 actual 210 ok\n") ||
		!strings.Contains(out, "verdict: pass\n") {
		t.Fatalf("stdout:\n%s", out)
	}
	if strings.Contains(out, "throughput_min_rps") {
		t.Fatalf("an unjudged budget was printed:\n%s", out)
	}
	res := readResult(t, path)
	if res["kind"] != "load" || res["verdict"] != "pass" {
		t.Fatalf("result = %v", res)
	}
	if _, has := res["fault"]; has {
		t.Fatal("a load result carries fault")
	}
	if _, has := res["processes"]; has {
		t.Fatal("processes must be absent")
	}
	lat := res["load"].(map[string]any)["latency_ms"].(map[string]any)
	if lat["p50"] != 42.5 {
		t.Fatalf("p50 should come from med: %v", lat)
	}
}

func TestSlowTargetBreachesP99(t *testing.T) {
	code, out, path := runQA(t, k6("k6-slow.json", "--open-model")...)
	if code != 1 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "BUDGET qa.budget.latency_p99_ms limit 500 actual 1800 BREACH\n") ||
		!strings.Contains(out, "verdict: breach\n") {
		t.Fatalf("stdout:\n%s", out)
	}
	if readResult(t, path)["verdict"] != "breach" {
		t.Fatal("result verdict is not breach")
	}
}

func TestFailingTargetBreachesErrorRate(t *testing.T) {
	code, out, _ := runQA(t, k6("k6-failing.json", "--open-model")...)
	if code != 1 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "BUDGET qa.budget.error_rate limit 0.01 actual 0.2 BREACH\n") {
		t.Fatalf("stdout:\n%s", out)
	}
}

func TestClosedModelWithDefaultBudgetsIsRefused(t *testing.T) {
	code, out, path := runQA(t, k6("k6-ok.json")...)
	if code != 2 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, refusedLine+"\n") {
		t.Fatalf("missing REFUSED line:\n%s", out)
	}
	requireNoFile(t, path)
}

func TestClosedModelWithoutLatencyBudgetsJudgesErrorRateOnly(t *testing.T) {
	code, out, path := runQA(t, k6("k6-ok.json", "--budgets", data("budgets-no-latency.yaml"))...)
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if strings.Contains(out, "latency") || !strings.Contains(out, "BUDGET qa.budget.error_rate limit 0.01 actual 0.001 ok\n") {
		t.Fatalf("stdout:\n%s", out)
	}
	load := readResult(t, path)["load"].(map[string]any)
	if load["open_model"] != false {
		t.Fatalf("open_model = %v", load["open_model"])
	}

	code, out, _ = runQA(t, k6("k6-failing.json", "--budgets", data("budgets-no-latency.yaml"))...)
	if code != 1 || !strings.Contains(out, "error_rate limit 0.01 actual 0.2 BREACH") {
		t.Fatalf("exit = %d\n%s", code, out)
	}
}

func TestFaultRunThatHolds(t *testing.T) {
	code, out, path := runQA(t, k6("k6-ok.json", "--open-model", "--fault", "latency 200ms on upstream")...)
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	res := readResult(t, path)
	fault := res["fault"].(map[string]any)
	if res["kind"] != "fault" || fault["pass"] != true || fault["injected"] != "latency 200ms on upstream" ||
		fault["steady_state"].(map[string]any)["budgets_held"] != true {
		t.Fatalf("result = %v", res)
	}
}

func TestFaultRunThatBreaches(t *testing.T) {
	code, out, path := runQA(t, k6("k6-failing.json", "--open-model", "--fault", "drop 20% of connections")...)
	if code != 1 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	fault := readResult(t, path)["fault"].(map[string]any)
	if fault["pass"] != false || fault["steady_state"].(map[string]any)["budgets_held"] != false {
		t.Fatalf("fault = %v", fault)
	}
}

func TestMissingP99NamesMetricAndStat(t *testing.T) {
	code, out, path := runQA(t, k6("k6-missing-p99.json", "--open-model")...)
	if code != 2 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if !strings.Contains(out, "http_req_duration") || !strings.Contains(out, "p(99)") {
		t.Fatalf("the missing metric and stat are not named:\n%s", out)
	}
	requireNoFile(t, path)
}

func TestTrailingDataIsAnInputError(t *testing.T) {
	code, out, path := runQA(t, k6("k6-trailing.json", "--open-model")...)
	if code != 2 || !strings.Contains(out, "data follows") {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	requireNoFile(t, path)
}

func TestBudgetsFileErrorsAreNamed(t *testing.T) {
	cases := []struct{ file, want string }{
		{"budgets-unknown-key.yaml", "qa.budget.latency_p77_ms"},
		{"budgets-negative.yaml", "qa.budget.error_rate"},
		{"budgets-malformed.yaml", "not YAML"},
	}
	for _, c := range cases {
		code, out, path := runQA(t, k6("k6-ok.json", "--open-model", "--budgets", data(c.file))...)
		if code != 2 || !strings.Contains(out, c.want) {
			t.Fatalf("%s: exit = %d, want 2 naming %q\n%s", c.file, code, c.want, out)
		}
		requireNoFile(t, path)
	}
}

func TestUnsupportedToolIsAUsageError(t *testing.T) {
	code, out, _ := runQA(t, "--tool", "jmeter", "--tool-version", "1", "--input", data("k6-ok.json"))
	if code != 2 || !strings.Contains(out, `"jmeter"`) {
		t.Fatalf("exit = %d\n%s", code, out)
	}
}

func TestWrittenResultsValidateAgainstSchema(t *testing.T) {
	schema, err := schemas.Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	runs := [][]string{
		k6("k6-ok.json", "--open-model"),
		k6("k6-slow.json", "--open-model"),
		k6("k6-ok.json", "--open-model", "--fault", "kill upstream"),
		k6("k6-failing.json", "--open-model", "--fault", "kill upstream"),
	}
	for _, args := range runs {
		_, out, path := runQA(t, args...)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(doc); err != nil {
			t.Fatalf("%v does not validate: %v\n%s", args, err, raw)
		}
	}
}

func TestSchemaRejectsMalformedResults(t *testing.T) {
	schema, err := schemas.Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, path := runQA(t, k6("k6-ok.json", "--open-model")...)
	base := readResult(t, path)

	mutate := func(f func(m map[string]any)) any {
		raw, _ := json.Marshal(base)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		f(m)
		return m
	}
	bad := map[string]any{
		"extra top-level key": mutate(func(m map[string]any) { m["unexpected"] = true }),
		"fault on a load": mutate(func(m map[string]any) {
			m["fault"] = map[string]any{"injected": "x", "steady_state": map[string]any{"budgets_held": true}, "pass": true}
		}),
		"fault kind no fault": mutate(func(m map[string]any) { m["kind"] = "fault" }),
		"extra load key":      mutate(func(m map[string]any) { m["load"].(map[string]any)["extra"] = 1 }),
		"error_rate over 1":   mutate(func(m map[string]any) { m["load"].(map[string]any)["error_rate"] = 1.5 }),
	}
	for name, doc := range bad {
		if err := schema.Validate(doc); err == nil {
			t.Fatalf("%s: validated, want a failure", name)
		}
	}
	if err := schema.Validate(mutate(func(m map[string]any) {})); err != nil {
		t.Fatalf("unmutated result must validate: %v", err)
	}
}

// C1: inputs outside the schema's ranges are refused with exit 2, naming the
// metric, before anything is written.

func TestErrorRateAboveOneIsRefused(t *testing.T) {
	code, out, path := runQA(t, k6("k6-error-rate-over-one.json", "--open-model")...)
	if code != 2 || !strings.Contains(out, "http_req_failed") {
		t.Fatalf("exit = %d, want 2 naming http_req_failed\n%s", code, out)
	}
	requireNoFile(t, path)
}

func TestNegativeThroughputIsRefused(t *testing.T) {
	code, out, path := runQA(t, k6("k6-negative-throughput.json", "--open-model")...)
	if code != 2 || !strings.Contains(out, "http_reqs") {
		t.Fatalf("exit = %d, want 2 naming http_reqs\n%s", code, out)
	}
	requireNoFile(t, path)
}

func TestNegativeP50IsRefused(t *testing.T) {
	code, out, path := runQA(t, k6("k6-negative-p50.json", "--open-model")...)
	if code != 2 || !strings.Contains(out, "http_req_duration") {
		t.Fatalf("exit = %d, want 2 naming http_req_duration\n%s", code, out)
	}
	requireNoFile(t, path)
}

func TestCountOverflowIsRefused(t *testing.T) {
	code, out, path := runQA(t, k6("k6-count-overflow.json", "--open-model")...)
	if code != 2 || !strings.Contains(out, "http_reqs") {
		t.Fatalf("exit = %d, want 2 naming http_reqs\n%s", code, out)
	}
	requireNoFile(t, path)
}

// K1: the boundary value itself (2^63) is refused, not wrapped to -2^63.
func TestCountEqualToTwoPow63IsRefused(t *testing.T) {
	code, out, path := runQA(t, k6("k6-count-maxint64-plus-one.json", "--open-model")...)
	if code != 2 || !strings.Contains(out, "http_reqs") {
		t.Fatalf("exit = %d, want 2 naming http_reqs\n%s", code, out)
	}
	requireNoFile(t, path)
}

// C2: the p95 and throughput breach checks and the zero-limit exclusion each
// have a test that fails when the check is removed.

func TestP95BreachIsJudged(t *testing.T) {
	code, out, path := runQA(t, k6("k6-p95-breach.json", "--open-model")...)
	if code != 1 || !strings.Contains(out, "BUDGET qa.budget.latency_p95_ms limit 300 actual 350 BREACH\n") ||
		!strings.Contains(out, "verdict: breach\n") {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if readResult(t, path)["verdict"] != "breach" {
		t.Fatal("result verdict is not breach")
	}
}

func TestThroughputBreachIsJudged(t *testing.T) {
	code, out, _ := runQA(t, k6("k6-ok.json", "--open-model", "--budgets", data("budgets-throughput.yaml"))...)
	if code != 1 || !strings.Contains(out, "BUDGET qa.budget.throughput_min_rps limit 200 actual 100.5 BREACH\n") ||
		!strings.Contains(out, "verdict: breach\n") {
		t.Fatalf("exit = %d\n%s", code, out)
	}
}

func TestZeroLimitBudgetIsNotJudged(t *testing.T) {
	code, out, path := runQA(t, k6("k6-failing.json", "--open-model", "--budgets", data("budgets-error-rate-zero.yaml"))...)
	if code != 0 || !strings.Contains(out, "verdict: pass\n") {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	if strings.Contains(out, "BUDGET qa.budget.error_rate") {
		t.Fatalf("an unjudged budget was judged:\n%s", out)
	}
	if readResult(t, path)["verdict"] != "pass" {
		t.Fatal("result verdict is not pass")
	}
}

// budgets: .inf and .nan limits are refused, naming the key.

func TestBudgetsRefuseNonFiniteLimits(t *testing.T) {
	cases := []struct{ file, key string }{
		{"budgets-inf.yaml", "qa.budget.latency_p99_ms"},
		{"budgets-nan.yaml", "qa.budget.error_rate"},
	}
	for _, c := range cases {
		code, out, path := runQA(t, k6("k6-ok.json", "--open-model", "--budgets", data(c.file))...)
		if code != 2 || !strings.Contains(out, c.key) {
			t.Fatalf("%s: exit = %d, want 2 naming %q\n%s", c.file, code, c.key, out)
		}
		requireNoFile(t, path)
	}
}

// M17: every test-data input runs through the command; whatever it writes
// validates against the schema, and nothing is written on refusal.

func TestEveryTestDataInputValidatesAgainstSchema(t *testing.T) {
	schema, err := schemas.Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	inputs := []string{
		"k6-ok.json",
		"k6-slow.json",
		"k6-failing.json",
		"k6-missing-p99.json",
		"k6-trailing.json",
		"k6-p95-breach.json",
		"k6-error-rate-over-one.json",
		"k6-negative-throughput.json",
		"k6-negative-p50.json",
		"k6-count-overflow.json",
		"k6-count-maxint64-plus-one.json",
	}
	for _, input := range inputs {
		code, out, path := runQA(t, k6(input, "--open-model")...)
		switch code {
		case 2:
			requireNoFile(t, path)
		case 0, 1:
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: exit %d but nothing was written: %v\n%s", input, code, err, out)
			}
			var doc any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("%s: result is not JSON: %v\n%s", input, err, out)
			}
			if err := schema.Validate(doc); err != nil {
				t.Fatalf("%s: exit %d, result does not validate: %v\n%s", input, code, err, raw)
			}
		default:
			t.Fatalf("%s: exit = %d\n%s", input, code, out)
		}
	}
}
