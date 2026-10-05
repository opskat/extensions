package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extstore"
)

// readSignedIndex reads index.json and index.json.sig, checks the signature the
// way the app does, and parses the index.
func readSignedIndex(t *testing.T, root string, pub ed25519.PublicKey) (extstore.Index, []byte, []byte) {
	t.Helper()
	raw := readFile(t, filepath.Join(root, indexFile))
	sig := readFile(t, filepath.Join(root, signatureFile))
	if err := extstore.Verify(raw, sig, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("app rejects the signature: %v", err)
	}
	idx, err := extstore.ParseIndex(raw)
	if err != nil {
		t.Fatal(err)
	}
	return idx, raw, sig
}

func TestPublishNewVersion(t *testing.T) {
	ctx := context.Background()
	pub, priv := newKey(t)
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	b, p := &fakeBuilder{}, &fakePusher{}

	published, err := newPublisher(root, priv, b, p, testClock).publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(published, []string{"sample:0.2.0"}) {
		t.Fatalf("published = %v", published)
	}

	const ref = "ghcr.io/opskat/extensions/sample:0.2.0"
	pkg, ok := p.pushed[ref]
	if !ok {
		t.Fatalf("pushed %v, want %s", p.order, ref)
	}
	idx, _, _ := readSignedIndex(t, root, pub)
	if len(idx.Extensions) != 1 || len(idx.Extensions[0].Versions) != 1 {
		t.Fatalf("index = %+v", idx)
	}
	ext := idx.Extensions[0]
	sum := sha256.Sum256(pkg)
	want := extstore.Version{
		Version:       "0.2.0",
		HostABI:       "2.2",
		MinAppVersion: "1.14.1",
		Capabilities:  extension.Capabilities{HTTP: extension.HTTPCapability{Allowlist: []string{"https://example.com/"}}},
		Size:          int64(len(pkg)),
		PublishedAt:   testClock,
		Source:        extstore.Source{Type: extstore.SourceOCI, Ref: ref, SHA256: hex.EncodeToString(sum[:])},
	}
	if !reflect.DeepEqual(ext.Versions[0], want) {
		t.Errorf("version entry = %+v\nwant %+v", ext.Versions[0], want)
	}
	if ext.Name != "sample" || ext.Icon != "radar" ||
		ext.Display["en"].Name != "Sample" || ext.Display["zh-CN"].Name != "示例" {
		t.Errorf("extension metadata = %+v", ext)
	}
	if _, err := os.Stat(filepath.Join(root, "extensions", "sample", "dist", "manifest.json")); err != nil {
		t.Errorf("builder output missing: %v", err)
	}
}

func TestPublishSkipsPublishedVersions(t *testing.T) {
	ctx := context.Background()
	pub, priv := newKey(t)
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	if _, err := newPublisher(root, priv, &fakeBuilder{}, &fakePusher{}, testClock).publish(ctx); err != nil {
		t.Fatal(err)
	}
	_, raw, sig := readSignedIndex(t, root, pub)

	b, p := &fakeBuilder{}, &fakePusher{}
	published, err := newPublisher(root, priv, b, p, testClock.Add(time.Hour)).publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 || len(b.built) != 0 || len(p.order) != 0 {
		t.Fatalf("re-run published %v, built %v, pushed %v; want nothing", published, b.built, p.order)
	}
	_, raw2, sig2 := readSignedIndex(t, root, pub)
	if !bytes.Equal(raw, raw2) || !bytes.Equal(sig, sig2) {
		t.Fatal("re-run with nothing new changed index.json or its signature")
	}
}

func TestPublishAppendsAndKeepsEarlierVersions(t *testing.T) {
	ctx := context.Background()
	pub, priv := newKey(t)
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	if _, err := newPublisher(root, priv, &fakeBuilder{}, &fakePusher{}, testClock).publish(ctx); err != nil {
		t.Fatal(err)
	}
	before, _, _ := readSignedIndex(t, root, pub)

	setVersion(t, root, "sample", "0.10.0")
	setVersion(t, root, "alpha", "1.0.0")
	b, p := &fakeBuilder{}, &fakePusher{}
	later := testClock.Add(24 * time.Hour)
	published, err := newPublisher(root, priv, b, p, later).publish(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(published, []string{"alpha:1.0.0", "sample:0.10.0"}) {
		t.Fatalf("published = %v", published)
	}
	after, _, _ := readSignedIndex(t, root, pub)
	if len(after.Extensions) != 2 || after.Extensions[0].Name != "alpha" || after.Extensions[1].Name != "sample" {
		t.Fatalf("extensions = %+v", after.Extensions)
	}
	versions := after.Extensions[1].Versions
	if len(versions) != 2 || versions[1].Version != "0.10.0" || !versions[1].PublishedAt.Equal(later) {
		t.Fatalf("sample versions = %+v", versions)
	}
	if !reflect.DeepEqual(versions[0], before.Extensions[0].Versions[0]) {
		t.Errorf("published 0.2.0 entry changed:\n%+v\nwas %+v", versions[0], before.Extensions[0].Versions[0])
	}
}

func TestPublishFailureWritesNoIndex(t *testing.T) {
	ctx := context.Background()
	_, priv := newKey(t)
	root := newRepo(t, map[string]string{"alpha": "1.0.0", "sample": "0.2.0"})
	p := &fakePusher{failOn: "ghcr.io/opskat/extensions/sample:0.2.0"}

	_, err := newPublisher(root, priv, &fakeBuilder{}, p, testClock).publish(ctx)
	if err == nil || !strings.Contains(err.Error(), "registry refused") {
		t.Fatalf("publish error = %v, want the push failure", err)
	}
	for _, f := range []string{indexFile, signatureFile} {
		if _, err := os.Stat(filepath.Join(root, f)); !os.IsNotExist(err) {
			t.Errorf("%s written after a failed publish (stat: %v)", f, err)
		}
	}
}

func TestPublishFailureLeavesExistingIndexUntouched(t *testing.T) {
	ctx := context.Background()
	pub, priv := newKey(t)
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	if _, err := newPublisher(root, priv, &fakeBuilder{}, &fakePusher{}, testClock).publish(ctx); err != nil {
		t.Fatal(err)
	}
	_, raw, sig := readSignedIndex(t, root, pub)

	setVersion(t, root, "sample", "0.3.0")
	p := &fakePusher{failOn: "ghcr.io/opskat/extensions/sample:0.3.0"}
	if _, err := newPublisher(root, priv, &fakeBuilder{}, p, testClock).publish(ctx); err == nil {
		t.Fatal("publish succeeded although the push failed")
	}
	_, raw2, sig2 := readSignedIndex(t, root, pub)
	if !bytes.Equal(raw, raw2) || !bytes.Equal(sig, sig2) {
		t.Fatal("a failed publish rewrote the index")
	}
}

func TestPublishRejectsVersionBelowPublished(t *testing.T) {
	ctx := context.Background()
	_, priv := newKey(t)
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	writeIndex(t, root, map[string][]string{"sample": {"0.3.0"}})
	b, p := &fakeBuilder{}, &fakePusher{}

	if _, err := newPublisher(root, priv, b, p, testClock).publish(ctx); err == nil {
		t.Fatal("publish accepted a manifest version below the published one")
	}
	if len(b.built) != 0 || len(p.order) != 0 {
		t.Errorf("built %v, pushed %v before rejecting", b.built, p.order)
	}
}
