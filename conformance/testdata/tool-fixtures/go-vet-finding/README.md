# go-vet-finding

Expected outcome: **findings**. Both packages compile, so `go build ./...` passes; package `noisy` has one `go vet`
finding (a `Sprintf` verb that does not match its argument), so `go vet ./...` exits non-zero. A JUnit report of the
`go-vet` action holds two test cases, `ok` passed and `noisy` failed.

This is a module of its own (its `go.mod` sits here), and it lives under `testdata`, so `go build ./...` and
`go vet ./...` run from the conformance module never reach it.
