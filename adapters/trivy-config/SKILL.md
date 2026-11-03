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

The tool writes SARIF 2.1.0 to `report.sarif`. Each finding is a SARIF result: `ruleId` names the check (for example `DS-0002`, runs as root, or `DS-0026`, no HEALTHCHECK), `level` is the severity, `message` holds the description, and `locations` point at the configured file. Severity fields follow SARIF 2.1.0: `level` is one of `error`, `warning`, `note`, `none`.

The exit map is `{0: clean, 3: findings, default: tool-error}`: exit 0 means clean, 3 means findings, anything else — including the 2 a crashing Go program exits with and the 1 trivy returns on a bad flag or a missing path — maps to `tool-error`. No error uses the findings exit code.

## What to do with a delta

- Fix: remove the misconfiguration from the configuration file.
- Waive: accept a finding for a limited time in `.saphan-waivers.yaml`, naming the rule (`ruleId`) and one of the result's fingerprints, with an `expires` date; an expired waiver is reported and never silently applied. The scanned repository's own `trivy.yaml` and `.trivyignore` are not read, so neither file can waive or silence anything.
- Escalate: a finding on a configuration you did not expect to change, a result you cannot reproduce, or output that does not parse as SARIF 2.1.0 — raise it before merging.

Noise on a typical repository: rules for files that do not exist there — for example Kubernetes checks when there are no Kubernetes manifests — simply never fire; they are quiet, not a clean result. On `dockerfile-misconfigured` trivy reports the root user (`DS-0002`) and the missing HEALTHCHECK (`DS-0026`); it does not report the remote-URL `ADD`, although the fixture's README claims every scanner flags it. That gap is the master's measurement, not a defect of this record; do not edit the fixture to force a missing rule, report the gap instead.

Terraform: a correctly walled run does not fetch remote modules — nothing in the invocation asks trivy to resolve the Terraform registry, and the run is confined to `egress: none`. A module that cannot be fetched is reported as it is, not guessed at; the SARIF output shows what the pinned binary's embedded checks could evaluate.
