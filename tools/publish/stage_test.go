package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckRejectsTwoDirectoriesWithTheSameName(t *testing.T) {
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	writeFile(t, filepath.Join(root, "extensions", "sample-copy"), "manifest.json", sampleManifest(t, "sample", "0.3.0"))
	err := check(root)
	if err == nil || !strings.Contains(err.Error(), "sample") {
		t.Fatalf("check = %v, want the duplicate name refused", err)
	}
}

// A name the host accepts but a registry refuses as a repository path component
// would pass review and then fail every publish at push time.
func TestCheckRejectsNamesThatAreNotRegistryRepositories(t *testing.T) {
	for _, name := range []string{"foo-", "a_-b", "a--_b"} {
		t.Run(name, func(t *testing.T) {
			root := newRepo(t, map[string]string{name: "0.1.0"})
			if err := check(root); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("check = %v, want %q refused", err, name)
			}
		})
	}
	root := newRepo(t, map[string]string{"a-b_c": "0.1.0", "a__b": "0.1.0", "a---b": "0.1.0"})
	if err := check(root); err != nil {
		t.Fatalf("check refused valid repository names: %v", err)
	}
}

// Versions order numerically everywhere (check, the app), so 01.0.0 is 1.0.0.
func TestPublishTreatsANumericallyEqualVersionAsPublished(t *testing.T) {
	_, priv := newKey(t)
	root := newRepo(t, map[string]string{"sample": "01.0.0"})
	writeIndex(t, root, map[string][]string{"sample": {"1.0.0"}})
	before := readFile(t, filepath.Join(root, indexFile))
	b, p := &fakeBuilder{}, &fakePusher{}

	published, err := newPublisher(root, priv, b, p, testClock).publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 || len(b.built) != 0 || len(p.order) != 0 {
		t.Fatalf("published %v, built %v, pushed %v; want nothing", published, b.built, p.order)
	}
	if !bytes.Equal(before, readFile(t, filepath.Join(root, indexFile))) {
		t.Fatal("index rewritten")
	}
}

// prepare runs the extension builds and needs neither the signing key nor the
// registry; release pushes and signs what prepare staged and builds nothing.
func TestPrepareThenReleaseSplitsBuildingFromSigning(t *testing.T) {
	ctx := context.Background()
	pub, priv := newKey(t)
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	out := t.TempDir()

	b := &fakeBuilder{}
	stager := &publisher{root: root, builder: b, now: func() time.Time { return testClock }, log: io.Discard}
	if err := stager.prepare(ctx, out); err != nil {
		t.Fatal(err)
	}
	if len(b.built) != 1 {
		t.Fatalf("built %v", b.built)
	}
	if _, err := os.Stat(filepath.Join(root, indexFile)); !os.IsNotExist(err) {
		t.Fatalf("prepare wrote the index (stat: %v)", err)
	}

	p := &fakePusher{}
	releaser := &publisher{root: root, registry: defaultRegistry, key: priv, pusher: p, log: io.Discard}
	published, err := releaser.release(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	const ref = "ghcr.io/opskat/extensions/sample:0.2.0"
	if len(published) != 1 || published[0] != "sample:0.2.0" || len(p.order) != 1 || p.order[0] != ref {
		t.Fatalf("published %v, pushed %v", published, p.order)
	}
	idx, _, _ := readSignedIndex(t, root, pub)
	v := idx.Extensions[0].Versions[0]
	sum := sha256.Sum256(p.pushed[ref])
	if v.Source.SHA256 != hex.EncodeToString(sum[:]) || v.Size != int64(len(p.pushed[ref])) || v.Source.Ref != ref {
		t.Fatalf("index entry %+v does not describe the pushed bytes", v)
	}
	if !v.PublishedAt.Equal(testClock) || idx.Extensions[0].Display["zh-CN"].Name != "示例" {
		t.Fatalf("index entry lost what prepare described: %+v", idx.Extensions[0])
	}
}

// A run that staged packages from an older commit may release after a newer run
// already published: the index has moved on and nothing is pushed or rewritten.
func TestReleaseSkipsWhatTheIndexAlreadyHas(t *testing.T) {
	ctx := context.Background()
	_, priv := newKey(t)
	root := newRepo(t, map[string]string{"sample": "0.2.0", "alpha": "1.0.0"})
	out := t.TempDir()
	stager := &publisher{root: root, builder: &fakeBuilder{}, now: func() time.Time { return testClock }, log: io.Discard}
	if err := stager.prepare(ctx, out); err != nil {
		t.Fatal(err)
	}
	writeIndex(t, root, map[string][]string{"sample": {"0.3.0"}, "alpha": {"1.0.0"}})
	before := readFile(t, filepath.Join(root, indexFile))

	p := &fakePusher{}
	releaser := &publisher{root: root, registry: defaultRegistry, key: priv, pusher: p, log: io.Discard}
	published, err := releaser.release(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 || len(p.order) != 0 {
		t.Fatalf("published %v, pushed %v; want nothing", published, p.order)
	}
	if !bytes.Equal(before, readFile(t, filepath.Join(root, indexFile))) {
		t.Fatal("index rewritten")
	}
}

func TestRunPrepareNeedsNoKeyAndReleaseDoes(t *testing.T) {
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	// No extensions to build: prepare still stages an (empty) release list.
	if err := os.RemoveAll(filepath.Join(root, "extensions", "sample")); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), []string{"prepare", "-root", root, "-out", out}, envOf(nil), &stdout, &stderr); code != 0 {
		t.Fatalf("prepare exited %d: %s", code, stderr.String())
	}
	stderr.Reset()
	if code := run(context.Background(), []string{"release", "-root", root, "-in", out}, envOf(nil), &stdout, &stderr); code == 0 {
		t.Fatal("release exited 0 without a signing key")
	}
	if !strings.Contains(stderr.String(), signingKeyEnv) {
		t.Errorf("stderr %q does not name %s", stderr.String(), signingKeyEnv)
	}
}
