# QA test result, version 1

Status: version 1. Schema identifier: `qa-result/v1`. Machine form: `spec/schemas/qa-result-v1.schema.json`.
Checker: `conformance/cmd/qa-normalize/`.

A **qa result** says, in one small file, how one load or fault test run went, judged against named budgets whose defaults are in the config. It is the single format the tester and the console read the same way: a slow or failing target turns the result red, with no model in the numbers.

The record is data. It carries no script, no shell line and no logic: the tool is named and versioned, the kind is declared, and every number is measured by the tool that produced it.

## The result

A key that is not listed here is refused. The document is exactly one JSON value.

| key | required | meaning |
|---|---|---|
| `schema` | yes | the literal `qa-result/v1` |
| `tool` | yes | `{id, version}`: the tool that produced the result and its version |
| `kind` | yes | `load` or `fault` |
| `load` | yes | the load measurement, always present |
| `fault` | only when `kind` is `fault` | the fault measurement; absent when `kind` is `load` |
| `processes` | no | resource samples; filled by a later sampler, this command leaves it absent |
| `budgets` | yes | array of `{key, limit, actual, breached}`, one entry per configured budget |
| `verdict` | yes | `pass` or `breach`: `breach` when any judged budget is breached |

### `tool`

`{id: string, version: string}`. For k6 output: `{id: "k6", version: <the k6 version>}`.

### `load`

`{requests int>=0, throughput_rps >=0, error_rate 0..1, latency_ms {p50,p95,p99}, open_model bool}`.

- `requests`: total request count (k6 `http_reqs.count`).
- `throughput_rps`: request rate (k6 `http_reqs.rate`).
- `error_rate`: failed fraction (k6 `http_req_failed`).
- `latency_ms`: `{p50, p95, p99}` in milliseconds, from `http_req_duration` (`p(50)`, or `med` when `p(50)` is absent, then `p(95)` and `p(99)`).
- `open_model`: whether the run used an open-model executor (k6 `constant-arrival-rate` or `ramping-arrival-rate`). Declared by the caller with `--open-model`; never guessed.

### `fault`

`{injected string, steady_state {budgets_held bool}, pass bool}`.

- `injected`: the fault description, as declared by the caller (`--fault`).
- `steady_state.budgets_held`: true when no judged budget was breached.
- `pass`: equal to `steady_state.budgets_held`.

### `processes`

Optional array of `{name string, cpu_seconds number>=0, max_rss_bytes integer>=0}`. Documented here because a later sampler fills it; `qa-normalize` leaves it absent.

### `budgets`

Array of `{key string, limit number, actual number, breached boolean}`, one entry per configured budget, in config order.

### `verdict`

`pass` when no judged budget is breached, `breach` otherwise. For a fault run the verdict follows the same rule, and `fault.pass` = `steady_state.budgets_held` = (verdict is `pass`).

## Coordinated omission

Percentiles from a summary are trustworthy only from an **open-model** run (k6 `constant-arrival-rate` / `ramping-arrival-rate`). From a closed-model run (k6 the default `per-vu-iteration` / `shared-iterations` executors) a slow target hides behind coordinated omission: the percentiles look fine because the slow requests never happened.

`open_model` is declared by the caller and never guessed by this command. A latency budget judged without `--open-model` is a named refusal: `REFUSED latency budgets need an open-model run (k6 arrival-rate executor); pass --open-model only when the run used one`.

## Budgets

Config keys with defaults, overridable by a flat YAML budgets file (one mapping, keys `qa.budget.<name>`):

| key | default | judged when |
|---|---|---|
| `qa.budget.latency_p99_ms` | 500 | limit > 0 |
| `qa.budget.latency_p95_ms` | 300 | limit > 0 |
| `qa.budget.error_rate` | 0.01 | limit > 0 |
| `qa.budget.throughput_min_rps` | 0 | limit > 0 |

A value of 0 means not judged: such an entry is written as `limit: 0` with its measured `actual` and `breached: false`, contributes nothing to the verdict, and is not printed on stdout. There is no separate `judged` field in the document or the schema: judged-ness is `limit > 0`, so a reader derives it from the document and this section rather than from an extra key.

An unknown key is refused, naming it. A negative value is refused, naming it. A non-finite value (`.inf` or `.nan`) is refused, naming it.

**Breach:** `actual > limit`; for throughput, `limit > 0` and `actual < limit`. `breached` is recorded per budget; the verdict is `breach` if any budget is breached.

## The k6 input

`--input` is a k6 `--summary-export` JSON document, produced with `--summary-trend-stats=med,p(95),p(99)`. It reads:

- `metrics.http_req_duration`: `p(50)` (or `med` when `p(50)` is absent), `p(95)`, `p(99)`;
- `metrics.http_reqs`: `count` and `rate`;
- `metrics.http_req_failed`: `value` (the failed fraction).

A missing metric or stat is an input error, naming the metric and the stat. Malformed JSON, malformed YAML budgets, and trailing data after the first JSON value are input errors. The k6 field names are as k6 documents them and are unmeasured until the first pinned k6 run.

## Stdout

One `BUDGET <key> limit <l> actual <a> ok|BREACH` line per judged budget, then `verdict: pass|breach`, then on every path the last line:

`remark: percentiles are from k6's summary; they are coordinated-omission-safe only for an open-model run.`

Numbers are printed with `strconv.FormatFloat(x, 'f', -1, 64)`.
