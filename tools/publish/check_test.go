package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/extstore"
)

// writeIndex writes an unsigned index.json with the given published versions.
func writeIndex(t *testing.T, root string, published map[string][]string) {
	t.Helper()
	idx := extstore.Index{Format: extstore.FormatVersion}
	for name, versions := range published {
		ext := extstore.Extension{Name: name}
		for _, v := range versions {
			ext.Versions = append(ext.Versions, extstore.Version{Version: v, HostABI: "2.2",
				PublishedAt: time.Unix(0, 0).UTC(),
				Source:      extstore.Source{Type: extstore.SourceOCI, Ref: "ref", SHA256: "00"}})
		}
		idx.Extensions = append(idx.Extensions, ext)
	}
	raw, err := encodeIndex(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, indexFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRejectsVersionBelowHighestPublished(t *testing.T) {
	root := newRepo(t, map[string]string{"sample": "0.2.0", "other": "1.0.0"})
	writeIndex(t, root, map[string][]string{"sample": {"0.1.0", "0.3.0"}, "other": {"1.0.0"}})

	err := check(root)
	if err == nil {
		t.Fatal("check passed with sample at 0.2.0 below published 0.3.0")
	}
	for _, want := range []string{"sample", "0.2.0", "0.3.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "other") {
		t.Errorf("error %q blames an extension at its published version", err)
	}
}

func TestCheckAcceptsPublishedOrNewerVersions(t *testing.T) {
	cases := map[string]string{"unchanged": "0.3.0", "bumped": "0.4.0"}
	for name, version := range cases {
		t.Run(name, func(t *testing.T) {
			root := newRepo(t, map[string]string{"sample": version, "brand-new": "0.0.1"})
			writeIndex(t, root, map[string][]string{"sample": {"0.1.0", "0.3.0"}})
			if err := check(root); err != nil {
				t.Fatalf("check(%s) = %v", version, err)
			}
		})
	}
}

func TestCheckWithoutIndexPasses(t *testing.T) {
	root := newRepo(t, map[string]string{"sample": "0.1.0"})
	if err := check(root); err != nil {
		t.Fatalf("check with no index yet = %v", err)
	}
}

func TestCheckRejectsInvalidManifest(t *testing.T) {
	root := newRepo(t, map[string]string{"sample": "0.1.0"})
	writeFile(t, filepath.Join(root, "extensions", "sample"), "manifest.json", `{"name":"sample","version":"one"}`)
	if err := check(root); err == nil {
		t.Fatal("check accepted a manifest the host would refuse")
	}
}
