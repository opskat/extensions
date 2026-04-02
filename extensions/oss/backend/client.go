package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/opskat/extensions/sdk/go/opskat"
)

// OSSConfig is the asset configuration for an S3-compatible storage connection.
type OSSConfig struct {
	Endpoint        string `json:"endpoint"`        // e.g. "https://s3.us-east-1.amazonaws.com"
	PublicEndpoint  string `json:"publicEndpoint"`  // public-facing endpoint for presigned URLs
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`          // default bucket
	PathStyle       bool   `json:"pathStyle"`       // use path-style addressing
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

// newS3Client creates an S3 client for the given config.
// HTTP requests go through opskat.NewHTTPTransport(), enabling SSH tunnel support.
func newS3Client(cfg OSSConfig) *s3.Client {
	httpClient := &http.Client{Transport: opskat.NewHTTPTransport()}

	return s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, "",
		),
		HTTPClient:   httpClient,
		UsePathStyle: cfg.PathStyle,
	})
}

// newS3ClientFromConfig creates an S3 client from a parsed config (for test_connection).
func newS3ClientFromConfig(cfg OSSConfig) (*s3.Client, *OSSConfig, error) {
	return newS3Client(cfg), &cfg, nil
}

// getS3Client reads config from the host and creates an S3 client.
// The host decrypts password fields before returning.
func getS3Client(assetID int64) (*s3.Client, *OSSConfig, error) {
	rawCfg, err := opskat.GetAssetConfig(assetID)
	if err != nil {
		return nil, nil, fmt.Errorf("get asset config: %w", err)
	}
	var cfg OSSConfig
	if err := json.Unmarshal(rawCfg, &cfg); err != nil {
		return nil, nil, fmt.Errorf("parse config: %w", err)
	}

	return newS3Client(cfg), &cfg, nil
}

// assetIDFromArgs extracts the assetId field from action/tool args.
func assetIDFromArgs(args json.RawMessage) int64 {
	var v struct {
		AssetID int64 `json:"assetId"`
	}
	json.Unmarshal(args, &v) //nolint:errcheck
	return v.AssetID
}

// getPresignClient returns an S3 client for presigning URLs.
// If PublicEndpoint is configured, the client uses it so that presigned URLs
// contain the public-facing domain; otherwise it falls back to the regular client.
func getPresignClient(assetID int64) (*s3.Client, *OSSConfig, error) {
	client, cfg, err := getS3Client(assetID)
	if err != nil {
		return nil, nil, err
	}
	if cfg.PublicEndpoint == "" {
		return client, cfg, nil
	}
	// Build a second client that signs against the public endpoint.
	publicCfg := *cfg
	publicCfg.Endpoint = cfg.PublicEndpoint
	return newS3Client(publicCfg), cfg, nil
}

// resolveBucket returns the explicit bucket if non-empty, else the default from config.
func resolveBucket(explicit string, cfg *OSSConfig) string {
	if explicit != "" {
		return explicit
	}
	return cfg.Bucket
}

// s3ctx returns a background context for S3 operations.
func s3ctx() context.Context {
	return context.Background()
}
