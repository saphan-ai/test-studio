// Command qa-normalize turns a k6 --summary-export into one normalized qa
// result (qa-result/v1), judged against configured budgets.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	exitOK     = 0
	exitBreach = 1
	exitUsage  = 2

	usageLine   = "usage: qa-normalize --tool k6 --input <summary.json> --tool-version <v> [--open-model] [--budgets <yaml>] [--fault <description>] --out <result.json>"
	remarkLine  = "remark: percentiles are from k6's summary; they are coordinated-omission-safe only for an open-model run."
	refusedLine = "REFUSED latency budgets need an open-model run (k6 arrival-rate executor); pass --open-model only when the run used one"
)

// budgetCfg lists the configured budgets in the order they are reported:
// key, default value, and whether judging it requires an open-model run.
// A value of 0 means not judged.
var budgetCfg = []struct {
	key     string
	limit   float64
	latency bool
}{
	{key: "qa.budget.latency_p99_ms", limit: 500, latency: true},
	{key: "qa.budget.latency_p95_ms", limit: 300, latency: true},
	{key: "qa.budget.error_rate", limit: 0.01},
	{key: "qa.budget.throughput_min_rps", limit: 0},
}

type options struct {
	tool        string
	input       string
	toolVersion string
	openModel   bool
	budgets     string
	fault       string
	out         string
}

type budgetLimit struct {
	key     string
	limit   float64
	judged  bool // limit > 0
	latency bool
}

type result struct {
	Schema  string      `json:"schema"`
	Tool    toolRef     `json:"tool"`
	Kind    string      `json:"kind"`
	Load    loadBlock   `json:"load"`
	Fault   *faultBlock `json:"fault,omitempty"`
	Budgets []budgetOut `json:"budgets"`
	Verdict string      `json:"verdict"`
}

type toolRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type loadBlock struct {
	Requests      int64     `json:"requests"`
	ThroughputRPS float64   `json:"throughput_rps"`
	ErrorRate     float64   `json:"error_rate"`
	LatencyMS     latencyMs `json:"latency_ms"`
	OpenModel     bool      `json:"open_model"`
}

type latencyMs struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

type faultBlock struct {
	Injected    string      `json:"injected"`
	SteadyState steadyState `json:"steady_state"`
	Pass        bool        `json:"pass"`
}

type steadyState struct {
	BudgetsHeld bool `json:"budgets_held"`
}

type budgetOut struct {
	Key      string  `json:"key"`
	Limit    float64 `json:"limit"`
	Actual   float64 `json:"actual"`
	Breached bool    `json:"breached"`
}

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

// run returns 0 on pass, 1 on breach, 2 on usage or input error. The remark
// is the last line on every path.
func run(args []string, out io.Writer) int {
	opts, err := parseArgs(args, out)
	if err != nil {
		return finish(out, err, true)
	}

	raw, err := os.ReadFile(opts.input)
	if err != nil {
		return finish(out, fmt.Errorf("cannot read %s: %w", opts.input, err), false)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var summary any
	if err := dec.Decode(&summary); err != nil {
		return finish(out, fmt.Errorf("input %s is not JSON: %w", opts.input, err), false)
	}
	if dec.More() {
		return finish(out, fmt.Errorf("input %s: data follows the first JSON value", opts.input), false)
	}

	limits, err := loadBudgets(opts.budgets)
	if err != nil {
		return finish(out, err, false)
	}

	p50, p95, p99, requests, rate, failed, err := measure(summary)
	if err != nil {
		return finish(out, err, false)
	}

	for _, b := range limits {
		if b.latency && b.judged && !opts.openModel {
			return finish(out, errors.New(refusedLine), false)
		}
	}

	verdict := "pass"
	anyBreached := false
	budgets := make([]budgetOut, 0, len(limits))
	for _, b := range limits {
		var actual float64
		breached := false
		switch b.key {
		case "qa.budget.latency_p99_ms":
			actual = p99
			breached = b.judged && actual > b.limit
		case "qa.budget.latency_p95_ms":
			actual = p95
			breached = b.judged && actual > b.limit
		case "qa.budget.error_rate":
			actual = failed
			breached = b.judged && actual > b.limit
		case "qa.budget.throughput_min_rps":
			actual = rate
			breached = b.judged && actual < b.limit
		}
		if breached {
			anyBreached = true
			verdict = "breach"
		}
		ok := "ok"
		if breached {
			ok = "BREACH"
		}
		if b.judged {
			fmt.Fprintf(out, "BUDGET %s limit %s actual %s %s\n", b.key, f(b.limit), f(actual), ok)
		}
		budgets = append(budgets, budgetOut{Key: b.key, Limit: b.limit, Actual: actual, Breached: breached})
	}
	fmt.Fprintln(out, "verdict: "+verdict)

	res := result{
		Schema: "qa-result/v1",
		Tool:   toolRef{ID: opts.tool, Version: opts.toolVersion},
		Kind:   "load",
		Load: loadBlock{
			Requests:      requests,
			ThroughputRPS: rate,
			ErrorRate:     failed,
			LatencyMS:     latencyMs{P50: p50, P95: p95, P99: p99},
			OpenModel:     opts.openModel,
		},
		Budgets: budgets,
		Verdict: verdict,
	}
	if opts.fault != "" {
		res.Kind = "fault"
		res.Fault = &faultBlock{
			Injected:    opts.fault,
			SteadyState: steadyState{BudgetsHeld: !anyBreached},
			Pass:        !anyBreached,
		}
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return finish(out, fmt.Errorf("cannot marshal result: %w", err), false)
	}
	data = append(data, '\n')
	if err := os.WriteFile(opts.out, data, 0o644); err != nil {
		return finish(out, fmt.Errorf("cannot write %s: %w", opts.out, err), false)
	}

	fmt.Fprintln(out, remarkLine)
	if verdict == "breach" {
		return exitBreach
	}
	return exitOK
}

func finish(out io.Writer, err error, printUsage bool) int {
	if printUsage {
		fmt.Fprintln(out, usageLine)
	}
	if err != nil {
		fmt.Fprintln(out, err.Error())
	}
	fmt.Fprintln(out, remarkLine)
	return exitUsage
}

func parseArgs(args []string, out io.Writer) (options, error) {
	fs := flag.NewFlagSet("qa-normalize", flag.ContinueOnError)
	fs.SetOutput(out)
	var o options
	fs.StringVar(&o.tool, "tool", "", "tool that produced the summary; k6 is the only accepted value")
	fs.StringVar(&o.input, "input", "", "k6 --summary-export JSON document")
	fs.StringVar(&o.toolVersion, "tool-version", "", "version of the tool that produced the summary")
	fs.BoolVar(&o.openModel, "open-model", false, "the run used an open-model executor (k6 arrival-rate)")
	fs.StringVar(&o.budgets, "budgets", "", "flat YAML budgets file overriding qa.budget.* defaults")
	fs.StringVar(&o.fault, "fault", "", "fault description; makes the result kind fault")
	fs.StringVar(&o.out, "out", "", "path to write the normalized result JSON")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, errors.New("unexpected argument: " + fs.Arg(0))
	}
	if o.tool == "" {
		return o, errors.New("missing --tool k6")
	}
	if o.tool != "k6" {
		return o, fmt.Errorf("--tool %q is not supported; only k6 is accepted", o.tool)
	}
	if o.input == "" {
		return o, errors.New("missing --input <summary.json>")
	}
	if o.toolVersion == "" {
		return o, errors.New("missing --tool-version <v>")
	}
	if o.out == "" {
		return o, errors.New("missing --out <result.json>")
	}
	return o, nil
}

// loadBudgets starts from the defaults and applies the flat YAML file, if any.
func loadBudgets(path string) ([]budgetLimit, error) {
	known := map[string]bool{}
	for _, b := range budgetCfg {
		known[b.key] = true
	}
	limits := make([]budgetLimit, len(budgetCfg))
	for i, b := range budgetCfg {
		limits[i] = budgetLimit{key: b.key, limit: b.limit, judged: b.limit > 0, latency: b.latency}
	}
	if path == "" {
		return limits, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read budgets %s: %w", path, err)
	}
	var declared map[string]any
	if err := yaml.Unmarshal(raw, &declared); err != nil {
		return nil, fmt.Errorf("budgets %s is not YAML: %w", path, err)
	}
	for key, val := range declared {
		if !known[key] {
			return nil, fmt.Errorf("unknown budgets key %q", key)
		}
		limit, ok := toFloat(val)
		if !ok {
			return nil, fmt.Errorf("budgets key %q is not a number", key)
		}
		if math.IsNaN(limit) || math.IsInf(limit, 0) {
			return nil, fmt.Errorf("budgets key %q is not a finite number (%v)", key, limit)
		}
		if limit < 0 {
			return nil, fmt.Errorf("negative value for budgets key %q", key)
		}
		for i := range limits {
			if limits[i].key == key {
				limits[i].limit = limit
				limits[i].judged = limit > 0
			}
		}
	}
	return limits, nil
}

// measure pulls the k6 summary fields the result is built from and names any
// missing metric or stat. Every number is refused when it falls outside the
// range the result document's schema promises, naming the metric and the stat.
func measure(summary any) (p50, p95, p99 float64, requests int64, rate, failed float64, err error) {
	metrics, ok := summary.(map[string]any)
	if !ok {
		err = errors.New("input JSON is not an object")
		return
	}
	m, ok := metrics["metrics"].(map[string]any)
	if !ok {
		err = errors.New("missing metric metrics")
		return
	}

	dur, ok := m["http_req_duration"].(map[string]any)
	if !ok {
		err = errors.New("missing metric http_req_duration")
		return
	}
	values := dur
	p50f, err := stat(values, []string{"p(50)", "med"})
	if err != nil {
		err = fmt.Errorf("missing metric http_req_duration: %w", err)
		return
	}
	if err := inRange("http_req_duration", "p(50)", p50f); err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}
	p95f, err := stat(values, []string{"p(95)"})
	if err != nil {
		err = fmt.Errorf("missing metric http_req_duration: %w", err)
		return
	}
	if err := inRange("http_req_duration", "p(95)", p95f); err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}
	p99f, err := stat(values, []string{"p(99)"})
	if err != nil {
		err = fmt.Errorf("missing metric http_req_duration: %w", err)
		return
	}
	if err := inRange("http_req_duration", "p(99)", p99f); err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}

	reqs, ok := m["http_reqs"].(map[string]any)
	if !ok {
		err = errors.New("missing metric http_reqs")
		return
	}
	reqsValues := reqs
	count, ok := toFloat(reqsValues["count"])
	if !ok {
		err = errors.New("missing metric http_reqs: missing stat count")
		return
	}
	if err := requestCountInRange(count); err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}
	ratef, ok := toFloat(reqsValues["rate"])
	if !ok {
		err = errors.New("missing metric http_reqs: missing stat rate")
		return
	}
	if err := inRange("http_reqs", "rate", ratef); err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}

	failedMeter, ok := m["http_req_failed"].(map[string]any)
	if !ok {
		err = errors.New("missing metric http_req_failed")
		return
	}
	failedValues := failedMeter
	failedf, ok := toFloat(failedValues["value"])
	if !ok {
		err = errors.New("missing metric http_req_failed: missing stat value")
		return
	}
	if err := errorRateInRange(failedf); err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}

	return p50f, p95f, p99f, int64(count), ratef, failedf, nil
}

// inRange refuses a k6 stat below the schema range (>= 0), naming the metric
// and the stat.
func inRange(metric, stat string, value float64) error {
	if value < 0 {
		return fmt.Errorf("%s: stat %s = %g: outside schema range (>= 0)", metric, stat, value)
	}
	return nil
}

// requestCountInRange refuses a request count outside the schema range: an
// integer from 0 to math.MaxInt64. A count like 1e300 overflows int64 when
// converted blindly, so it is refused before the conversion.
func requestCountInRange(count float64) error {
	if count < 0 || count >= float64(math.MaxInt64) || count != math.Trunc(count) {
		return fmt.Errorf("http_reqs: stat count = %g: outside schema range (integer 0..%d)", count, math.MaxInt64)
	}
	return nil
}

// errorRateInRange refuses a failed fraction outside the schema range 0..1.
func errorRateInRange(value float64) error {
	if value < 0 || value > 1 {
		return fmt.Errorf("http_req_failed: stat value = %g: outside schema range (0..1)", value)
	}
	return nil
}

// stat returns the first present stat under any of the names, or an error
// naming the stats that were looked for.
func stat(values map[string]any, names []string) (float64, error) {
	for _, name := range names {
		if v, ok := values[name]; ok {
			f, ok := toFloat(v)
			if !ok {
				return 0, fmt.Errorf("stat %s is not a number", name)
			}
			return f, nil
		}
	}
	return 0, errors.New("missing stat " + strings.Join(names, ", "))
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func f(x float64) string {
	return strconv.FormatFloat(x, 'f', -1, 64)
}
