---
name: gitleaks
description: gitleaks: secret scanning at pinned release v8.30.1; scans a repository tree for hard-coded secrets, keys and tokens, and reports each match as a SARIF 2.1.0 result with ruleId, location and fingerprint. Use it when a change adds, moves or touches credentials and the pinned scan must run deterministically; reach for a repository-native scan (VCS push protection, a pre-commit hook) when the question is a single commit's diff, and for a dependency advisory scan when the question is vulnerable libraries rather than secrets at rest.
license: MIT
compatibility: the binary pinned by adapters/gitleaks/record.yaml (gitleaks, v8.30.1); installed by the operator; not present on every machine
metadata:
  saphan.origin: builtin
  saphan.default: "off"
  saphan.roles: "executor, review"
  saphan.instruments: "C5, C7"
  saphan.param.none: "the record carries no typed inputs beyond repo_root, config_path and output_path"
  saphan.conflicts: none
  saphan.upstream: zricethezav/gitleaks@v8.30.1 (MIT)
---

# gitleaks: secret scanning

## When to use it

The question it answers: does the repository tree contain a secret, and where? The tool scans file contents for hard-coded keys, tokens and passwords and reports each match as a SARIF 2.1.0 result: ruleId, location, severity and fingerprint.

Use it when:

- a change adds, rotates or moves credentials, keys or tokens;
- the change must show a deterministic, pinned scan of a repository or subtree;
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

The report carries the rule id, the file and the line of each match — never the secret's value. An agent works from rule id, file, line and fingerprint only. It never opens, prints or copies the matched value; looking at the value is a human's job. `--redact` is on the invocation so no secret value reaches the report kept as evidence.

Noise on a typical repository: strings that look like keys but are test fixtures, example values or public constants (for example the fixture's `TSFIXTURE-SYNTHETIC-…` token), and anything already rotated. A finding is noise only after a human decides it; the default decision is that a finding is real.

## What its exit codes mean

Exit codes name the program they belong to:

- gitleaks (the scanner): 0 means clean, 3 means findings, anything else — a crash, a bad flag, an unreadable `.gitleaks.toml` — is `tool-error`: a broken run of gitleaks, never read as clean and never read as findings.
- `tool-delta` (the delta): 0 means no `NEW` line was printed (nothing new), 1 means at least one `NEW` line, and 2 means a usage or input error. Exit 2 means the delta did not run; it is never "nothing new" — report it as an input error, and never read the delta's output lines as clean.

## What to do with a delta

Act on the delta (`tool-delta`, "The delta" in `spec/TOOL_RECORD_V1.md`), not on the raw SARIF report:

- `NEW <ruleId> <identity> <uri>:<startLine>` — a finding to fix, waive or escalate. Fix: a REAL secret is rotated first — revoke it at its issuer, and only then remove it from the files; removing it from the files does not remove it from history, so the history question goes to a human. Waive: add an entry to `.saphan-waivers.yaml` whose `fingerprint` is the identity the delta printed on that `NEW` line — the `partialFingerprints`/`fingerprints` value, or the derived `saphan/v1:` fingerprint when the result has neither — with `rule`, `reason` and `expires`; a waiver is for a false positive or a test fixture only, never an equal choice to fixing a live secret; an expired waiver is reported and never silently applied. Escalate: if the secret might be real, treat it as compromised: rotate it and tell the security contact; never log or commit the value.
- `WAIVED <ruleId> <identity> until <expires>` — nothing to do until its date.
- `EXPIRED <rule> <fingerprint> <reason> expired <expires>` — the waiver lapsed: the finding counts again, so fix it or renew the waiver with a reason.
- `tool-delta` exits 0 when no `NEW` line was printed (nothing new), 1 when at least one `NEW` line was printed, and 2 on a usage or input error; exit 2 means the delta did not run and is never "nothing new". A run mapping to `clean` needs no action; `tool-error` means the record, the binary, or the scanned repository's own `.gitleaks.toml` — missing or malformed — is broken.

## Repository policy files

A change that adds or edits `.gitleaks.toml` or `.gitleaksignore` is itself escalated to a human, never accepted by an agent — the repository's own scanner policy stays as the customer wrote it (the gate's fixture depends on it). A waiver goes through `.saphan-waivers.yaml`, which carries `expires`; an inline `gitleaks:allow` comment carries no such date and does not silence the scan. An allowlist in the repository's `.gitleaks.toml` silences findings. A `.gitleaksignore` entry silences a finding only if it names the exact absolute path the scan sees: entries written from a local scan of `.` do not match, and a fresh copy never matches. Either way, a change that adds or edits either file is escalated to a human.
