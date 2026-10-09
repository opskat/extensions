package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
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
	display map[string]extstore.Display
	icon    string
	file    string
	version extstore.Version
}

// publish releases every extension whose manifest version is not in the index:
// prepare (build, package, describe) into a temporary directory, then release
// (push, append the entries and sign). Versions the index already lists are left
// alone. It returns the "<name>:<version>" pairs it published; with nothing new
// it writes nothing.
//
// CI runs the two halves as separate jobs so the extension builds never share a
// machine with the signing key; publish is both on one machine, for a maintainer.
func (p *publisher) publish(ctx context.Context) ([]string, error) {
	staged, err := os.MkdirTemp("", "publish-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staged)
	if err := p.prepare(ctx, staged); err != nil {
		return nil, err
	}
	return p.release(ctx, staged)
}

// stagedRelease is one prepared version as releases.json records it. The
// package is <name>-<version>.zip beside it; its ref, sha256 and size are filled
// in by release from the file itself.
type stagedRelease struct {
	Name    string                      `json:"name"`
	Display map[string]extstore.Display `json:"display"`
	Icon    string                      `json:"icon"`
	Version extstore.Version            `json:"version"`
}

// releasesFile lists the prepared versions in the staging directory.
const releasesFile = "releases.json"

func packageFile(dir, name, version string) string {
	return filepath.Join(dir, name+"-"+version+".zip")
}

// prepare builds, packages and describes every extension whose manifest version
// is not in the index into out: one zip per version plus releases.json (an empty
// list when there is nothing new). It needs no key and touches no registry.
func (p *publisher) prepare(ctx context.Context, out string) error {
	idx, err := loadIndex(filepath.Join(p.root, indexFile))
	if err != nil {
		return err
	}
	sources, err := scanSources(p.root)
	if err != nil {
		return err
	}
	if err := checkVersions(idx, sources); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	releases := []stagedRelease{}
	for _, src := range sources {
		name, version := src.manifest.Name, src.manifest.Version
		if ext := findExtension(&idx, name); ext != nil && hasVersion(ext, version) {
			fmt.Fprintf(p.log, "%s %s is already published, skipping\n", name, version)
			continue
		}
		fmt.Fprintf(p.log, "building %s %s\n", name, version)
		r, err := p.stage(ctx, src, out)
		if err != nil {
			return fmt.Errorf("%s %s: %w", name, version, err)
		}
		releases = append(releases, r)
	}
	data, err := json.MarshalIndent(releases, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, releasesFile), data, 0o644) //nolint:gosec // build output, not secret
}

// release pushes what prepare staged in dir and appends it to the index, then
// signs. A staged version the index already has, or one below its newest (a run
// staged from an older commit releasing after a newer one), is skipped. The
// index is written only after every push succeeded, so a version in the index is
// always in the registry.
func (p *publisher) release(ctx context.Context, dir string) ([]string, error) {
	idx, err := loadIndex(filepath.Join(p.root, indexFile))
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, releasesFile)) //nolint:gosec // the staging directory we were given
	if err != nil {
		return nil, err
	}
	var staged []stagedRelease
	if err := json.Unmarshal(data, &staged); err != nil {
		return nil, fmt.Errorf("%s: %w", releasesFile, err)
	}

	var releases []release
	for _, s := range staged {
		name, version := s.Name, s.Version.Version
		if err := validName(name); err != nil {
			return nil, fmt.Errorf("%s: %w", releasesFile, err)
		}
		if !semverRe.MatchString(version) {
			return nil, fmt.Errorf("%s: %s has version %q, want MAJOR.MINOR.PATCH", releasesFile, name, version)
		}
		if ext := findExtension(&idx, name); ext != nil &&
			(hasVersion(ext, version) || compareVersions(version, highestVersion(ext)) < 0) {
			fmt.Fprintf(p.log, "%s %s: the index already has it or a newer version, skipping\n", name, version)
			continue
		}
		file := packageFile(dir, name, version)
		sum, size, err := fileDigest(file)
		if err != nil {
			return nil, err
		}
		v := s.Version
		v.Size = size
		v.Source = extstore.Source{Type: extstore.SourceOCI, Ref: fmt.Sprintf("%s/%s:%s", p.registry, name, version), SHA256: sum}
		releases = append(releases, release{name: name, display: s.Display, icon: s.Icon, file: file, version: v})
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

// stage builds, packages and describes one extension version into out.
func (p *publisher) stage(ctx context.Context, src source, out string) (stagedRelease, error) {
	name, version := src.manifest.Name, src.manifest.Version
	dist, err := p.builder.Build(ctx, src.dir)
	if err != nil {
		return stagedRelease{}, fmt.Errorf("build: %w", err)
	}
	file := packageFile(out, name, version)
	if err := zipDir(dist, file); err != nil {
		return stagedRelease{}, fmt.Errorf("package: %w", err)
	}
	meta, err := describePackage(ctx, file)
	if err != nil {
		return stagedRelease{}, err
	}
	if meta.Manifest.Name != name || meta.Manifest.Version != version {
		return stagedRelease{}, fmt.Errorf("built package is %s %s, the source manifest says %s %s",
			meta.Manifest.Name, meta.Manifest.Version, name, version)
	}
	return stagedRelease{
		Name:    name,
		Display: meta.Display,
		Icon:    meta.Icon,
		Version: extstore.Version{
			Version:       version,
			HostABI:       meta.Manifest.HostABI,
			MinAppVersion: meta.Manifest.MinAppVersion,
			Capabilities:  meta.Manifest.Capabilities,
			PublishedAt:   p.now().UTC().Truncate(time.Second),
		},
	}, nil
}

// hasVersion reports whether ext publishes version, by the same numeric order
// check and the app use (01.0.0 is 1.0.0).
func hasVersion(ext *extstore.Extension, version string) bool {
	for _, v := range ext.Versions {
		if compareVersions(v.Version, version) == 0 {
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
	ext.Display = r.display
	ext.Icon = r.icon
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
