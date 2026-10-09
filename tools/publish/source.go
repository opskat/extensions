package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extstore"
)

// source is one extension's directory in the repository with its manifest.json,
// parsed by the host's own parser so a manifest the app would refuse fails here.
type source struct {
	dir      string
	manifest *extension.Manifest
}

var (
	// semverRe is the version shape extension.ParseManifest holds manifests to.
	semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	// repoNameRe is a registry repository path component (OCI distribution
	// spec); an extension name is pushed as one.
	repoNameRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*$`)
)

// validName fails for an extension name the registry would refuse as a
// repository, though the host's manifest rules accept it (a trailing "-", "_-").
func validName(name string) error {
	if !repoNameRe.MatchString(name) {
		return fmt.Errorf("extension name %q is not a valid registry repository name: "+
			"lowercase letters and digits joined by \".\", \"_\", \"__\" or dashes", name)
	}
	return nil
}

// scanSources reads extensions/*/manifest.json under root, ordered by name. Two
// directories naming the same extension, or a name the registry would refuse,
// fail here — check runs it, so a pull request finds out before the push does.
func scanSources(root string) ([]source, error) {
	entries, err := os.ReadDir(filepath.Join(root, "extensions"))
	if err != nil {
		return nil, err
	}
	var out []source
	dirOf := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, "extensions", entry.Name())
		data, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // the repository's own extensions
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		m, err := extension.ParseManifest(data)
		if err != nil {
			return nil, fmt.Errorf("extensions/%s/manifest.json: %w", entry.Name(), err)
		}
		if err := validName(m.Name); err != nil {
			return nil, fmt.Errorf("extensions/%s/manifest.json: %w", entry.Name(), err)
		}
		if other, dup := dirOf[m.Name]; dup {
			return nil, fmt.Errorf("extensions/%s and extensions/%s both name extension %q", other, entry.Name(), m.Name)
		}
		dirOf[m.Name] = entry.Name()
		out = append(out, source{dir: dir, manifest: m})
	}
	slices.SortFunc(out, func(a, b source) int { return strings.Compare(a.manifest.Name, b.manifest.Name) })
	return out, nil
}

// checkVersions fails when an extension's manifest version is below the highest
// version the index already publishes for it: that release would never reach
// anyone, and is almost always a bad merge.
func checkVersions(idx extstore.Index, sources []source) error {
	var problems []string
	for _, src := range sources {
		ext := findExtension(&idx, src.manifest.Name)
		if ext == nil {
			continue
		}
		if highest := highestVersion(ext); compareVersions(src.manifest.Version, highest) < 0 {
			problems = append(problems, fmt.Sprintf("%s: manifest version %s is below the published %s",
				src.manifest.Name, src.manifest.Version, highest))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// check is the pull-request gate: every manifest parses and no version goes
// backwards. It needs no signing key.
func check(root string) error {
	idx, err := loadIndex(filepath.Join(root, indexFile))
	if err != nil {
		return err
	}
	sources, err := scanSources(root)
	if err != nil {
		return err
	}
	return checkVersions(idx, sources)
}
