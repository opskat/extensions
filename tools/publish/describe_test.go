package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extstore"
)

func TestDescribePackageMatchesWhatTheHostInstalls(t *testing.T) {
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

	// What the app shows after installing the same zip.
	mgr := extension.NewManager(t.TempDir(), func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{})
	}, zap.NewNop())
	defer mgr.Close(ctx)
	if _, err := mgr.Install(ctx, pkg); err != nil {
		t.Fatal(err)
	}
	ext := mgr.GetExtension("sample")
	for _, lang := range []string{"en", "zh-CN"} {
		lm := ext.Manifest.Localized(func(key string) string { return ext.Translate(lang, key) })
		want := extstore.Display{Name: lm.I18n.DisplayName, Description: lm.I18n.Description}
		if got.Display[lang] != want {
			t.Errorf("display[%s] = %+v, host shows %+v", lang, got.Display[lang], want)
		}
	}
	if got.Icon != ext.Manifest.Icon {
		t.Errorf("icon = %q, host shows %q", got.Icon, ext.Manifest.Icon)
	}
	if !reflect.DeepEqual(got.Manifest.Capabilities, ext.Manifest.Capabilities) {
		t.Errorf("capabilities = %+v, host grants %+v", got.Manifest.Capabilities, ext.Manifest.Capabilities)
	}

	// And the values are the sample's, not empty agreement.
	if got.Display["zh-CN"] != (extstore.Display{Name: "示例", Description: "发布工具测试用的扩展"}) {
		t.Errorf("zh-CN display = %+v", got.Display["zh-CN"])
	}
	if got.Display["en"].Name != "Sample" || got.Icon != "radar" {
		t.Errorf("en display / icon = %+v / %q", got.Display["en"], got.Icon)
	}
	m := got.Manifest
	if m.Name != "sample" || m.Version != "0.2.0" || m.HostABI != "2.2" || m.MinAppVersion != "1.14.1" {
		t.Errorf("manifest = %s %s hostABI %s minApp %s", m.Name, m.Version, m.HostABI, m.MinAppVersion)
	}
	if !reflect.DeepEqual(m.Capabilities.HTTP.Allowlist, []string{"https://example.com/"}) {
		t.Errorf("http allowlist = %v", m.Capabilities.HTTP.Allowlist)
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
