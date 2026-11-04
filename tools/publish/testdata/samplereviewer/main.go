// Command samplereviewer is the hostABI 1.0 command reviewer extension the
// publisher's tests package: a real WASM guest whose describe() contributes
// settings and a command reviewer, the two points the app added for reviewers.
// It never reviews anything — packaging only runs describe().
package main

import (
	opskat "github.com/opskat/opskat/pkg/extsdk"
)

func main() {}

type settings struct {
	APIKey  string `json:"api_key" title:"API key" format:"password"`
	BaseURL string `json:"base_url" title:"Service URL" format:"endpoint" default:"https://example.com"`
}

func init() {
	opskat.Extension(opskat.Meta{Icon: "shield", DisplayName: "extension.displayName", Description: "extension.description"})
	opskat.Settings[settings]().Auth(opskat.Auth{Groups: []opskat.AuthGroup{{
		Bindings: []opskat.AuthBinding{{In: "header", Name: "Authorization", Value: "Bearer {{api_key}}"}},
	}}})
	opskat.CommandReviewer([]opskat.ReviewQuestion{
		{ID: "destructive", Names: map[string]string{"en": "may destroy data", "zh-CN": "可能破坏数据"}},
	}, 4000, func(*opskat.ToolContext, opskat.ReviewItem) opskat.ReviewResult {
		return opskat.ReviewResult{Outcome: opskat.ReviewPass}
	})
}
