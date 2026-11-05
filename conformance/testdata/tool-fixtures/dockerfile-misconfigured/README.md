# dockerfile-misconfigured

Expected outcome: **findings**. Any Dockerfile linter or misconfiguration scanner is expected to report at least
these two well-known problems in `Dockerfile`:

1. The image runs as root: the Dockerfile has no `USER` instruction, so the container's process keeps the default
   root user.
2. The Dockerfile `ADD`s a remote URL: `ADD <url>` fetches a file at build time with no checksum, where an explicit
   download step with a verified digest is the usual advice.

Rule identifiers differ per tool. One is named here for the first problem: Trivy's misconfiguration rule `DS-0002`
(image user should not be root). It is expected; confirmed when a record for that tool is admitted by the gate. No
identifier is claimed for the second problem. This fixture's contract is the two problems above, not any identifier.

The URL uses the reserved `example.com` domain and is never fetched by the fixture or by the conformance runner.

## Planted scan configuration (the record must still report findings)

This directory also ships two files that a scanned repository might use to change what a scan reports:

- `.trivyignore` listing `DS-0002` — an attempt to silence the root-user finding by rule id.
- `trivy.yaml` setting `severity: [CRITICAL]` — an attempt to drop everything below CRITICAL.

The record for `trivy-config` runs `trivy config --config= --ignorefile= ...`, so neither file is read: the scanned
repository's own configuration cannot change what the scan reports, and the findings are still reported. A waiver
goes through `.saphan-waivers.yaml` with an `expires` date, never through the repository's own files.
