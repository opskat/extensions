package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestRunPublishWithoutKeyFails(t *testing.T) {
	cases := map[string]string{"missing": "", "malformed": "not-a-key"}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			root := newRepo(t, map[string]string{"sample": "0.2.0"})
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"publish", "-root", root},
				envOf(map[string]string{signingKeyEnv: key}), &stdout, &stderr)
			if code == 0 {
				t.Fatal("publish exited 0 without a usable signing key")
			}
			if !strings.Contains(stderr.String(), signingKeyEnv) {
				t.Errorf("stderr %q does not name %s", stderr.String(), signingKeyEnv)
			}
			if _, err := os.Stat(filepath.Join(root, indexFile)); !os.IsNotExist(err) {
				t.Errorf("index written without a key (stat: %v)", err)
			}
		})
	}
}

func TestRunCheckExitCode(t *testing.T) {
	root := newRepo(t, map[string]string{"sample": "0.2.0"})
	writeIndex(t, root, map[string][]string{"sample": {"0.3.0"}})
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"check", "-root", root}, envOf(nil), &stdout, &stderr); code == 0 {
		t.Fatal("check exited 0 on a version regression")
	}
	if !strings.Contains(stderr.String(), "0.3.0") {
		t.Errorf("stderr %q does not explain the regression", stderr.String())
	}

	setVersion(t, root, "sample", "0.3.0")
	stderr.Reset()
	if code := run(context.Background(), []string{"check", "-root", root}, envOf(nil), &stdout, &stderr); code != 0 {
		t.Fatalf("check exited %d at the published version: %s", code, stderr.String())
	}
}

func TestKeygenOutputSignsVerifiably(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"keygen"}, envOf(nil), &stdout, &stderr); code != 0 {
		t.Fatalf("keygen exited %d: %s", code, stderr.String())
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if ok {
			fields[k] = v
		}
	}
	priv, err := parseSigningKey(fields["private"])
	if err != nil {
		t.Fatalf("keygen private key does not parse as %s: %v (output %q)", signingKeyEnv, err, stdout.String())
	}
	pub, err := base64.StdEncoding.DecodeString(fields["public"])
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("keygen public key = %q (%v)", fields["public"], err)
	}
	msg := []byte(`{"format":1,"extensions":[]}`)
	if !ed25519.Verify(pub, msg, ed25519.Sign(priv, msg)) {
		t.Fatal("keygen's public key does not verify its private key's signatures")
	}
}
