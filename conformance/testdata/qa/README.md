# QA normalizer test data

Hand-written k6 `--summary-export` documents in the structure k6 documents, and budgets files, for
`conformance/cmd/qa-normalize/`. The k6 field names are unmeasured until the first pinned k6 run.

| file | used with | expected outcome |
|---|---|---|
| `k6-ok.json` | `--open-model`, default budgets | exit 0, verdict pass |
| `k6-ok.json` | `--open-model --fault <text>` | exit 0, `fault.pass` true |
| `k6-slow.json` | `--open-model`, default budgets | exit 1, `latency_p99_ms` BREACH (p99 1800 > 500) |
| `k6-failing.json` | `--open-model`, default budgets | exit 1, `error_rate` BREACH (0.2 > 0.01) |
| `k6-failing.json` | `--open-model --fault <text>` | exit 1, `fault.pass` false |
| `k6-ok.json` | closed model, default budgets | exit 2, REFUSED, no file written |
| `k6-failing.json` + `budgets-no-latency.yaml` | closed model | exit 1, judged on error rate only |
| `k6-ok.json` + `budgets-no-latency.yaml` | closed model | exit 0, judged on error rate only |
| `k6-missing-p99.json` | `--open-model` | exit 2, names `http_req_duration` and `p(99)` |
| `k6-trailing.json` | `--open-model` | exit 2, data follows the JSON value |
| `budgets-unknown-key.yaml` | any | exit 2, names `qa.budget.latency_p77_ms` |
| `budgets-negative.yaml` | any | exit 2, names `qa.budget.error_rate` |
| `budgets-malformed.yaml` | any | exit 2, YAML error |
