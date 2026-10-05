package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// zipEpoch is the modification time of every zip entry: file times on a CI
// runner say when the checkout happened, and would make every rebuild of an
// unchanged extension a different package.
var zipEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// zipDir packs the installable directory dir into dest with its contents at the
// archive root — manifest.json included — which is the layout the app's zip
// install unpacks. Entries are files only, in lexical path order, with fixed
// times and modes, so the same files always give the same bytes.
func zipDir(dir, dest string) (err error) {
	out, err := os.Create(dest) //nolint:gosec // caller-chosen output path
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	zw := zip.NewWriter(out)
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate, Modified: zipEpoch}
		hdr.SetMode(0o644)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path) //nolint:gosec // walking the build output
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// fileDigest returns the lowercase hex sha256 and the size of a file.
func fileDigest(path string) (string, int64, error) {
	f, err := os.Open(path) //nolint:gosec // our own package
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
