package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureDir = "../../testdata/tool-delta"

func fixture(name string) string { return filepath.Join(fixtureDir, name) }

func runDelta(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	code := run(args, &buf)
	out := buf.String()
	if strings.Contains(out, "marker-") {
		t.Fatalf("finding message leaked into the output:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || lines[len(lines)-1] != remarkLine {
		t.Fatalf("last line = %q, want the remark\n%s", lines[len(lines)-1], out)
	}
	return code, out
}

func requireSummary(t *testing.T, out string, n, w, e int) {
	t.Helper()
	want := fmt.Sprintf("delta: %d new, %d waived, %d expired\n", n, w, e)
	if !strings.Contains(out, want) {
		t.Fatalf("missing summary %q in\n%s", want, out)
	}
}

func linesWith(out, prefix string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			got = append(got, line)
		}
	}
	return got
}

// specDerived is the waiver section's formula, written out again so a drift
// in the command's own helper fails this test instead of following it.
func specDerived(rule, uri, message string) string {
	h := sha256.New()
	parts := [][]byte{[]byte(rule), {0}, []byte(uri), {0}, []byte(message)}
	for _, p := range parts {
		if _, err := h.Write(p); err != nil {
			panic(err)
		}
	}
	return "saphan/v1:" + hex.EncodeToString(h.Sum(nil))[:16]
}

func TestUnchangedReportHasEmptyDelta(t *testing.T) {
	t.Parallel()
	code, out := runDelta(t,
		"--current", fixture("with-fingerprints.sarif"),
		"--baseline", fixture("baseline.sarif"),
		"--today", "2026-11-04",
	)
	if code != exitOK {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	requireSummary(t, out, 0, 0, 0)
	if len(linesWith(out, "NEW ")) != 0 {
		t.Fatalf("unchanged report printed NEW lines:\n%s", out)
	}
}

func TestAddedResultIsTheOnlyNewLine(t *testing.T) {
	t.Parallel()
	code, out := runDelta(t,
		"--current", fixture("current-added.sarif"),
		"--baseline", fixture("baseline.sarif"),
		"--today", "2026-11-04",
	)
	if code != exitNew {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	requireSummary(t, out, 1, 0, 0)
	got := linesWith(out, "NEW ")
	if len(got) != 1 || got[0] != "NEW RULE-NEW newfingerprint0001 notes/added.txt:7" {
		t.Fatalf("NEW lines = %#v", got)
	}
}

func TestWaiverBeforeExpirySuppressesNew(t *testing.T) {
	t.Parallel()
	code, out := runDelta(t,
		"--current", fixture("current-added.sarif"),
		"--baseline", fixture("baseline.sarif"),
		"--waivers", fixture("waivers.yaml"),
		"--today", "2026-11-04",
	)
	if code != exitOK {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	requireSummary(t, out, 0, 1, 0)
	got := linesWith(out, "WAIVED ")
	if len(got) != 1 || got[0] != "WAIVED RULE-NEW newfingerprint0001 until 2027-03-31" {
		t.Fatalf("WAIVED lines = %#v", got)
	}
	if len(linesWith(out, "NEW ")) != 0 {
		t.Fatalf("a waived result was also printed as NEW:\n%s", out)
	}
}

func TestWaiverHoldsOnItsExpiryDate(t *testing.T) {
	t.Parallel()
	code, out := runDelta(t,
		"--current", fixture("current-added.sarif"),
		"--baseline", fixture("baseline.sarif"),
		"--waivers", fixture("waivers.yaml"),
		"--today", "2027-03-31",
	)
	if code != exitOK {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	requireSummary(t, out, 0, 1, 0)
}

func TestWaiverAfterExpiryIsNewAndExpired(t *testing.T) {
	t.Parallel()
	code, out := runDelta(t,
		"--current", fixture("current-added.sarif"),
		"--baseline", fixture("baseline.sarif"),
		"--waivers", fixture("waivers.yaml"),
		"--today", "2027-04-01",
	)
	if code != exitNew {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	requireSummary(t, out, 1, 0, 1)
	neu := linesWith(out, "NEW ")
	exp := linesWith(out, "EXPIRED ")
	if len(neu) != 1 || neu[0] != "NEW RULE-NEW newfingerprint0001 notes/added.txt:7" {
		t.Fatalf("NEW lines = %#v", neu)
	}
	wantExp := "EXPIRED RULE-NEW newfingerprint0001 synthetic fixture finding expired 2027-03-31"
	if len(exp) != 1 || exp[0] != wantExp {
		t.Fatalf("EXPIRED lines = %#v", exp)
	}
}

func TestDerivedFingerprintWaiver(t *testing.T) {
	t.Parallel()
	want := specDerived("RULE-NOFP", "notes/readme.txt", "marker-no-fingerprint")
	raw, err := os.ReadFile(fixture("waivers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), want) {
		t.Fatalf("waiver file does not list the spec fingerprint %s", want)
	}
	code, out := runDelta(t,
		"--current", fixture("without-fingerprints.sarif"),
		"--waivers", fixture("waivers.yaml"),
		"--today", "2026-11-04",
	)
	if code != exitOK {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	requireSummary(t, out, 0, 1, 0)
	got := linesWith(out, "WAIVED ")
	wantLine := "WAIVED RULE-NOFP " + want + " until 2027-03-31"
	if len(got) != 1 || got[0] != wantLine {
		t.Fatalf("WAIVED lines = %#v, want %s", got, wantLine)
	}
}

func TestLineMoveKeepsDerivedIdentity(t *testing.T) {
	t.Parallel()
	_, at4 := runDelta(t, "--current", fixture("without-fingerprints.sarif"), "--today", "2026-11-04")
	_, at40 := runDelta(t, "--current", fixture("without-fingerprints-moved.sarif"), "--today", "2026-11-04")
	id4 := linesWith(at4, "NEW ")
	id40 := linesWith(at40, "NEW ")
	if len(id4) != 1 || len(id40) != 1 {
		t.Fatalf("identities:\n%s\n%s", at4, at40)
	}
	if id4[0] == id40[0] {
		t.Fatalf("moving the line did not change the printed location:\n%s\n%s", id4[0], id40[0])
	}
	want := specDerived("RULE-NOFP", "notes/readme.txt", "marker-no-fingerprint")
	if !strings.Contains(id4[0], want) || !strings.Contains(id40[0], want) {
		t.Fatalf("identity changed with the line:\n%s\n%s", id4[0], id40[0])
	}
	code, out := runDelta(t,
		"--current", fixture("without-fingerprints-moved.sarif"),
		"--baseline", fixture("without-fingerprints.sarif"),
		"--today", "2026-11-04",
	)
	if code != exitOK {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	requireSummary(t, out, 0, 0, 0)
	if len(linesWith(out, "NEW ")) != 0 {
		t.Fatalf("a line move was reported as new:\n%s", out)
	}
}

func TestWaiverMissingExpiresIsRefused(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(fixture("waivers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Mutate exactly the expires field of the first entry.
	dropped := strings.Replace(string(raw), "    expires: 2027-03-31\n", "", 1)
	if dropped == string(raw) || strings.Count(dropped, "expires:") != 1 {
		t.Fatalf("the fixture was not mutated in exactly one expires field:\n%s", dropped)
	}
	path := filepath.Join(t.TempDir(), "waivers.yaml")
	if err := os.WriteFile(path, []byte(dropped), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runDelta(t,
		"--current", fixture("current-added.sarif"),
		"--baseline", fixture("baseline.sarif"),
		"--waivers", path,
		"--today", "2026-11-04",
	)
	if code != exitUsage {
		t.Fatalf("exit %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "waiver entry 0: missing field expires") {
		t.Fatalf("refusal does not name the index and expires:\n%s", out)
	}
}

func TestWaiverRuleMismatchDoesNotWaive(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(fixture("waivers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Mutate exactly the rule of the waiver whose fingerprint matches RULE-NEW.
	swapped := strings.Replace(string(raw), "rule: RULE-NEW\n", "rule: RULE-OTHER\n", 1)
	if swapped == string(raw) {
		t.Fatal("rule field was not mutated")
	}
	path := filepath.Join(t.TempDir(), "waivers.yaml")
	if err := os.WriteFile(path, []byte(swapped), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runDelta(t,
		"--current", fixture("current-added.sarif"),
		"--baseline", fixture("baseline.sarif"),
		"--waivers", path,
		"--today", "2026-11-04",
	)
	if code != exitNew {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	requireSummary(t, out, 1, 0, 0)
	if len(linesWith(out, "WAIVED ")) != 0 {
		t.Fatalf("a different rule waived the finding:\n%s", out)
	}
	got := linesWith(out, "NEW ")
	if len(got) != 1 || got[0] != "NEW RULE-NEW newfingerprint0001 notes/added.txt:7" {
		t.Fatalf("NEW lines = %#v", got)
	}
}

func TestIdentityPrefersDocumentOrderOfPartialFingerprints(t *testing.T) {
	t.Parallel()
	body := `{
	  "runs":[{"results":[{
	    "ruleId":"RULE-ORDER",
	    "message":{"text":"marker-order"},
	    "partialFingerprints":{"zzz":"from-partial-first","aaa":"from-partial-second"},
	    "fingerprints":{"k":"from-fingerprints"},
	    "locations":[{"physicalLocation":{"artifactLocation":{"uri":"notes/order.txt"},"region":{"startLine":1}}}]
	  }]}]
	}`
	path := filepath.Join(t.TempDir(), "order.sarif")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runDelta(t, "--current", path, "--today", "2026-11-04")
	if code != exitNew {
		t.Fatalf("exit %d\n%s", code, out)
	}
	got := linesWith(out, "NEW ")
	if len(got) != 1 || got[0] != "NEW RULE-ORDER from-partial-first notes/order.txt:1" {
		t.Fatalf("NEW lines = %#v", got)
	}
}

func TestIdentityUsesFingerprintsWhenPartialIsAbsent(t *testing.T) {
	t.Parallel()
	body := `{
	  "runs":[{"results":[{
	    "ruleId":"RULE-FULL",
	    "message":{"text":"marker-full"},
	    "fingerprints":{"zzz":"from-full-first","aaa":"from-full-second"},
	    "locations":[{"physicalLocation":{"artifactLocation":{"uri":"notes/full.txt"},"region":{"startLine":3}}}]
	  }]}]
	}`
	path := filepath.Join(t.TempDir(), "full.sarif")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out := runDelta(t, "--current", path, "--today", "2026-11-04")
	got := linesWith(out, "NEW ")
	if len(got) != 1 || got[0] != "NEW RULE-FULL from-full-first notes/full.txt:3" {
		t.Fatalf("NEW lines = %#v", got)
	}
}

func TestDerivedFingerprintDoesNotCoverAScannerFingerprint(t *testing.T) {
	t.Parallel()
	// The result has a scanner fingerprint, so the derived value is not one of
	// its fingerprints even though the waiver names that derived value.
	derived := specDerived("RULE-FP", "notes/other.txt", "marker-with-fingerprint")
	waiver := "waivers:\n  - rule: RULE-FP\n    fingerprint: " + derived + "\n    reason: would be wrong\n    expires: 2027-03-31\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "waivers.yaml")
	if err := os.WriteFile(path, []byte(waiver), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runDelta(t,
		"--current", fixture("with-fingerprints.sarif"),
		"--waivers", path,
		"--today", "2026-11-04",
	)
	if code != exitNew {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	requireSummary(t, out, 1, 0, 0)
	got := linesWith(out, "NEW ")
	if len(got) != 1 || !strings.Contains(got[0], "abc123fingerprint") {
		t.Fatalf("NEW lines = %#v", got)
	}
}

func TestExpiredWaiverOnAKnownResultIsSilent(t *testing.T) {
	t.Parallel()
	waiver := "waivers:\n  - rule: RULE-FP\n    fingerprint: abc123fingerprint\n    reason: already in the baseline\n    expires: 2020-01-01\n"
	path := filepath.Join(t.TempDir(), "waivers.yaml")
	if err := os.WriteFile(path, []byte(waiver), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runDelta(t,
		"--current", fixture("with-fingerprints.sarif"),
		"--baseline", fixture("baseline.sarif"),
		"--waivers", path,
		"--today", "2026-11-04",
	)
	if code != exitOK {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	requireSummary(t, out, 0, 0, 0)
	if len(linesWith(out, "EXPIRED ")) != 0 || len(linesWith(out, "NEW ")) != 0 {
		t.Fatalf("a known result was reported:\n%s", out)
	}
}

func TestNoBaselineTreatsEveryResultAsNew(t *testing.T) {
	t.Parallel()
	code, out := runDelta(t, "--current", fixture("baseline.sarif"), "--today", "2026-11-04")
	if code != exitNew {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	requireSummary(t, out, 1, 0, 0)
}

func TestMissingCurrentIsUsage(t *testing.T) {
	t.Parallel()
	code, out := runDelta(t, "--today", "2026-11-04")
	if code != exitUsage {
		t.Fatalf("exit %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, usageLine) {
		t.Fatalf("usage missing:\n%s", out)
	}
}

func TestBadTodayIsUsage(t *testing.T) {
	t.Parallel()
	code, out := runDelta(t, "--current", fixture("baseline.sarif"), "--today", "11-04-2026")
	if code != exitUsage {
		t.Fatalf("exit %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "--today") {
		t.Fatalf("refusal does not name --today:\n%s", out)
	}
}
