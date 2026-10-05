package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sampleWasm is testdata/sample built once for the whole test binary: every test
// that needs a real package copies it instead of compiling the guest again.
var sampleWasm string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "publish-test-wasm-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	sampleWasm = filepath.Join(dir, "main.wasm")
	build := exec.Command("go", "build", "-buildmode=c-shared", "-o", sampleWasm, ".")
	build.Dir = filepath.Join("testdata", "sample")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build sample extension: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// writeFile writes content to root/rel, creating parent directories.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// sampleManifest is testdata/sample/manifest.json renamed and re-versioned.
func sampleManifest(t *testing.T, name, version string) string {
	t.Helper()
	m := string(readFile(t, filepath.Join("testdata", "sample", "manifest.json")))
	m = strings.Replace(m, `"name": "sample"`, fmt.Sprintf(`"name": %q`, name), 1)
	return strings.Replace(m, `"version": "0.2.0"`, fmt.Sprintf(`"version": %q`, version), 1)
}

// newRepo lays out an extensions repo whose extensions are copies of the sample,
// keyed by name with their manifest versions.
func newRepo(t *testing.T, versions map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, version := range versions {
		setVersion(t, root, name, version)
	}
	return root
}

// setVersion writes (or rewrites) extensions/<name> at version.
func setVersion(t *testing.T, root, name, version string) {
	t.Helper()
	dir := filepath.Join(root, "extensions", name)
	writeFile(t, dir, "manifest.json", sampleManifest(t, name, version))
	for _, lang := range []string{"en", "zh-CN"} {
		rel := filepath.Join("locales", lang+".json")
		writeFile(t, dir, rel, string(readFile(t, filepath.Join("testdata", "sample", rel))))
	}
}

// fakeBuilder produces dist/ the way the extension Makefiles do — main.wasm,
// manifest.json, locales/ — from the prebuilt sample guest.
type fakeBuilder struct {
	mu    sync.Mutex
	built []string
}

func (b *fakeBuilder) Build(_ context.Context, extDir string) (string, error) {
	b.mu.Lock()
	b.built = append(b.built, filepath.Base(extDir))
	b.mu.Unlock()
	dist := filepath.Join(extDir, "dist")
	if err := os.RemoveAll(dist); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dist, "locales"), 0o755); err != nil {
		return "", err
	}
	copies := map[string]string{
		sampleWasm:                                     "main.wasm",
		filepath.Join(extDir, "manifest.json"):         "manifest.json",
		filepath.Join(extDir, "locales", "en.json"):    filepath.Join("locales", "en.json"),
		filepath.Join(extDir, "locales", "zh-CN.json"): filepath.Join("locales", "zh-CN.json"),
	}
	for src, rel := range copies {
		data, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dist, rel), data, 0o644); err != nil {
			return "", err
		}
	}
	return dist, nil
}

// fakePusher records what would have been pushed. failOn makes the push of that
// ref fail.
type fakePusher struct {
	pushed map[string][]byte
	order  []string
	failOn string
}

func (p *fakePusher) Push(_ context.Context, ref, file string) error {
	if ref == p.failOn {
		return fmt.Errorf("registry refused %s", ref)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if p.pushed == nil {
		p.pushed = map[string][]byte{}
	}
	p.pushed[ref] = data
	p.order = append(p.order, ref)
	return nil
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

var testClock = time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC)

func newPublisher(root string, key ed25519.PrivateKey, b *fakeBuilder, p *fakePusher, now time.Time) *publisher {
	return &publisher{
		root:     root,
		registry: defaultRegistry,
		key:      key,
		builder:  b,
		pusher:   p,
		now:      func() time.Time { return now },
		log:      io.Discard,
	}
}
