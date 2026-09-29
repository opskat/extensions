package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	opskat "github.com/opskat/opskat/pkg/extsdk"

	. "github.com/smartystreets/goconvey/convey"
)

// validate runs the config validator the way the host does: over the JSON the
// asset is about to be stored as. A secret the user did not touch on edit is the
// stored (encrypted) string, so it looks exactly like a freshly typed one.
func validate(t *testing.T, cfg string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range validateConfig(json.RawMessage(cfg)) {
		out[e.Field] = e.Message
	}
	return out
}

func TestValidateConfig(t *testing.T) {
	Convey("config validation", t, func() {
		Convey("a plain https endpoint without auth is valid", func() {
			So(validate(t, `{"endpoint":"https://es.example:9200"}`), ShouldBeEmpty)
		})
		Convey("an endpoint may carry a path prefix", func() {
			So(validate(t, `{"endpoint":"https://gw.example/es","authType":"none"}`), ShouldBeEmpty)
		})
		Convey("the endpoint must be an http(s) URL with a host", func() {
			for _, ep := range []string{``, `es.example:9200`, `ftp://es.example`, `https://`, `http:///path`} {
				cfg, _ := json.Marshal(map[string]string{"endpoint": ep})
				errs := validate(t, string(cfg))
				So(errs, ShouldContainKey, "endpoint")
				So(len(errs), ShouldEqual, 1)
			}
		})
		Convey("an unknown auth type is refused on authType", func() {
			So(validate(t, `{"endpoint":"http://es:9200","authType":"kerberos"}`), ShouldContainKey, "authType")
		})
		Convey("basic auth needs a username, and the password may be empty", func() {
			errs := validate(t, `{"endpoint":"http://es:9200","authType":"basic"}`)
			So(errs, ShouldContainKey, "username")
			So(len(errs), ShouldEqual, 1)
			So(validate(t, `{"endpoint":"http://es:9200","authType":"basic","username":"elastic"}`), ShouldBeEmpty)
		})
		Convey("apiKey and token need their secret", func() {
			So(validate(t, `{"endpoint":"http://es:9200","authType":"apiKey"}`), ShouldContainKey, "apiKey")
			So(validate(t, `{"endpoint":"http://es:9200","authType":"token"}`), ShouldContainKey, "token")
			So(validate(t, `{"endpoint":"http://es:9200","authType":"apiKey","apiKey":""}`), ShouldContainKey, "apiKey")
			So(validate(t, `{"endpoint":"http://es:9200","authType":"apiKey","apiKey":"abc=="}`), ShouldBeEmpty)
			So(validate(t, `{"endpoint":"http://es:9200","authType":"token","token":"t"}`), ShouldBeEmpty)
		})
		Convey("an untouched stored secret counts as filled", func() {
			handle := `{"__credential_handle":"h1"}`
			So(validate(t, `{"endpoint":"http://es:9200","authType":"apiKey","apiKey":`+handle+`}`), ShouldBeEmpty)
			So(validate(t, `{"endpoint":"http://es:9200","authType":"token","token":`+handle+`}`), ShouldBeEmpty)
		})
		Convey("a secret of another auth type does not satisfy the selected one", func() {
			So(validate(t, `{"endpoint":"http://es:9200","authType":"token","apiKey":"x"}`), ShouldContainKey, "token")
		})
		Convey("every problem is reported at once, per field", func() {
			errs := validate(t, `{"endpoint":"nope","authType":"basic"}`)
			So(errs, ShouldContainKey, "endpoint")
			So(errs, ShouldContainKey, "username")
		})
		Convey("malformed JSON is reported rather than validated as empty", func() {
			So(validateConfig(json.RawMessage(`{`)), ShouldNotBeEmpty)
		})
	})
}

// esReply mocks the cluster's answer to the test-connection request.
func esReply(status int, body string, seen *http.Request) opskat.TestOption {
	return opskat.WithMockHTTP(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func cfgOf(t *testing.T, raw string) esConfig {
	t.Helper()
	var cfg esConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestTestConnection(t *testing.T) {
	Convey("test connection", t, func() {
		cfg := cfgOf(t, `{"endpoint":"https://es.example:9200/prefix/"}`)

		Convey("a 2xx answer to GET / (under the path prefix) is success", func() {
			var seen http.Request
			host := opskat.NewTestHost(esReply(200, `{"version":{"number":"8.19.0"}}`, &seen))
			defer host.Close()
			So(testConnection(cfg), ShouldBeNil)
			So(seen.Method, ShouldEqual, "GET")
			So(seen.URL.Path, ShouldEqual, "/prefix/")
		})
		Convey("401 and 403 are authentication failures carrying ES's reason", func() {
			for _, status := range []int{401, 403} {
				host := opskat.NewTestHost(esReply(status,
					`{"error":{"type":"security_exception","reason":"missing authentication credentials for REST request [/]"},"status":401}`, nil))
				err := testConnection(cfg)
				host.Close()
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "authentication failed")
				So(err.Error(), ShouldContainSubstring, "missing authentication credentials for REST request [/]")
			}
		})
		Convey("any other status is a failure with the reason", func() {
			host := opskat.NewTestHost(esReply(503, `{"error":{"reason":"cluster is starting"},"status":503}`, nil))
			defer host.Close()
			err := testConnection(cfg)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldNotContainSubstring, "authentication failed")
			So(err.Error(), ShouldContainSubstring, "503")
			So(err.Error(), ShouldContainSubstring, "cluster is starting")
		})
		Convey("a plain-text or string-error body is still surfaced", func() {
			host := opskat.NewTestHost(esReply(502, `bad gateway`, nil))
			defer host.Close()
			err := testConnection(cfg)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "bad gateway")

			host2 := opskat.NewTestHost(esReply(500, `{"error":"boom"}`, nil))
			defer host2.Close()
			err = testConnection(cfg)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "boom")
		})
	})
}

// injected evaluates what the host would put on a request for the asset config:
// it picks the auth group the selector names and renders each binding's template
// from the config (the same {{field}} / {{base64(...)}} rules the host applies),
// returning the resulting header name → value ("Authorization" for basic).
func injected(t *testing.T, raw string) map[string]string {
	t.Helper()
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		t.Fatal(err)
	}
	get := func(name string) string { s, _ := values[name].(string); return s }
	part := func(p string) string {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, `"`) {
			return strings.Trim(p, `"`)
		}
		return get(p)
	}
	base64Re := regexp.MustCompile(`\{\{base64\((.*?)\)\}\}`)
	fieldRe := regexp.MustCompile(`\{\{(\w+)\}\}`)
	render := func(tmpl string) string {
		tmpl = base64Re.ReplaceAllStringFunc(tmpl, func(m string) string {
			var sb strings.Builder
			for _, p := range strings.Split(base64Re.FindStringSubmatch(m)[1], ",") {
				sb.WriteString(part(p))
			}
			return base64.StdEncoding.EncodeToString([]byte(sb.String()))
		})
		return fieldRe.ReplaceAllStringFunc(tmpl, func(m string) string { return get(fieldRe.FindStringSubmatch(m)[1]) })
	}

	out := map[string]string{}
	for _, g := range esAuth.Groups {
		if g.When != get(esAuth.Selector) {
			continue
		}
		for _, b := range g.Bindings {
			switch b.In {
			case "basic":
				out["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(render(b.Value)))
			case "header":
				out[b.Name] = render(b.Value)
			default:
				t.Fatalf("unexpected binding %q", b.In)
			}
		}
	}
	return out
}

func TestAuthInjection(t *testing.T) {
	Convey("credential injection per auth type", t, func() {
		Convey("basic", func() {
			So(injected(t, `{"authType":"basic","username":"elastic","password":"s3cret"}`), ShouldResemble,
				map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("elastic:s3cret"))})
		})
		Convey("api key sends the encoded value as-is", func() {
			So(injected(t, `{"authType":"apiKey","apiKey":"VnVhQ2ZHY0I="}`), ShouldResemble,
				map[string]string{"Authorization": "ApiKey VnVhQ2ZHY0I="})
		})
		Convey("bearer token", func() {
			So(injected(t, `{"authType":"token","token":"tok"}`), ShouldResemble,
				map[string]string{"Authorization": "Bearer tok"})
		})
		Convey("none, and a saved asset with no auth type, inject nothing", func() {
			So(injected(t, `{"authType":"none","username":"u","password":"p","token":"t"}`), ShouldBeEmpty)
			So(injected(t, `{"username":"u","password":"p"}`), ShouldBeEmpty)
		})
	})
}

func TestAuthTypeFieldOptions(t *testing.T) {
	Convey("the auth type dropdown shows translated labels and starts on none", t, func() {
		f, _ := reflect.TypeFor[esConfig]().FieldByName("AuthType")
		options := strings.Split(f.Tag.Get("enum"), ",")
		keys := strings.Split(f.Tag.Get("enumLabels"), ",")
		So(keys, ShouldHaveLength, len(options))
		So(f.Tag.Get("default"), ShouldEqual, "none")
		for _, file := range []string{"locales/en.json", "locales/zh-CN.json"} {
			raw, err := os.ReadFile(file)
			So(err, ShouldBeNil)
			var msgs map[string]string
			So(json.Unmarshal(raw, &msgs), ShouldBeNil)
			for _, k := range keys {
				So(msgs[k], ShouldNotBeEmpty)
			}
		}
	})
}
