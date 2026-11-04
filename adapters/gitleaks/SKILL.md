---
name: gitleaks
description: gitleaks: secret scanning at pinned release v8.30.1; scans a repository tree for hard-coded secrets, keys and tokens, and reports each match as a SARIF 2.1.0 result with ruleId, location and fingerprint. Use it when a change adds, moves or touches credentials and the ASK library must run the pinned scan deterministically; reach for a repository-native scan (VCS push protection, a pre-commit hook) when the question is a single commit's diff, and for a dependency advisory scan when the question is vulnerable libraries rather than secrets at rest.
license: MIT
compatibility: the binary pinned by adapters/gitleaks/record.yaml (gitleaks, v8.30.1); staged by the master's gate and not present on every seat
metadata:
  saphan.origin: builtin
  saphan.default: "off"
  saphan.roles: "executor, review"
  saphan.instruments: "C5, C7"
  saphan.param.none: "the record carries no typed inputs beyond repo_root, config_path and output_path"
  saphan.conflicts: none
  saphan.upstream: zricethezav/gitleaks@v8.30.1 (MIT)
---

# gitleaks: secret scanning in the ASK library

## When to use it

The question it answers: does the repository tree contain a secret, and where? The tool scans file contents for hard-coded keys, tokens and passwords and reports each match as a SARIF 2.1.0 result: ruleId, location, severity and fingerprint.

Use it when:

- a change adds, rotates or moves credentials, keys or tokens;
- the ASK library must show a deterministic, pinned scan of a repository or subtree;
- a finding reported elsewhere needs confirmation under the same pinned configuration.

Do not use it for:

- vulnerable dependencies — that is a dependency advisory scan;
- a single commit's diff before push — that is the VCS provider's push protection or a pre-commit hook;
- lint, type or style problems.

## How to run it

Only through `saphan ask tool gitleaks`, which prints the pinned invocation (the argv from adapters/gitleaks/record.yaml: the program, the flag for the config file, the SARIF report format, the report path report.sarif and the repository root). NEVER a copied command line. Until `saphan ask tool` exists, that line stands as written and nothing replaces it. Do not type the invocation by hand: flags drift between releases, and the record is what the gate checks.

The scan reads `.gitleaks.toml` at the repository root (the record's `config`) and writes `report.sarif`. It runs with `--ignore-gitleaks-allow`, so inline `gitleaks:allow` comments in the scanned code do not silence the scan. egress: none — nothing leaves the machine.

## What its output means

SARIF 2.1.0: a document whose `version` is exactly `2.1.0` with a non-empty `runs` array. Each run holds `results`; each result names:

- `ruleId` — the rule that matched (the fixture's rule is `synthetic-fixture-token`);
- `level` — the finding-class severity field (`error`, `warning`, `note`, `none`; matches are reported at `error`);
- `locations` — the file and region where the secret appears;
- `partialFingerprints` / `fingerprints` — the finding's identity, the value `.saphan-waivers.yaml` matches on.

The report carries the rule id, the file and the line of each match — never the secret's value. An agent works from rule id, file, line and fingerprint only. It never opens, prints or copies the matched value; looking at the value is a human's job. `--redact` is on the invocation so no secret value reaches the report the fleet stores as evidence.

Noise on a typical repository: strings that look like keys but are test fixtures, example values or public constants (for example the fixture's `TSFIXTURE-SYNTHETIC-…` token), and anything already rotated. A finding is noise only after a human decides it; the default decision is that a finding is real.

## What its exit codes mean

The record maps exit code 0 to `clean`, exit code 3 to `findings`, and anything else to `tool-error`. A crash of the scanner is therefore a broken run: it is reported as `tool-error`, never read as clean and never read as findings. `tool-error` means the record, the binary, or the scanned repository's own `.gitleaks.toml` (a malformed config) is broken.

## What to do with a delta

A run whose exit code maps to `findings`:

1. fix — a REAL secret is rotated first: revoke it at its issuer, and only then remove it from the files. Removing it from the files does not remove it from history, so the history question goes to a human.
2. waive — a waiver is for a false positive or a test fixture only; it is never an equal choice to fixing a live secret. Add an entry to `.saphan-waivers.yaml` with `rule`, `fingerprint`, `reason` and an `expires` date (the spec's key); an expired waiver is reported and never silently applied.
3. escalate — if the secret might be real, treat it as compromised: rotate it and tell the security contact; never log or commit the value.

A run mapping to `clean` needs no action; `tool-error` means the record, the binary, or the scanned repository's own `.gitleaks.toml` (a malformed config) is broken.

## Repository policy files

A change that adds or edits `.gitleaks.toml` or `.gitleaksignore` is itself escalated to a human, never accepted by an agent — the repository's own scanner policy stays as the customer wrote it (the gate's fixture depends on it). A waiver goes through `.saphan-waivers.yaml`, which carries `expires`; an inline `gitleaks:allow` comment carries no such date and does not silence the scan. The repository's `.gitleaksignore` and any allowlist in its `.gitleaks.toml` still silence findings; that is why a change that adds or edits either file is escalated to a human.
