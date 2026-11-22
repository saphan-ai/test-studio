---
name: trivy-config
description: Run trivy v0.76.1's `config` scanner (misconfigurations only) over a repository and report findings as SARIF 2.1.0, pinned by record adapters/trivy-config/record.yaml.
---

# trivy-config: trivy configuration misconfiguration scanner

## When to use it

The question it answers: "does this repository contain configuration misconfigurations?" Trivy's `config` scanner at the pinned release v0.76.1 reads configuration files — Dockerfiles, Kubernetes manifests, Helm charts, Terraform, CloudFormation, Azure ARM, Ansible and more — and reports misconfigurations such as a container that runs as root or an image without a `HEALTHCHECK`.

Use it for configuration files in any repository (ecosystem: any). It reports misconfigurations only: it reads no vulnerability database, so it is not the tool for vulnerable dependencies (use a dependency audit) and not for secrets in source (use a secret scanner such as gitleaks).

## How to run it

Run only through `saphan ask tool trivy-config`, which prints the pinned invocation for this adapter. Never a copied command line: do not paste `trivy config ...` into a terminal, do not retype its flags, and do not drift from `adapters/trivy-config/record.yaml`. Until `saphan ask tool` exists, the line stands as written and nothing replaces it.

The pinned invocation is `trivy config --config= --ignorefile= --cache-dir <fresh per-run cache> --skip-check-update --disable-telemetry --skip-version-check --format sarif --output report.sarif --exit-code 3 <repo_root>`:

- `--config=` and `--ignorefile=` (empty values) disable loading of any `trivy.yaml` or `.trivyignore` the scanned repository ships, so the repository cannot change severity, skip directories or drop rules. `--skip-check-update --disable-telemetry --skip-version-check` stop the checks-bundle download, the start-up update check and telemetry.
- `--cache-dir` points at `.saphan-tool-cache/trivy` inside the fresh copy the runner scans, so the check bundle is never taken from the host's `~/.cache/trivy`: the checks that run are the pinned binary's embedded ones, every run.
- `--exit-code 3` means trivy exits 3 when it finds misconfigurations; exit 0 means clean; anything else — including a Go crash, which exits 2 — is a broken run (`tool-error`), never findings. That is why findings are not mapped to exit 2.

## What its output means

The tool writes SARIF 2.1.0 to `report.sarif`. Each finding is a SARIF result: `ruleId` names the check (for example `DS-0002`, runs as root, or `DS-0026`, no HEALTHCHECK), `level` is the severity, and `locations` point at the configured file. Severity fields follow SARIF 2.1.0: `level` is one of `error`, `warning`, `note`, `none`.

Exit codes name the program they belong to:

- trivy (the scanner): 0 means clean, 3 means findings, anything else — a bad flag, a missing path, a crash (Go exits 2 on a crash) — is `tool-error`: a broken run of trivy, never findings. No error uses the findings exit code.
- `tool-delta` (the delta): 0 means no `NEW` line was printed (nothing new), 1 means at least one `NEW` line, and 2 means a usage or input error. Exit 2 means the delta did not run; it is never "nothing new" — report it as an input error, and never read the delta's output lines as clean.

## What to do with a delta

Act on the delta (`tool-delta`, "The delta" in `spec/TOOL_RECORD_V1.md`), not on the raw SARIF report:

- `NEW <ruleId> <identity> <uri>:<startLine>` — a finding to fix, waive (where this skill allows it) or escalate. Fix: remove the misconfiguration from the configuration file. Waive: add an entry to `.saphan-waivers.yaml` whose `fingerprint` is the identity the delta printed on that `NEW` line — the result's `partialFingerprints` or `fingerprints` value, or the derived fingerprint when the result has neither — with `rule`, `reason` and an `expires` date. A waiver is legitimate only for a false positive or for a configuration that is deliberate and reviewed; it is never a way to accept a real misconfiguration without fixing it. Waivers expire, and an expired waiver is reported and never silently applied. Escalate: a finding on a configuration you did not expect to change, a result you cannot reproduce, or output that does not parse as SARIF 2.1.0 — raise it before merging.
- `WAIVED <ruleId> <identity> until <expires>` — the finding is covered by an unexpired waiver; nothing to do until its date.
- `EXPIRED <rule> <fingerprint> <reason> expired <expires>` — the waiver lapsed: the finding counts again, so fix it or renew the waiver with a reason.
- `tool-delta` exits 0 when no `NEW` line was printed (nothing new), 1 when at least one `NEW` line was printed, and 2 on a usage or input error; exit 2 means the delta did not run and is never "nothing new".

Do not use the scanned repository's own `trivy.yaml` or `.trivyignore`: the record switches them off with `--config=` and `--ignorefile=` (empty values). Never print, quote or ask for a finding's message.

Noise on a typical repository: rules for files that do not exist there — for example Kubernetes checks when there are no Kubernetes manifests — simply never fire; they are quiet, not a clean result. On `dockerfile-misconfigured` trivy reports the root user (`DS-0002`) and the missing HEALTHCHECK (`DS-0026`); it does not report the remote-URL `ADD`, although the fixture's README claims every scanner flags it. That gap is a measurement, not a defect of this record; do not edit the fixture to force a missing rule, report the gap instead.

Terraform: a correctly walled run does not fetch remote modules — nothing in the invocation asks trivy to resolve the Terraform registry, and the run is confined to `egress: none`. A module that cannot be fetched is reported as it is, not guessed at; the SARIF output shows what the pinned binary's embedded checks could evaluate.
