package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/opskat/opskat/pkg/extstore"
)

const (
	// indexFile and signatureFile live at the repository root; the app fetches
	// them through raw links.
	indexFile     = "index.json"
	signatureFile = "index.json.sig"
)

// loadIndex reads index.json. Before the first release there is none: that is an
// empty index, not an error.
func loadIndex(path string) (extstore.Index, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the repository's own index
	if errors.Is(err, os.ErrNotExist) {
		return extstore.Index{Format: extstore.FormatVersion, Extensions: []extstore.Extension{}}, nil
	}
	if err != nil {
		return extstore.Index{}, err
	}
	idx, err := extstore.ParseIndex(raw)
	if err != nil {
		return extstore.Index{}, fmt.Errorf("%s: %w", path, err)
	}
	return idx, nil
}

// publishedEntries are the version entries index.json already publishes, as
// published, keyed by publishedKey. An entry carries fields of the app it was
// written for (a 2.x extension's "capabilities") that extstore.Version no longer
// models; the apps of that time still read and install by them.
type publishedEntries map[string]json.RawMessage

func publishedKey(name, version string) string { return name + "@" + version }

// loadPublishedEntries reads the version entries of index.json byte for byte. No
// index yet is no entries.
func loadPublishedEntries(path string) (publishedEntries, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the repository's own index
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var idx struct {
		Extensions []struct {
			Name     string            `json:"name"`
			Versions []json.RawMessage `json:"versions"`
		} `json:"extensions"`
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := publishedEntries{}
	for _, ext := range idx.Extensions {
		for _, v := range ext.Versions {
			var head struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal(v, &head); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", path, ext.Name, err)
			}
			out[publishedKey(ext.Name, head.Version)] = v
		}
	}
	return out, nil
}

// indexOut and extensionOut encode an index whose version entries are already
// JSON: the outer Versions / Extensions fields shadow the embedded ones, so
// every other field is extstore's own.
type indexOut struct {
	extstore.Index
	Extensions []extensionOut `json:"extensions"`
}

type extensionOut struct {
	extstore.Extension
	Versions []json.RawMessage `json:"versions"`
}

// encodeIndex is the one serialization of an index: extensions by name, versions
// oldest first, map keys sorted by encoding/json, two-space indent and a final
// newline. A version kept in published is written exactly as published; the
// others are encoded from idx. Equal indexes therefore encode to equal bytes, so
// the signature and the committed file only change when the content does.
func encodeIndex(idx extstore.Index, published publishedEntries) ([]byte, error) {
	exts := slices.Clone(idx.Extensions)
	if exts == nil {
		exts = []extstore.Extension{}
	}
	slices.SortFunc(exts, func(a, b extstore.Extension) int { return strings.Compare(a.Name, b.Name) })
	out := indexOut{Index: idx, Extensions: make([]extensionOut, 0, len(exts))}
	for _, ext := range exts {
		versions := slices.Clone(ext.Versions)
		slices.SortFunc(versions, func(a, b extstore.Version) int { return compareVersions(a.Version, b.Version) })
		e := extensionOut{Extension: ext, Versions: make([]json.RawMessage, 0, len(versions))}
		for _, v := range versions {
			raw, ok := published[publishedKey(ext.Name, v.Version)]
			if !ok {
				var err error
				if raw, err = json.Marshal(v); err != nil {
					return nil, err
				}
			}
			e.Versions = append(e.Versions, raw)
		}
		out.Extensions = append(out.Extensions, e)
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// findExtension returns the index entry for name, or nil.
func findExtension(idx *extstore.Index, name string) *extstore.Extension {
	for i := range idx.Extensions {
		if idx.Extensions[i].Name == name {
			return &idx.Extensions[i]
		}
	}
	return nil
}

// highestVersion is the newest version published for ext.
func highestVersion(ext *extstore.Extension) string {
	var best string
	for _, v := range ext.Versions {
		if best == "" || compareVersions(v.Version, best) > 0 {
			best = v.Version
		}
	}
	return best
}

// compareVersions orders two MAJOR.MINOR.PATCH versions numerically. Every
// version here comes from a manifest.json, which extension.ParseManifest holds to
// exactly that shape (semverRe).
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(pa), len(pb)) {
		na, _ := strconv.Atoi(pa[i])
		nb, _ := strconv.Atoi(pb[i])
		if c := cmp.Compare(na, nb); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(pa), len(pb))
}
