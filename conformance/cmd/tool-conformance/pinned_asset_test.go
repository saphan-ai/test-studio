package main

import (
	"bytes"
	"encoding/json"
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

// assertNoPrivateAssetCopy fails when dir still holds a staging directory.
// dir is a TMPDIR set for one run. MkdirTemp reads that variable, so a copy
// the runner did not remove is visible here, and a copy it did remove is not.
// Setting TMPDIR does not change the defer that removes the directory; it
// only gives the assertion an empty directory to read.
func assertNoPrivateAssetCopy(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tool-conformance-asset-") {
			left = append(left, e.Name())
		}
	}
	if len(left) != 0 {
		t.Fatalf("TMPDIR still holds a private asset copy: %s", strings.Join(left, ", "))
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
// exits 9; a pass means (c) ran the pinned bytes (exit 1) from a private copy, not this path.
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
	if _, err := os.Stat(sentinel); err == nil || code != 0 || !strings.Contains(c, "exited with code 1") || !strings.Contains(c, "a private copy of the file (b) hashed") || strings.Contains(c, "; ran "+abs+",") {
		ran := "absent"
		if _, err := os.Stat(sentinel); err == nil {
			ran = "present"
		}
		t.Fatalf("exit %d, check (c) %q, PATH sentinel %s; want exit 0, (c) naming a private copy and code 1, not %s, sentinel absent\n%s", code, c, ran, abs, out)
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
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
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
	assertNoPrivateAssetCopy(t, scratch)
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

// TestReplacingTheAssetAfterTheHashDoesNotChangeTheRun is the window between the
// digest and the exec. The hook replaces the original --asset with a different
// program after (b) has passed. The run must still execute the bytes that were hashed.
func TestReplacingTheAssetAfterTheHashDoesNotChangeTheRun(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "swapped-ran")
	if strings.Contains(sentinel, "'") {
		t.Fatalf("sentinel path %q contains a single quote", sentinel)
	}
	asset, sum := pinnedAsset(t)
	var copyPath, copySum string
	var copyMode, dirMode os.FileMode
	var staged bool
	t.Cleanup(func() { betweenPinAndRun = nil })
	betweenPinAndRun = func(given string) {
		if lastStagedAsset != "" {
			staged = true
			copyPath = lastStagedAsset
			info, err := os.Stat(copyPath)
			if err != nil {
				t.Errorf("stat private copy: %v", err)
			} else {
				copyMode = info.Mode().Perm()
			}
			dinfo, err := os.Stat(filepath.Dir(copyPath))
			if err != nil {
				t.Errorf("stat private directory: %v", err)
			} else {
				dirMode = dinfo.Mode().Perm()
			}
			copySum, err = fileSHA256(copyPath)
			if err != nil {
				t.Errorf("hash private copy: %v", err)
			}
		}
		writeScript(t, given, "#!/bin/sh\necho ran > '"+sentinel+"'\nexit 9\n")
	}
	code, out := runRecord(t, assetRecord(t, sum), "--asset", asset)
	c := statusLine(out, "c")
	_, serr := os.Stat(sentinel)
	if serr == nil || code != 0 || !strings.Contains(c, "exited with code 1") || !strings.Contains(c, "a private copy of the file (b) hashed") {
		present := serr == nil
		t.Fatalf("replaced --asset changed the run: exit %d, (c) %q, sentinel present=%v; want exit 0, code 1, private copy, sentinel absent\n%s", code, c, present, out)
	}
	if !staged || copyMode != 0o500 || dirMode != 0o700 || copySum != sum {
		t.Fatalf("private copy: staged=%v mode=%#o dir=%#o sum=%s want=%s path=%s", staged, copyMode, dirMode, copySum, sum, copyPath)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("private copy %s still present after the run: %v", copyPath, err)
	}
	if _, err := os.Stat(filepath.Dir(copyPath)); !os.IsNotExist(err) {
		t.Fatalf("private directory %s still present after the run: %v", filepath.Dir(copyPath), err)
	}
	replaced, err := fileSHA256(asset)
	if err != nil {
		t.Fatal(err)
	}
	if replaced == sum {
		t.Fatal("the original --asset was not replaced, so the hook did not run")
	}
	if strings.Contains(c, "; ran "+asset+",") {
		t.Fatalf("(c) names the original path %s\n%s", asset, c)
	}
}

// TestRelativeAssetRunsTheHashedFileNotAPathNamesake: a bare relative --asset,
// with a PATH program of that same name, still runs the hashed file.
// The pinned asset exits 1, so the (c) line says "exited with code 1".
func TestRelativeAssetRunsTheHashedFileNotAPathNamesake(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "path-ran")
	if strings.Contains(sentinel, "'") {
		t.Fatalf("sentinel path %q contains a single quote", sentinel)
	}
	asset, sum := pinnedAsset(t)
	// command[0] is fake-tool. The relative --asset is the file's base name, and that
	// is the name a bare argv[0] is looked up under. Both names can steal the run.
	pathDecoy(t, sentinel)
	decoyDir := t.TempDir()
	writeScript(t, filepath.Join(decoyDir, filepath.Base(asset)), "#!/bin/sh\necho ran > '"+sentinel+"'\nexit 9\n")
	t.Setenv("PATH", decoyDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	recPath := filepath.Join(t.TempDir(), "rec.json")
	body, err := json.Marshal(assetRecord(t, sum))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	fx, err := filepath.Abs(fixturesRootPath)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := filepath.Abs(recordSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(asset))
	var buf bytes.Buffer
	code := run([]string{"--record", recPath, "--fixtures-root", fx, "--record-schema", sc, "--asset", filepath.Base(asset)}, &buf)
	out := buf.String()
	c := statusLine(out, "c")
	_, serr := os.Stat(sentinel)
	if serr == nil || code != 0 || !strings.Contains(c, "exited with code 1") {
		t.Fatalf("relative --asset: exit %d, (c) %q, sentinel present=%v\n%s", code, c, serr == nil, out)
	}
}
