package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript writes an executable shell script. The body is the whole file, shebang included.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// pathDecoy is a program named like the pin's asset. It records that it ran and exits 9,
// which is not the pinned file's exit.
func pathDecoy(t *testing.T, sentinel string) string {
	t.Helper()
	if strings.Contains(sentinel, "'") {
		t.Fatalf("sentinel path %q contains a single quote", sentinel)
	}
	dir := t.TempDir()
	writeScript(t, filepath.Join(dir, "fake-tool"), "#!/bin/sh\necho ran > '"+sentinel+"'\nexit 9\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// pinnedAsset is the file --asset names: it accepts command[1:] unchanged
// (__fake-tool__ junit report.xml), writes the JUnit the fixture expects, and exits 1.
func pinnedAsset(t *testing.T) (path, sum string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "pinned-asset")
	writeScript(t, path, "#!/bin/sh\n"+
		"if [ \"$1\" != \"__fake-tool__\" ] || [ \"$2\" != \"junit\" ] || [ \"$3\" != \"report.xml\" ]; then\n"+
		"  echo \"asset args: $*\" >&2\n"+
		"  exit 4\n"+
		"fi\n"+
		"cat > \"$3\" << 'ENDXML'\n"+fakeJUnit+"ENDXML\n"+
		"exit 1\n")
	var err error
	sum, err = fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, sum
}

// assetRecord is a valid record whose command[0] is the bare asset name, so a PATH lookup
// can find a different program than the file --asset names. The pin hash is the pinned file's.
func assetRecord(t *testing.T, sum string) map[string]any {
	t.Helper()
	return mutate(t, func(r map[string]any) {
		r["command"] = []any{"fake-tool", "__fake-tool__", "junit", "report.xml"}
		sub(r, "pin")["sha256"] = sum
	})
}

// TestAssetPinRunsThePinnedFileNotThePathBinary is the defect: (b) hashes --asset, and (c)
// must execute that same file. A program named like the pin's asset sits earlier on PATH and
// exits 9; a pass means (c) ran the pinned file (exit 1, and the (c) line names its path).
func TestAssetPinRunsThePinnedFileNotThePathBinary(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "path-ran")
	pathDecoy(t, sentinel)
	asset, sum := pinnedAsset(t)
	abs, err := filepath.Abs(asset)
	if err != nil {
		t.Fatal(err)
	}
	code, out := runRecord(t, assetRecord(t, sum), "--asset", asset)
	b := statusLine(out, "b")
	c := statusLine(out, "c")
	if !strings.HasPrefix(b, "(b) PASS:") {
		t.Fatalf("check (b) is %q, want PASS on the --asset file\n%s", b, out)
	}
	if _, err := os.Stat(sentinel); err == nil || code != 0 || !strings.Contains(c, "exited with code 1") || !strings.Contains(c, abs) {
		ran := "absent"
		if _, err := os.Stat(sentinel); err == nil {
			ran = "present"
		}
		t.Fatalf("exit %d, check (c) %q, PATH sentinel %s; want exit 0, (c) naming %s and code 1, sentinel absent\n%s", code, c, ran, abs, out)
	}
	t.Logf("exit %d\n%s\n%s", code, b, c)
}

// TestMismatchedAssetStopsBeforeThePathBinaryRuns mutates exactly the --asset bytes.
// The pin still names the pinned file. (b) must FAIL and (c) must not run, so the PATH program does not either.
func TestMismatchedAssetStopsBeforeThePathBinaryRuns(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "path-ran")
	pathDecoy(t, sentinel)
	asset, sum := pinnedAsset(t)
	wrong := filepath.Join(t.TempDir(), "wrong-bytes")
	if err := os.WriteFile(wrong, []byte("not the pinned file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runRecord(t, assetRecord(t, sum), "--asset", wrong)
	if code != 1 || !strings.HasPrefix(statusLine(out, "b"), "(b) FAIL:") || !strings.Contains(statusLine(out, "b"), "pin.sha256") {
		t.Fatalf("exit %d, check (b) %q, want FAIL naming pin.sha256\n%s", code, statusLine(out, "b"), out)
	}
	if c := statusLine(out, "c"); c != "" {
		t.Fatalf("check (c) ran after the hash mismatch: %q\n%s", c, out)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("the PATH binary ran even though (b) failed\n%s", out)
	}
	t.Logf("exit %d\n%s\ncheck (c) printed: %q", code, statusLine(out, "b"), statusLine(out, "c"))
	if _, err := os.Stat(asset); err != nil {
		t.Fatal(err)
	}
}

// TestAssetPinWithoutFileSaysNothingAboutThePinnedBytes: with no --asset, (c) still runs
// argv[0] through PATH and says so. The remark is the addition; the run itself is the old one.
func TestAssetPinWithoutFileSaysNothingAboutThePinnedBytes(t *testing.T) {
	code, out := runRecord(t, validRecord(t))
	if code != 0 || !strings.HasPrefix(statusLine(out, "b"), "(b) UNMEASURED:") {
		t.Fatalf("exit %d, check (b) %q\n%s", code, statusLine(out, "b"), out)
	}
	c := statusLine(out, "c")
	if !strings.HasPrefix(c, "(c) PASS:") || !strings.Contains(c, "exited with code 1") {
		t.Fatalf("check (c) is %q, want the argv to have run as before\n%s", c, out)
	}
	const remark = "remark: argv[0] was resolved through PATH, so this run says nothing about the pinned bytes."
	if !strings.Contains(out, remark) {
		t.Fatalf("no PATH remark under (c)\n%s", out)
	}
	t.Logf("%s\n%s", c, remark)
}

// TestImagePinDoesNotRunTheAssetFile: an image pin ignores --asset and runs command[0],
// with no PATH remark added to (c).
func TestImagePinDoesNotRunTheAssetFile(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "asset-ran")
	if strings.Contains(sentinel, "'") {
		t.Fatalf("sentinel path %q contains a single quote", sentinel)
	}
	asset := filepath.Join(t.TempDir(), "decoy-asset")
	writeScript(t, asset, "#!/bin/sh\necho ran > '"+sentinel+"'\nexit 9\n")
	rec := mutate(t, func(r map[string]any) {
		r["pin"] = map[string]any{"image": "registry.invalid/tool@sha256:" + strings.Repeat("ab", 32)}
	})
	code, out := runRecord(t, rec, "--asset", asset)
	b := statusLine(out, "b")
	if code != 0 || !strings.HasPrefix(b, "(b) NOT APPLICABLE:") || !strings.Contains(b, "ignored") {
		t.Fatalf("exit %d, check (b) %q\n%s", code, b, out)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("an image pin executed --asset\n%s", out)
	}
	if strings.Contains(out, "resolved through PATH") {
		t.Fatalf("an image pin gained a PATH remark\n%s", out)
	}
	abs, err := filepath.Abs(asset)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(statusLine(out, "c"), abs) {
		t.Fatalf("check (c) names the --asset file on an image pin\n%s", out)
	}
}
