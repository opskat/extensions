// Command publish releases the extensions in this repository to the OpsKat
// extension store.
//
//	publish check   [-root DIR]                       pull-request gate, no key needed
//	publish publish [-root DIR] [-registry R] [-oras BIN]
//	publish keygen                                    new index signing key pair
//
// check fails when an extension's manifest version is below the highest version
// index.json already publishes for it. publish builds every extension whose
// manifest version is not in index.json yet, pushes each package to
// <registry>/<name>:<version>, appends the versions to index.json and signs it
// into index.json.sig with the key in $EXTENSION_INDEX_SIGNING_KEY. Any failure
// exits non-zero with index.json untouched.
//
// Index metadata (display strings, icon, capabilities, hostABI, minAppVersion) is
// read by loading each package through OpsKat's own extension loader, so the
// store shows exactly what the app sees once the extension is installed.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"
)

const usage = `usage:
  publish check   [-root DIR]
  publish publish [-root DIR] [-registry REGISTRY] [-oras BIN]
  publish keygen
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is the whole command line, with its environment passed in.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root (holds extensions/ and index.json)")

	switch args[0] {
	case "check":
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if err := check(*root); err != nil {
			fmt.Fprintf(stderr, "check failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "extension versions ok")
		return 0

	case "publish":
		registry := flags.String("registry", defaultRegistry, "registry repository prefix packages are pushed under")
		oras := flags.String("oras", "oras", "oras CLI used to push packages")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		key, err := parseSigningKey(getenv(signingKeyEnv))
		if err != nil {
			fmt.Fprintf(stderr, "publish failed: %v\n", err)
			return 1
		}
		p := &publisher{
			root:     *root,
			registry: *registry,
			key:      key,
			builder:  makeBuilder{log: stderr},
			pusher:   orasPusher{bin: *oras, log: stderr},
			now:      time.Now,
			log:      stderr,
		}
		published, err := p.publish(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "publish failed: %v\n", err)
			return 1
		}
		if len(published) == 0 {
			fmt.Fprintln(stderr, "nothing to publish")
		}
		for _, r := range published {
			fmt.Fprintln(stdout, r)
		}
		return 0

	case "keygen":
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fmt.Fprintf(stderr, "keygen failed: %v\n", err)
			return 1
		}
		private, public := encodeKeyPair(priv)
		fmt.Fprintf(stdout, "private: %s\npublic: %s\n", private, public)
		return 0

	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
}
