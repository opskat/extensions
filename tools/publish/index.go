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

// encodeIndex is the one serialization of an index: extensions by name, versions
// oldest first, map keys sorted by encoding/json, two-space indent and a final
// newline. Equal indexes therefore encode to equal bytes, so the signature and
// the committed file only change when the content does.
func encodeIndex(idx extstore.Index) ([]byte, error) {
	exts := slices.Clone(idx.Extensions)
	if exts == nil {
		exts = []extstore.Extension{}
	}
	slices.SortFunc(exts, func(a, b extstore.Extension) int { return strings.Compare(a.Name, b.Name) })
	for i := range exts {
		exts[i].Versions = slices.Clone(exts[i].Versions)
		slices.SortFunc(exts[i].Versions, func(a, b extstore.Version) int { return compareVersions(a.Version, b.Version) })
	}
	idx.Extensions = exts
	raw, err := json.MarshalIndent(idx, "", "  ")
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
