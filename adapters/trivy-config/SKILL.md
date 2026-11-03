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

The pinned invocation runs `trivy config` with `--skip-check-update` (no network: the checks are embedded in the binary, so an ERROR line "Falling back to embedded checks" is expected and harmless), `--format sarif`, `--output report.sarif`, `--exit-code 2`, and the repository root as the directory to scan. `--exit-code 2` moves the findings exit code to a value no error uses: trivy returns 1 on bad arguments and on a missing path, so exit 2 means findings and anything else is a tool error.

## What its output means

The tool writes SARIF 2.1.0 to `report.sarif`. Each finding is a SARIF result: `ruleId` names the check (for example `DS-0002`, runs as root, or `DS-0026`, no HEALTHCHECK), `level` is the severity, `message` holds the description, and `locations` point at the configured file. Severity fields follow SARIF 2.1.0: `level` is one of `error`, `warning`, `note`, `none`.

Noise on a typical repository: rules for technologies you do not use (for example Kubernetes checks on a repository with no Kubernetes manifests), and results the fixture's README promises but the embedded checks at this tag do not raise — on `dockerfile-misconfigured` trivy reports the root user and the missing HEALTHCHECK, but not the remote-URL `ADD`, although the README claims every scanner flags it. That gap is the master's measurement, not a defect of this record; do not edit the fixture to force a missing rule, report the gap instead.

## What to do with a delta

- Fix: remove the misconfiguration from the configuration file.
- Waive: accept a finding for a limited time in `.saphan-waivers.yaml`, naming the rule (`ruleId`) and one of the result's fingerprints, with an `expires` date; an expired waiver is reported and never silently applied.
- Escalate: a finding on a configuration you did not expect to change, a result you cannot reproduce, or output that does not parse as SARIF 2.1.0 — raise it before merging.

The exit map is `{0: clean, 2: findings, default: tool-error}`: exit 0 means clean, 2 means findings, anything else — including the 1 trivy returns on a bad flag value or a missing path — maps to tool-error.
