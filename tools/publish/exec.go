package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
)

// makeBuilder builds an extension with its own Makefile: `make build` writes the
// installable directory to dist/ (see the repository README).
type makeBuilder struct {
	log io.Writer
}

func (b makeBuilder) Build(ctx context.Context, extDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "make", "-C", extDir, "build")
	cmd.Stdout, cmd.Stderr = b.log, b.log
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("make -C %s build: %w", extDir, err)
	}
	return filepath.Join(extDir, "dist"), nil
}

const (
	// artifactType marks the manifest as an OpsKat extension package.
	artifactType = "application/vnd.opskat.extension.v1"
	// packageMediaType is the media type of the single layer: the package zip.
	packageMediaType = "application/zip"
	// sourceAnnotation links the package to this repository on ghcr.io.
	sourceAnnotation = "org.opencontainers.image.source=https://github.com/opskat/extensions"
)

// orasPusher pushes a package as a single-layer OCI artifact with the oras CLI,
// which must already be logged in to the registry.
type orasPusher struct {
	bin string
	log io.Writer
}

func (p orasPusher) Push(ctx context.Context, ref, file string) error {
	// oras records the file name as the layer title and refuses absolute paths,
	// so it runs next to the file.
	layer := filepath.Base(file) + ":" + packageMediaType
	cmd := exec.CommandContext(ctx, p.bin, "push", //nolint:gosec // fixed argv; ref and file are ours
		"--artifact-type", artifactType,
		"--annotation", sourceAnnotation,
		ref, layer)
	cmd.Dir = filepath.Dir(file)
	cmd.Stdout, cmd.Stderr = p.log, p.log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s push %s: %w", p.bin, ref, err)
	}
	return nil
}
