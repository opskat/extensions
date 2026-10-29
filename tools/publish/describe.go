package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extstore"
	"github.com/opskat/opskat/pkg/extwire"
)

// indexLanguages are the languages the index carries display strings for.
var indexLanguages = []string{"en", "zh-CN"}

// packageMeta is what the index says about one package, as the app sees it.
type packageMeta struct {
	// Manifest is manifest.json's security contract.
	Manifest *extmanifest.Manifest
	Display  map[string]extstore.Display
	Icon     string
}

// wirePoint is a contribution point that holds an extension's entry to its wire
// shape (pkg/extwire, the same types the guest SDK encodes) and nothing more:
// the subsystem rules belong to the app and cannot be imported from here. What
// it guarantees is what the runtime guarantees about keys — every key describe()
// answers has a point — plus that the entry decodes strictly.
type wirePoint struct {
	key   string
	check func(v extension.ExtensionView, raw json.RawMessage) error
}

func (p wirePoint) Key() string { return p.key }

func (p wirePoint) Check(v extension.ExtensionView) error {
	raw := v.Contribution(p.key)
	if raw == nil && p.key != extwire.KeyDatabaseDrivers {
		return nil
	}
	if err := p.check(v, raw); err != nil {
		return fmt.Errorf("contributes.%s: %w", p.key, err)
	}
	return nil
}

func (p wirePoint) Attach(extension.ExtensionView) (func(), error) { return func() {}, nil }

func decodeInto[T any](_ extension.ExtensionView, raw json.RawMessage) error {
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(&v)
}

// checkDrivers also holds describe()'s drivers to the ids manifest.json declares,
// the one cross-contribution rule that decides whether the app would load a
// driver package at all.
func checkDrivers(v extension.ExtensionView, raw json.RawMessage) error {
	var descs []dbdriver.Descriptor
	if raw != nil {
		if err := json.Unmarshal(raw, &descs); err != nil {
			return err
		}
	}
	var got []string
	for _, d := range descs {
		got = append(got, string(d.ID))
	}
	want := slices.Clone(v.Manifest().Contributes.DatabaseDrivers)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("describe() provides drivers %v, manifest.json declares %v", got, want)
	}
	return nil
}

// newContributionRegistry is the registry describing runs the extension under:
// one wire-shape point per contributes key.
func newContributionRegistry() *extension.ContributionRegistry {
	return extension.NewContributionRegistry(
		wirePoint{extwire.KeyAssetTypes, decodeInto[[]extwire.AssetType]},
		wirePoint{extwire.KeyTools, decodeInto[[]extwire.Tool]},
		wirePoint{extwire.KeyPolicy, decodeInto[extwire.Policy]},
		wirePoint{extwire.KeySnippets, decodeInto[extwire.Snippets]},
		wirePoint{extwire.KeyPages, decodeInto[extwire.Pages]},
		wirePoint{extwire.KeyDatabaseDrivers, checkDrivers},
	)
}

// describePackage stages the package zip exactly as the app's install does —
// unzip, parse manifest.json, load the WASM, run describe() — and reads the
// metadata back the way the extension settings show it. A package whose manifest,
// describe() wire shape or driver ids the app would refuse fails here, before
// anything is pushed — the wire shape checked strictly (wirePoint); the app's own
// per-contribution rules (internal/extreg) apply only when the app loads it.
func describePackage(ctx context.Context, zipPath string) (packageMeta, error) {
	dir, err := os.MkdirTemp("", "publish-stage-")
	if err != nil {
		return packageMeta{}, err
	}
	defer os.RemoveAll(dir)

	mgr := extension.NewManager(dir, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{})
	}, newContributionRegistry(), zap.NewNop())
	defer mgr.Close(ctx)
	staged, err := mgr.Stage(ctx, zipPath)
	if err != nil {
		return packageMeta{}, fmt.Errorf("load package as the app would: %w", err)
	}
	defer staged.Abort(ctx)

	ext := staged.Extension()
	meta := packageMeta{Manifest: &ext.Manifest.Manifest, Icon: ext.Manifest.Icon, Display: map[string]extstore.Display{}}
	for _, lang := range indexLanguages {
		meta.Display[lang] = extstore.Display{
			Name:        ext.Translate(lang, ext.Manifest.I18n.DisplayName),
			Description: ext.Translate(lang, ext.Manifest.I18n.Description),
		}
	}
	return meta, nil
}
