package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// signingKeyEnv holds the index signing key: the standard base64 of a 32-byte
// ed25519 seed, as `keygen` prints it. In CI it comes from the repository secret
// of the same name.
const signingKeyEnv = "EXTENSION_INDEX_SIGNING_KEY"

// parseSigningKey decodes the value of signingKeyEnv.
func parseSigningKey(value string) (ed25519.PrivateKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("%s is not set: publishing signs index.json and needs the ed25519 private key (see README, \"Maintainer setup\")", signingKeyEnv)
	}
	seed, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s must be the standard base64 of a %d-byte ed25519 seed, as `keygen` prints it", signingKeyEnv, ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// encodeKeyPair is the text form keygen prints and parseSigningKey reads back.
func encodeKeyPair(priv ed25519.PrivateKey) (private, public string) {
	return base64.StdEncoding.EncodeToString(priv.Seed()),
		base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
}
