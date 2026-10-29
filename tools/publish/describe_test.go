package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opskat/opskat/pkg/extstore"
)

// describePackage reads the package as the app's install does: the display strings
// of each index language resolved from its locales, its icon, and manifest.json's
// security contract.
func TestDescribePackageReadsThePackageMetadata(t *testing.T) {
	ctx := context.Background()
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	dist, err := (&fakeBuilder{}).Build(ctx, filepath.Join(root, "extensions", "sample"))
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(t.TempDir(), "sample.zip")
	if err := zipDir(dist, pkg); err != nil {
		t.Fatal(err)
	}

	got, err := describePackage(ctx, pkg)
	if err != nil {
		t.Fatal(err)
	}

	if got.Display["zh-CN"] != (extstore.Display{Name: "示例", Description: "发布工具测试用的扩展"}) {
		t.Errorf("zh-CN display = %+v", got.Display["zh-CN"])
	}
	if got.Display["en"].Name != "Sample" || got.Icon != "radar" {
		t.Errorf("en display / icon = %+v / %q", got.Display["en"], got.Icon)
	}
	m := got.Manifest
	if m.Name != "sample" || m.Version != "0.2.0" || m.HostABI != "1.0" || m.MinAppVersion != "1.15.0" {
		t.Errorf("manifest = %s %s hostABI %s minApp %s", m.Name, m.Version, m.HostABI, m.MinAppVersion)
	}
	if !reflect.DeepEqual(m.Permissions.Network.HTTP, []string{"https://example.com/"}) {
		t.Errorf("http allowlist = %v", m.Permissions.Network.HTTP)
	}
}

func TestDescribePackageRejectsWhatTheHostCannotLoad(t *testing.T) {
	ctx := context.Background()
	dist := t.TempDir()
	writeFile(t, dist, "manifest.json", sampleManifest(t, "sample", "0.2.0"))
	writeFile(t, dist, "main.wasm", "not wasm")
	pkg := filepath.Join(t.TempDir(), "sample.zip")
	if err := zipDir(dist, pkg); err != nil {
		t.Fatal(err)
	}
	if _, err := describePackage(ctx, pkg); err == nil {
		t.Fatal("describePackage accepted a package whose wasm cannot load")
	}
}

// A package on a hostABI the host no longer supports, or in the old manifest
// shape, never reaches the index: publishing must fail where the app would.
func TestDescribePackageRefusesWhatTheHostRefuses(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		manifest string
		want     string
	}{
		"nightly hostABI 2.x": {
			manifest: strings.Replace(sampleManifest(t, "sample", "0.2.0"), `"hostABI": "1.0"`, `"hostABI": "2.5"`, 1),
			want:     "hostABI 2.5",
		},
		"old shape under hostABI 1.0": {
			manifest: strings.Replace(sampleManifest(t, "sample", "0.2.0"), `"permissions"`, `"capabilities"`, 1),
			want:     "capabilities",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dist := t.TempDir()
			writeFile(t, dist, "manifest.json", tc.manifest)
			writeFile(t, dist, "main.wasm", string(readFile(t, sampleWasm)))
			pkg := filepath.Join(t.TempDir(), "sample.zip")
			if err := zipDir(dist, pkg); err != nil {
				t.Fatal(err)
			}
			_, err := describePackage(ctx, pkg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("describePackage error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}
