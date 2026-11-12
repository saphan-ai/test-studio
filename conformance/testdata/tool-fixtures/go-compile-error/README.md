# go-compile-error

Expected outcome: **findings**. Package `ok` compiles; package `bad` has one compile error, so `go build ./...` in
this directory exits non-zero. A JUnit report of the `go-build` action holds two test cases, `ok` passed and `bad`
failed.

This is a module of its own (its `go.mod` sits here), and it lives under `testdata`, so `go build ./...` and
`go vet ./...` run from the conformance module never reach it.
