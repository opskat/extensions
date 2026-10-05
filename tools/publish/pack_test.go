package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestZipDirPutsManifestAtRootAndIsReproducible(t *testing.T) {
	dist := t.TempDir()
	writeFile(t, dist, "manifest.json", `{"name":"x"}`)
	writeFile(t, dist, "main.wasm", "wasm")
	writeFile(t, dist, filepath.Join("locales", "en.json"), "{}")
	writeFile(t, dist, filepath.Join("frontend", "index.js"), "export {}")

	out := t.TempDir()
	first := filepath.Join(out, "a.zip")
	if err := zipDir(dist, first); err != nil {
		t.Fatal(err)
	}

	r, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	r.Close()
	want := []string{"frontend/index.js", "locales/en.json", "main.wasm", "manifest.json"}
	if !slices.Equal(names, want) {
		t.Fatalf("zip entries = %v, want %v", names, want)
	}

	// A rebuild touches every file; the package bytes must not change.
	later := time.Now().Add(time.Hour)
	for _, rel := range want {
		if err := os.Chtimes(filepath.Join(dist, rel), later, later); err != nil {
			t.Fatal(err)
		}
	}
	second := filepath.Join(out, "b.zip")
	if err := zipDir(dist, second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFile(t, first), readFile(t, second)) {
		t.Fatal("zipping the same files twice produced different bytes")
	}
}
