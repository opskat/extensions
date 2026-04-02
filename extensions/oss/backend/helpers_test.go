package main

import (
	"net/http"

	"github.com/opskat/extensions/sdk/go/opskat"
)

// newTestS3Host creates a TestHost configured with mock S3 config (including credentials).
func newTestS3Host(handler http.HandlerFunc) *opskat.TestHost {
	return opskat.NewTestHost(
		opskat.WithAssetConfig(0, OSSConfig{
			Endpoint:        "http://s3.test.local",
			Region:          "us-east-1",
			Bucket:          "default-bucket",
			PathStyle:       true,
			AccessKeyID:     "AKID",
			SecretAccessKey: "SECRET",
		}),
		opskat.WithMockHTTP(handler),
	)
}
