package main

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extstore"
)

// indexLanguages are the languages the index carries display strings for.
var indexLanguages = []string{"en", "zh-CN"}

// packageMeta is what the index says about one package, as the app sees it.
type packageMeta struct {
	// Manifest is the host's merged view: manifest.json plus describe().
	Manifest *extension.Manifest
	Display  map[string]extstore.Display
	Icon     string
}

// describePackage stages the package zip exactly as the app's install does —
// unzip, parse manifest.json, load the WASM, run describe() — and reads the
// metadata back the way the extension settings show it. A package the app could
// not install fails here, before anything is pushed.
func describePackage(ctx context.Context, zipPath string) (packageMeta, error) {
	dir, err := os.MkdirTemp("", "publish-stage-")
	if err != nil {
		return packageMeta{}, err
	}
	defer os.RemoveAll(dir)

	mgr := extension.NewManager(dir, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{})
	}, zap.NewNop())
	defer mgr.Close(ctx)
	staged, err := mgr.Stage(ctx, zipPath)
	if err != nil {
		return packageMeta{}, fmt.Errorf("load package as the app would: %w", err)
	}
	defer staged.Abort(ctx)

	ext := staged.Extension()
	meta := packageMeta{Manifest: ext.Manifest, Icon: ext.Manifest.Icon, Display: map[string]extstore.Display{}}
	for _, lang := range indexLanguages {
		lm := ext.Manifest.Localized(func(key string) string { return ext.Translate(lang, key) })
		meta.Display[lang] = extstore.Display{Name: lm.I18n.DisplayName, Description: lm.I18n.Description}
	}
	return meta, nil
}
