package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// assetStageDirMode is the staging directory after the copy is closed: read and
// execute only, so the owner cannot rename or unlink the file before (c) starts.
// assetStageFileMode is the copy itself. assetStageWriteMode is what removal
// restores, because the owner cannot unlink entries in a mode 0500 directory.
const (
	assetStageDirMode   os.FileMode = 0o500
	assetStageFileMode  os.FileMode = 0o500
	assetStageWriteMode os.FileMode = 0o700
)

// stagePinnedAsset copies assetFile into a new private directory and returns that
// directory, the absolute path of the copy, and the sha256 of the bytes written.
// That digest is the one read of the source. The directory is made read-and-execute
// only after the copy is closed. On failure the directory is removed.
// The caller removes it when the run ends.
func stagePinnedAsset(assetFile string) (dir, copyPath, sum string, err error) {
	dir, err = os.MkdirTemp("", "tool-conformance-asset-*")
	if err != nil {
		return "", "", "", err
	}
	// created is not the named result: a failing return clears dir before this defer runs.
	created := dir
	defer func() {
		if err != nil {
			if rerr := removeStagedAsset(created); rerr != nil {
				err = fmt.Errorf("%w (and removing %s: %v)", err, created, rerr)
			}
		}
	}()
	dst := filepath.Join(dir, stagedAssetName(assetFile))
	sum, err = copyPinnedAsset(assetFile, dst)
	if err != nil {
		return "", "", "", err
	}
	// The copy is closed. Lock the directory so the file cannot be renamed or unlinked.
	if err = os.Chmod(dir, assetStageDirMode); err != nil {
		return "", "", "", err
	}
	copyPath, err = filepath.Abs(dst)
	if err != nil {
		return "", "", "", err
	}
	return dir, copyPath, sum, nil
}

// removeStagedAsset removes a private asset directory. The directory is mode 0500
// once staging has finished, and that mode refuses unlink, so removal restores
// write permission first. A directory that never reached that chmod is already
// writable; restoring write permission there is the same act.
func removeStagedAsset(dir string) error {
	if err := os.Chmod(dir, assetStageWriteMode); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// stagedAssetName is the copy's base name. A base that is not a single path element
// becomes "asset", so the copy cannot be written outside the private directory.
func stagedAssetName(assetFile string) string {
	name := filepath.Base(assetFile)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return "asset"
	}
	return name
}

// copyPinnedAsset copies the bytes of src to dst at assetStageFileMode and returns
// the sha256 of the bytes the destination accepted. The hasher is second in the
// writer, so a short write is not hashed. os.Open follows a symlink, so the copy
// is the target's bytes, not the link. The file is closed before this returns.
func copyPinnedAsset(src, dst string) (sum string, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := in.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, assetStageFileMode)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", err
	}
	// OpenFile applies the umask. Chmod sets the mode the staging contract requires.
	if err = os.Chmod(dst, assetStageFileMode); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyTree copies the regular files and directories under src into dst. A symlink or any other special file is
// refused: the copy must not point back out of itself.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm())
		default:
			return fmt.Errorf("%s is not a regular file or directory (a symlink?)", rel)
		}
	})
}

// bind returns the values of the typed inputs for one run, all rooted in the fresh copy.
func bind(rec *record, root string) map[string]string {
	v := map[string]string{
		"repo_root":   root,
		"output_path": filepath.Join(root, filepath.FromSlash(rec.Output.Path)),
	}
	if rec.Config != "" {
		v["config_path"] = filepath.Join(root, filepath.FromSlash(rec.Config))
	}
	for name, e := range rec.Params {
		if e.Type == "relpath" {
			v[name] = filepath.Join(root, filepath.FromSlash(e.Value))
		} else {
			v[name] = e.Value
		}
	}
	return v
}

func substitute(el string, vals map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(el, func(m string) string {
		return vals[placeholderRe.FindStringSubmatch(m)[1]]
	})
}

// runResult is what check (c) hands to check (d).
type runResult struct {
	code int
	tail string
}

// runTool executes the record's argv, with no shell, inside root, and waits at most timeout.
// When program is set, it is the absolute path of the private asset copy. It replaces
// command[0], which is dropped and not compared with pin.asset. command[1:] is still
// what runs, after the same placeholder substitution.
func runTool(rec *record, root string, timeout time.Duration, program string) (runResult, error) {
	vals := bind(rec, root)
	argv := make([]string, len(rec.Command))
	for i, el := range rec.Command {
		argv[i] = substitute(el, vals)
	}
	if program != "" {
		rest := []string{}
		if len(argv) > 1 {
			rest = argv[1:]
		}
		argv = append([]string{program}, rest...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	res := runResult{tail: tailOf(buf.String(), 600)}
	if ctx.Err() != nil {
		return res, fmt.Errorf("the tool did not finish within %s and was killed", timeout)
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.code = 0
	case errors.As(err, &ee):
		res.code = ee.ExitCode()
		if res.code < 0 {
			return res, fmt.Errorf("the tool was ended by a signal (%v)", ee)
		}
	default:
		return res, fmt.Errorf("the tool did not start: %v", err)
	}
	return res, nil
}

func tailOf(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	return s
}

// classOf maps an exit code through exit_map; the key it used comes back for the message.
func classOf(rec *record, code int) (class, key string) {
	key = strconv.Itoa(code)
	if c, ok := rec.ExitMap[key]; ok {
		return c, key
	}
	return rec.ExitMap["default"], "default"
}
