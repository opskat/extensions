package main

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/opskat/opskat/pkg/extstore"
)

// defaultRegistry is the repository prefix packages are pushed under:
// <registry>/<name>:<version>.
const defaultRegistry = "ghcr.io/opskat/extensions"

// builder turns an extension's source directory into its installable directory.
type builder interface {
	Build(ctx context.Context, extDir string) (distDir string, err error)
}

// pusher stores a package file in the registry under ref.
type pusher interface {
	Push(ctx context.Context, ref, file string) error
}

type publisher struct {
	root     string
	registry string
	key      ed25519.PrivateKey
	builder  builder
	pusher   pusher
	now      func() time.Time
	log      io.Writer
}

// release is one version being published: its package and the index entry that
// will describe it.
type release struct {
	name    string
	meta    packageMeta
	file    string
	version extstore.Version
}

// publish releases every extension whose manifest version is not in the index:
// build, package, describe, push, then append the entries and sign. Versions the
// index already lists are left alone. The index is written only after every push
// succeeded, so a version in the index is always in the registry. It returns the
// "<name>:<version>" pairs it published; with nothing new it writes nothing.
func (p *publisher) publish(ctx context.Context) ([]string, error) {
	idx, err := loadIndex(filepath.Join(p.root, indexFile))
	if err != nil {
		return nil, err
	}
	sources, err := scanSources(p.root)
	if err != nil {
		return nil, err
	}
	if err := checkVersions(idx, sources); err != nil {
		return nil, err
	}

	work, err := os.MkdirTemp("", "publish-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	var releases []release
	for _, src := range sources {
		name, version := src.manifest.Name, src.manifest.Version
		if ext := findExtension(&idx, name); ext != nil && hasVersion(ext, version) {
			fmt.Fprintf(p.log, "%s %s is already published, skipping\n", name, version)
			continue
		}
		fmt.Fprintf(p.log, "building %s %s\n", name, version)
		r, err := p.prepare(ctx, src, work)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", name, version, err)
		}
		releases = append(releases, r)
	}
	if len(releases) == 0 {
		return nil, nil
	}

	var published []string
	for _, r := range releases {
		fmt.Fprintf(p.log, "pushing %s\n", r.version.Source.Ref)
		if err := p.pusher.Push(ctx, r.version.Source.Ref, r.file); err != nil {
			return nil, fmt.Errorf("push %s: %w", r.version.Source.Ref, err)
		}
		addRelease(&idx, r)
		published = append(published, r.name+":"+r.version.Version)
	}
	if err := p.writeIndex(idx); err != nil {
		return nil, err
	}
	return published, nil
}

// prepare builds, packages and describes one extension version.
func (p *publisher) prepare(ctx context.Context, src source, work string) (release, error) {
	name, version := src.manifest.Name, src.manifest.Version
	dist, err := p.builder.Build(ctx, src.dir)
	if err != nil {
		return release{}, fmt.Errorf("build: %w", err)
	}
	file := filepath.Join(work, name+"-"+version+".zip")
	if err := zipDir(dist, file); err != nil {
		return release{}, fmt.Errorf("package: %w", err)
	}
	meta, err := describePackage(ctx, file)
	if err != nil {
		return release{}, err
	}
	if meta.Manifest.Name != name || meta.Manifest.Version != version {
		return release{}, fmt.Errorf("built package is %s %s, the source manifest says %s %s",
			meta.Manifest.Name, meta.Manifest.Version, name, version)
	}
	sum, size, err := fileDigest(file)
	if err != nil {
		return release{}, err
	}
	return release{
		name: name,
		meta: meta,
		file: file,
		version: extstore.Version{
			Version:       version,
			HostABI:       meta.Manifest.HostABI,
			MinAppVersion: meta.Manifest.MinAppVersion,
			Capabilities:  meta.Manifest.Capabilities,
			Size:          size,
			PublishedAt:   p.now().UTC().Truncate(time.Second),
			Source: extstore.Source{
				Type:   extstore.SourceOCI,
				Ref:    fmt.Sprintf("%s/%s:%s", p.registry, name, version),
				SHA256: sum,
			},
		},
	}, nil
}

func hasVersion(ext *extstore.Extension, version string) bool {
	for _, v := range ext.Versions {
		if v.Version == version {
			return true
		}
	}
	return false
}

// addRelease appends r to the index. A release is always newer than everything
// published before it (checkVersions), so its display strings and icon become
// the extension's.
func addRelease(idx *extstore.Index, r release) {
	ext := findExtension(idx, r.name)
	if ext == nil {
		idx.Extensions = append(idx.Extensions, extstore.Extension{Name: r.name})
		ext = &idx.Extensions[len(idx.Extensions)-1]
	}
	ext.Display = r.meta.Display
	ext.Icon = r.meta.Icon
	ext.Versions = append(ext.Versions, r.version)
}

// writeIndex encodes and signs idx and replaces index.json and index.json.sig.
// Both are written to temporary files first, so a failure leaves the old pair.
func (p *publisher) writeIndex(idx extstore.Index) error {
	raw, err := encodeIndex(idx)
	if err != nil {
		return err
	}
	sig := extstore.Sign(raw, p.key)
	files := []struct {
		name string
		data []byte
	}{{indexFile, raw}, {signatureFile, sig}}
	var temps []string
	defer func() {
		for _, t := range temps {
			_ = os.Remove(t)
		}
	}()
	for _, f := range files {
		tmp := filepath.Join(p.root, "."+f.name+".tmp")
		if err := os.WriteFile(tmp, f.data, 0o644); err != nil { //nolint:gosec // published, world-readable by design
			return err
		}
		temps = append(temps, tmp)
	}
	for i, f := range files {
		if err := os.Rename(temps[i], filepath.Join(p.root, f.name)); err != nil {
			return err
		}
	}
	return nil
}
