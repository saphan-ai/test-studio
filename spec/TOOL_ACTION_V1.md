# Catalogue action, version 1

Status: version 1. Validator: `actions/actionspec/`, run by `actions/cmd/validate-action/`. Schema vendored from
SchemaStore: `actions/actionspec/github-action.schema.json`, with its source and digest in the sidecar
`actions/actionspec/github-action.schema.json.source.yaml`.

A **catalogue action** is one pipeline check (a build, a vet, a test run) described in the syntax GitHub Actions uses
for action metadata, so that an ordinary workflow tool can read it, and run by a pinned runner that writes JUnit.
This document is the contract: what a catalogue action is, and what is refused. It is the catalogue side only.

remark: the engine side is the engine's, not this document's. Narrowing the schema further, placing the runner,
substituting inputs, writing the attestation and enforcing the pinned digests are done by the engine; a file this
validator admits has been shown to sit inside this contract and nothing more. 2026-11-12.

## Layout

```
actions/
  go.mod                      one module for every runner and the validator
  go-build/
    action.yml                the action, the one file the contract is about
    record.yaml               a tool-invocation/v1 record pinning the runner binary
    main.go                   the runner (a thin main over actions/internal/gocheck)
  go-vet/                     the same three files
```

The runners share one module, `github.com/saphan-ai/test-studio/actions`, because they share one body
(`actions/internal/gocheck`) and one JUnit model (`conformance/junit`, the model the sample adapter already used).
The directory name is the action's id and the runner's id.

## The action file

`action.yml` is valid against the vendored SchemaStore `github-action.json` (JSON Schema draft-07) and also inside
this narrowing:

| key | rule |
|---|---|
| top level | only `name`, `description`, `inputs`, `outputs`, `runs` |
| `inputs.<id>` | only `description` (required), `default`, `required` |
| `outputs` | must declare `junit`; every output carries only `description` and `value` |
| `outputs.junit.value` | the literal path of the JUnit report, relative to the job's work directory, for example `go-build.junit.xml`; no `${{`, no absolute path, no `..` |
| `runs` | only `using` and `steps`; `using` is `composite` |
| `runs.steps` | exactly one step, with only `run` and `shell`; `shell` is `saphan-proc` |
| `runs.steps[0].run` | the run string below |

### The run string

The engine never starts a shell. It splits `run` into argv by whitespace, `"double quotes"` group a word, and nothing
else is interpreted: no globbing, no variables, no pipes, no redirects. Token 0 is the catalogue runner id, which is
the name of the action's directory. `${{ inputs.<name> }}` is replaced only when it is a whole token and only for a
declared input. The validator enforces the same reading.

## The runner

A runner is a program that takes the arguments the run string gives it and writes its JUnit report in the work
directory, at the path `outputs.junit.value` names. The report is a `testsuite` of `testcase` elements, one per Go
package checked; a failed package's case carries the compiler or vet output as its failure text. A runner leaves the
report whatever happens: when it cannot run, the report holds one error case named `runner`.

| exit code | meaning |
|---|---|
| 0 | every case passed |
| 1 | a case failed |
| 2 | the runner itself could not run; the reason is on stderr |

`go-build` runs `go build` and `go-vet` runs `go vet` over the packages its arguments select (`./...` by default).
`go` is the host toolchain the action requires, not a downloaded asset.

## The record

Beside each `action.yml`, `record.yaml` is a `tool-invocation/v1` record (`spec/TOOL_RECORD_V1.md`). Its `pin.sha256`
is the sha256 of the runner binary, built with `CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags=-buildid=` (without `-buildvcs=false` the binary carries the
commit it was built from, and the digest changes with every commit). The engine puts
that digest, and the git blob digest of `action.yml`, into the attestation and refuses a binary whose digest differs.
The action file carries no digest of its own. A digest depends on the Go toolchain and the platform; the one in the
record was taken with the toolchain named in `actions/pin-toolchain.txt`, and `go -C actions test ./...` compares it
only where that toolchain is the one running.

## A worked example

```yaml
name: go-build
description: Compile the selected Go packages and report one JUnit case per package.
inputs:
  packages:
    description: Package patterns to build, as the go command reads them.
    default: ./...
    required: false
outputs:
  junit:
    description: Path of the JUnit report, relative to the job's work directory.
    value: go-build.junit.xml
runs:
  using: composite
  steps:
    - run: go-build ${{ inputs.packages }}
      shell: saphan-proc
```

Check a file with `go -C actions test ./actionspec`, or build `actions/cmd/validate-action` and give it the paths:
it prints `<file>: admitted` or one refusal per line and exits 0, 1 (refused) or 2 (usage or unreadable file).

## What is refused

Every refusal names a code, the key and the file: `<file>: refused <code> at <key>: <what to change>`.

| code | refused |
|---|---|
| `yaml-syntax`, `not-a-mapping` | a file that is not one YAML mapping |
| `duplicate-key`, `yaml-alias` | a key written twice; a YAML alias |
| `schema-violation` | anything the SchemaStore schema refuses; the message is the schema's |
| `unknown-top-level-key` | a top-level key beyond the five, for example `branding` or `author` (both legal upstream) |
| `input-key-outside-contract` | an input key beyond `description`, `default`, `required` |
| `output-key-outside-contract` | an output key beyond `description`, `value` |
| `junit-output-missing` | no `outputs.junit` |
| `junit-value-not-literal` | `outputs.junit.value` missing, containing `${{`, absolute or containing `..` |
| `junit-value-not-the-report-path` | `outputs.junit.value` other than exactly `<id>.junit.xml`, the path the runner writes for the JUnit report |
| `runs-key-outside-contract` | a `runs` key beyond `using` and `steps` |
| `runs-using-not-composite` | `runs.using` other than `composite`, for example `node20` or `docker` |
| `runs-uses` | a `uses:` in `runs` or in a step |
| `runs-steps-count` | any number of steps but one |
| `step-key-outside-contract` | a step key beyond `run` and `shell` |
| `step-shell-not-saphan-proc` | `shell` missing or other than `saphan-proc`, `bash` included |
| `step-run-missing` | a step with no `run` string |
| `run-unterminated` | a double quote or `${{` that is never closed |
| `run-shell-metacharacter` | a pipe, semicolon, ampersand, angle bracket, dollar sign, backtick, asterisk, question mark, square or curly bracket, tilde, backslash, single quote or line break, outside a `${{ }}` |
| `run-substitution-inside-token` | a `${{ }}` that shares its token with other text, such as `--pkg=${{ inputs.packages }}` |
| `run-expression-unsupported` | a `${{ }}` that is not `inputs.<name>` |
| `run-undeclared-input` | `inputs.<name>` for an input the action does not declare |
| `run-empty-token` | an empty quoted token `""`; the engine would pass an empty argv word and exit 2 |
| `run-runner-id-mismatch` | token 0 is not the action's directory name |

Each refusal has a test that mutates exactly the field it names in an otherwise admitted file and requires the
refusal, `actions/actionspec/actionspec_test.go`.
