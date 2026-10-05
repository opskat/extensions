package main

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/extstore"
)

func TestEncodeIndexIsDeterministic(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	v := func(version string) extstore.Version {
		return extstore.Version{Version: version, HostABI: "2.2", PublishedAt: at,
			Source: extstore.Source{Type: extstore.SourceOCI, Ref: "r:" + version, SHA256: "00"}}
	}
	shuffled := extstore.Index{Format: extstore.FormatVersion, Extensions: []extstore.Extension{
		{Name: "zeta", Display: map[string]extstore.Display{"zh-CN": {Name: "泽"}, "en": {Name: "Zeta"}},
			Versions: []extstore.Version{v("0.10.0"), v("0.9.0"), v("0.9.1")}},
		{Name: "alpha", Versions: []extstore.Version{v("1.0.0")}},
	}}
	sorted := extstore.Index{Format: extstore.FormatVersion, Extensions: []extstore.Extension{
		{Name: "alpha", Versions: []extstore.Version{v("1.0.0")}},
		{Name: "zeta", Display: map[string]extstore.Display{"en": {Name: "Zeta"}, "zh-CN": {Name: "泽"}},
			Versions: []extstore.Version{v("0.9.0"), v("0.9.1"), v("0.10.0")}},
	}}

	a, err := encodeIndex(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodeIndex(sorted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("same index in different order encoded differently:\n%s\n---\n%s", a, b)
	}
	parsed, err := extstore.ParseIndex(a)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, sorted) {
		t.Fatalf("round trip = %+v, want %+v", parsed, sorted)
	}
	// Re-encoding what was read back is byte-identical, so an unchanged index
	// never produces a diff.
	again, err := encodeIndex(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, again) {
		t.Fatalf("re-encoding changed the bytes:\n%s\n---\n%s", a, again)
	}
}

func TestLoadIndexMissingIsEmpty(t *testing.T) {
	idx, err := loadIndex(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Format != extstore.FormatVersion || len(idx.Extensions) != 0 {
		t.Fatalf("missing index = %+v, want empty format-%d index", idx, extstore.FormatVersion)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.10.0", "0.9.0", 1},
		{"0.9.0", "0.10.0", -1},
		{"1.2.3", "1.2.3", 0},
		{"2.0.0", "1.99.99", 1},
		{"1.0.9", "1.0.10", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
