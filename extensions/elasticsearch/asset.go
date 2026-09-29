package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	opskat "github.com/opskat/opskat/pkg/extsdk"
)

const (
	authNone   = "none"
	authBasic  = "basic"
	authAPIKey = "apiKey"
	authToken  = "token"
)

// esConfig is the asset's configuration form. The three secrets are Credentials:
// the host encrypts them and keeps injecting them through esAuth, so the guest
// only ever learns whether one is set.
type esConfig struct {
	Endpoint string            `json:"endpoint" format:"endpoint" title:"config.endpoint.title" placeholder:"config.endpoint.placeholder" desc:"config.endpoint.desc"`
	AuthType string            `json:"authType,omitempty" enum:"none,basic,apiKey,token" title:"config.authType.title" desc:"config.authType.desc"`
	Username string            `json:"username,omitempty" title:"config.username.title"`
	Password opskat.Credential `json:"password,omitempty" title:"config.password.title"`
	APIKey   opskat.Credential `json:"apiKey,omitempty" title:"config.apiKey.title" desc:"config.apiKey.desc"`
	Token    opskat.Credential `json:"token,omitempty" title:"config.token.title"`
}

// esAuth is what the host injects into every request to the endpoint, chosen by
// authType: no group is selected for "none" (or an unset type), so nothing is sent.
var esAuth = opskat.Auth{
	Selector: "authType",
	Groups: []opskat.AuthGroup{
		{When: authBasic, Bindings: []opskat.AuthBinding{{In: "basic", Value: "{{username}}:{{password}}"}}},
		{When: authAPIKey, Bindings: []opskat.AuthBinding{{In: "header", Name: "Authorization", Value: "ApiKey {{apiKey}}"}}},
		{When: authToken, Bindings: []opskat.AuthBinding{{In: "header", Name: "Authorization", Value: "Bearer {{token}}"}}},
	},
}

// validateConfig checks the config about to be stored. A secret already stored
// and left untouched on edit reaches it as the stored value, and IsSet counts it
// as filled.
func validateConfig(raw json.RawMessage) []opskat.ValidationError {
	var cfg esConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return []opskat.ValidationError{{Message: err.Error()}}
	}
	var errs []opskat.ValidationError
	fail := func(field, msg string) {
		errs = append(errs, opskat.ValidationError{Field: field, Message: msg})
	}

	if u, err := url.Parse(cfg.Endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		fail("endpoint", "endpoint must be an http:// or https:// URL with a host, e.g. https://es.example:9200")
	}
	switch cfg.AuthType {
	case "", authNone:
	case authBasic:
		if cfg.Username == "" {
			fail("username", "username is required for basic authentication")
		}
	case authAPIKey:
		if !cfg.APIKey.IsSet() {
			fail("apiKey", "API key is required for API key authentication")
		}
	case authToken:
		if !cfg.Token.IsSet() {
			fail("token", "token is required for bearer token authentication")
		}
	default:
		fail("authType", fmt.Sprintf("unknown authentication type %q", cfg.AuthType))
	}
	return errs
}

// testConnection asks the cluster for its root document. The host has already
// scoped the request to the endpoint and injected the credentials.
func testConnection(cfg esConfig) error {
	target := strings.TrimRight(cfg.Endpoint, "/") + "/"
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("invalid endpoint: %w", err)
	}
	client := &http.Client{Transport: opskat.NewHTTPTransport()}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Elasticsearch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 == 2 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	reason := errorReason(body)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("authentication failed (HTTP %d): %s", resp.StatusCode, reason)
	}
	return fmt.Errorf("Elasticsearch answered HTTP %d: %s", resp.StatusCode, reason)
}

// errorReason extracts ES's own explanation from an error body: error.reason, a
// bare error string, or the trimmed body when it is neither.
func errorReason(body []byte) string {
	var parsed struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && len(parsed.Error) > 0 {
		var obj struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(parsed.Error, &obj) == nil && obj.Reason != "" {
			return obj.Reason
		}
		var s string
		if json.Unmarshal(parsed.Error, &s) == nil && s != "" {
			return s
		}
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		return text
	}
	return "no details returned"
}
