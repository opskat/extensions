package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	opskat "github.com/opskat/opskat/pkg/extsdk"

	. "github.com/smartystreets/goconvey/convey"
)

// classifyCase is one row of the request classification table: the call as the
// host would hand it to check_policy, and the action plus resource set the
// extension must answer. Resources are compared as a set.
type classifyCase struct {
	method, path, body string
	action             string
	resources          []string
}

// ndjson joins lines into an NDJSON body with the trailing newline ES expects.
func ndjson(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

var requestClassification = []classifyCase{
	// read: GET / HEAD, whatever the API
	{"GET", "/", "", "read", nil},
	{"HEAD", "/logs-1", "", "read", []string{"logs-1"}},
	{"get", "/logs-1/_doc/1", "", "read", []string{"logs-1"}},
	{"GET", "/logs-1/_source/1", "", "read", []string{"logs-1"}},
	{"GET", "/logs-1,logs-2/_search?q=x", "", "read", []string{"logs-1", "logs-2"}},
	{"GET", "/x/_mapping", "", "read", []string{"x"}},
	{"GET", "/_mapping", "", "read", []string{"*"}},
	{"GET", "/_settings", "", "read", []string{"*"}},
	{"GET", "/_alias/current", "", "read", []string{"*"}},
	{"GET", "/_data_stream/logs-app", "", "read", []string{"logs-app"}},
	{"GET", "/_resolve/index/logs-*", "", "read", []string{"logs-*"}},

	// read: POST search-family APIs
	{"POST", "/logs-*/_search", `{"query":{"match_all":{}}}`, "read", []string{"logs-*"}},
	{"POST", "/_search", `{}`, "read", []string{"*"}},
	{"POST", "/_all/_search", `{}`, "read", []string{"*"}},
	{"POST", "/_all,logs-1/_search", `{}`, "read", []string{"*", "logs-1"}},
	{"POST", "/logs-1/_count", `{}`, "read", []string{"logs-1"}},
	{"POST", "/_count", `{}`, "read", []string{"*"}},
	{"POST", "/x/_field_caps?fields=*", "", "read", []string{"x"}},
	{"POST", "/x/_validate/query", `{}`, "read", []string{"x"}},
	{"POST", "/x/_explain/1", `{}`, "read", []string{"x"}},
	{"POST", "/x/_termvectors/1", `{}`, "read", []string{"x"}},
	{"POST", "/_mtermvectors", `{}`, "read", []string{"*"}},
	{"POST", "/_sql?format=json", `{"query":"SELECT 1"}`, "read", []string{"*"}},
	{"POST", "/_sql/close", `{"cursor":"c"}`, "read", []string{"*"}},
	{"POST", "/x/_eql/search", `{}`, "read", []string{"x"}},
	// Only clearing a scroll and closing a point in time are reads among DELETEs;
	// deleting a stored async / EQL / SQL search result is admin like any other.
	{"DELETE", "/_eql/search/abc", "", "admin", []string{"*"}},
	{"POST", "/x/_async_search", `{}`, "read", []string{"x"}},
	{"DELETE", "/_async_search/abc", "", "admin", []string{"*"}},
	{"DELETE", "/_sql/async/delete/abc", "", "admin", []string{"*"}},
	{"POST", "/x/_search/template", `{"id":"t"}`, "read", []string{"x"}},
	{"POST", "/_render/template", `{"id":"t"}`, "read", []string{"*"}},
	{"POST", "/x/_pit?keep_alive=1m", "", "read", []string{"x"}},
	{"DELETE", "/_pit", `{"id":"p"}`, "read", []string{"*"}},
	{"POST", "/_search/scroll", `{"scroll_id":"s"}`, "read", []string{"*"}},
	{"DELETE", "/_search/scroll", `{"scroll_id":"s"}`, "read", []string{"*"}},

	// read: _mget / _msearch collect the indices named in the body
	{"POST", "/_mget", `{"docs":[{"_index":"a","_id":"1"},{"_index":"b","_id":"2"}]}`, "read", []string{"a", "b"}},
	{"POST", "/x/_mget", `{"ids":["1","2"]}`, "read", []string{"x"}},
	{"POST", "/x/_mget", `{"docs":[{"_id":"1"},{"_index":"y","_id":"2"}]}`, "read", []string{"x", "y"}},
	{"POST", "/_mget", `{"docs":[{"_id":"1"}]}`, "read", []string{"*"}},
	{"POST", "/_mget", `not json`, "read", []string{"*"}},
	{"POST", "/a/_msearch", ndjson(
		`{"index":"b"}`, `{"query":{}}`,
		`{}`, `{"query":{}}`,
		`{"index":["c","d"]}`, `{}`,
		`{"index":"e,f"}`, `{}`,
	), "read", []string{"a", "b", "c", "d", "e", "f"}},
	{"POST", "/_msearch", ndjson(`{"index":"b"}`, `{}`, `{}`, `{}`), "read", []string{"*", "b"}},
	{"POST", "/_msearch/template", ndjson(`{"index":"t-1"}`, `{"id":"tpl"}`), "read", []string{"t-1"}},

	// cluster-level reads: the leading segment
	{"GET", "/_cluster/health", "", "read", []string{"_cluster"}},
	{"GET", "/_cluster/health/logs-1", "", "read", []string{"_cluster"}},
	{"GET", "/_nodes/stats", "", "read", []string{"_nodes"}},
	{"GET", "/_cat/health?format=json", "", "read", []string{"_cat"}},
	{"GET", "/_security/_authenticate", "", "read", []string{"_security"}},
	// _cat APIs that take an index expression report it
	{"GET", "/_cat/indices/logs-*?format=json", "", "read", []string{"logs-*"}},
	{"GET", "/_cat/indices", "", "read", []string{"*"}},
	{"GET", "/_cat/count/a,b", "", "read", []string{"a", "b"}},

	// write: document writes
	{"PUT", "/x/_doc/1", `{"a":1}`, "write", []string{"x"}},
	{"POST", "/x/_doc", `{"a":1}`, "write", []string{"x"}},
	{"PUT", "/x/_create/1", `{"a":1}`, "write", []string{"x"}},
	{"POST", "/x/_update/1", `{"doc":{"a":1}}`, "write", []string{"x"}},
	{"POST", "/x/_update_by_query", `{}`, "write", []string{"x"}},
	{"POST", "/logs-1,logs-2/_update_by_query", `{}`, "write", []string{"logs-1", "logs-2"}},

	// write: _bulk collects the body's indices; the path index is the default
	{"POST", "/_bulk", ndjson(
		`{"index":{"_index":"a","_id":"1"}}`, `{"f":1}`,
		`{"update":{"_index":"b","_id":"1"}}`, `{"doc":{"f":2}}`,
		`{"create":{"_index":"c"}}`, `{"f":3}`,
	), "write", []string{"a", "b", "c"}},
	{"POST", "/x/_bulk", ndjson(
		`{"index":{}}`, `{"f":1}`,
		`{"index":{"_index":"y"}}`, `{"f":1}`,
	), "write", []string{"x", "y"}},
	{"PUT", "/_bulk", ndjson(`{"index":{"_id":"1"}}`, `{"f":1}`, `{"index":{"_index":"a"}}`, `{"f":1}`), "write", []string{"*", "a"}},
	{"POST", "/_bulk", ndjson(`{"index":{"_index":"a"}}`, `{"f":1}`, `not json`), "write", []string{"*", "a"}},
	{"POST", "/x/_bulk", "", "write", []string{"x"}},
	// a bulk body that deletes documents is a delete, on every index it touches
	{"POST", "/_bulk", ndjson(
		`{"index":{"_index":"logs-1"}}`, `{"f":1}`,
		`{"delete":{"_index":"prod-1","_id":"9"}}`,
		`{"index":{"_index":"logs-2"}}`, `{"f":1}`,
	), "delete", []string{"logs-1", "logs-2", "prod-1"}},

	// delete: documents, indices, delete_by_query
	{"DELETE", "/x/_doc/1", "", "delete", []string{"x"}},
	{"DELETE", "/logs-1,prod-1", "", "delete", []string{"logs-1", "prod-1"}},
	{"DELETE", "/logs-1, prod-1", "", "delete", []string{"logs-1", "prod-1"}},
	{"DELETE", "/_all", "", "delete", []string{"*"}},
	{"DELETE", "/logs-*", "", "delete", []string{"logs-*"}},
	{"DELETE", "/logs-*,-logs-keep", "", "delete", []string{"logs-*"}},
	{"POST", "/x/_delete_by_query", `{"query":{}}`, "delete", []string{"x"}},
	{"DELETE", "/_data_stream/logs-app,logs-web", "", "delete", []string{"logs-app", "logs-web"}},

	// admin: index management
	{"PUT", "/x", `{"settings":{}}`, "admin", []string{"x"}},
	{"PUT", "/x/_mapping", `{}`, "admin", []string{"x"}},
	{"PUT", "/_settings", `{}`, "admin", []string{"*"}},
	{"PUT", "/x/_settings", `{}`, "admin", []string{"x"}},
	{"POST", "/x/_close", "", "admin", []string{"x"}},
	{"POST", "/x,y/_open", "", "admin", []string{"x", "y"}},
	{"POST", "/x/_refresh", "", "admin", []string{"x"}},
	{"GET", "/x/_refresh", "", "admin", []string{"x"}},
	{"GET", "/_flush", "", "admin", []string{"*"}},
	{"POST", "/x/_forcemerge", "", "admin", []string{"x"}},
	{"POST", "/x/_cache/clear", "", "admin", []string{"x"}},
	{"PUT", "/x/_alias/current", "", "admin", []string{"x"}},
	{"DELETE", "/x/_alias/current", "", "admin", []string{"x"}},
	{"POST", "/_aliases", `{"actions":[{"add":{"index":"a","alias":"al"}},{"remove":{"indices":["b","c"],"alias":"al"}}]}`, "admin", []string{"a", "b", "c"}},
	{"POST", "/_aliases", `{"actions":[{"add":{"index":"a","alias":"al"}},{"remove_index":{"index":"prod-1"}}]}`, "delete", []string{"a", "prod-1"}},
	{"POST", "/_aliases", `nope`, "admin", []string{"*"}},
	{"POST", "/_reindex", `{"source":{"index":["a","b"]},"dest":{"index":"c"}}`, "admin", []string{"a", "b", "c"}},
	{"POST", "/_reindex", `{"source":{"index":"a,b"},"dest":{"index":"c"}}`, "admin", []string{"a", "b", "c"}},
	{"POST", "/_reindex", "", "admin", []string{"*"}},
	{"POST", "/logs/_rollover/logs-000002", "", "admin", []string{"logs", "logs-000002"}},
	{"POST", "/src/_clone/dst", "", "admin", []string{"dst", "src"}},
	{"POST", "/src/_shrink/small", "", "admin", []string{"small", "src"}},
	{"PUT", "/_data_stream/logs-app", "", "admin", []string{"logs-app"}},

	// admin: cluster-level APIs report their leading segment
	{"PUT", "/_index_template/t", `{}`, "admin", []string{"_index_template"}},
	{"DELETE", "/_index_template/t", "", "admin", []string{"_index_template"}},
	{"PUT", "/_ilm/policy/p", `{}`, "admin", []string{"_ilm"}},
	{"PUT", "/_ingest/pipeline/p", `{}`, "admin", []string{"_ingest"}},
	{"PUT", "/_snapshot/repo/snap", `{}`, "admin", []string{"_snapshot"}},
	{"DELETE", "/_snapshot/repo/snap", "", "admin", []string{"_snapshot"}},
	{"PUT", "/_cluster/settings", `{}`, "admin", []string{"_cluster"}},
	{"POST", "/_security/user/bob", `{}`, "admin", []string{"_security"}},
	{"POST", "/_tasks/node:1/_cancel", "", "admin", []string{"_tasks"}},

	// unrecognized requests are admin
	{"POST", "/x", `{}`, "admin", []string{"x"}},
	{"PUT", "/x/_search", `{}`, "admin", []string{"x"}},
	{"POST", "/x/_source/1", "", "admin", []string{"x"}},
	{"POST", "/x/_analyze", `{"text":"a"}`, "admin", []string{"x"}},
	{"PATCH", "/x/_doc/1", `{}`, "admin", []string{"x"}},
	{"POST", "/_foo/bar", "", "admin", []string{"_foo"}},
	{"", "/x/_search", "", "admin", []string{"x"}},

	// expressions: wildcards kept, date math widened to a wildcard, remote kept
	{"GET", "/%3Clogs-%7Bnow%2Fd%7D%3E/_search", "", "read", []string{"logs-*"}},
	{"GET", "/%3Clogs-%7Bnow%2Fd%7BYYYY.MM%7D%7D-app%3E/_search", "", "read", []string{"logs-*-app"}},
	{"GET", "/remote:logs-*/_search", "", "read", []string{"remote:logs-*"}},
	{"GET", "/logs-%3F/_search", "", "read", []string{"logs-?"}},
	{"DELETE", "/-only-exclusion", "", "delete", []string{"*"}},

	// a path the handler refuses is still classified conservatively
	{"GET", "http://other:9200/_search", "", "admin", []string{"*"}},
	{"GET", "//other/_search", "", "admin", []string{"*"}},
	{"GET", "_search", "", "admin", []string{"*"}},
}

func sortedSet(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func TestRequestClassification(t *testing.T) {
	Convey("request classification: method × path × body → action + resources", t, func() {
		host := opskat.NewTestHost()
		defer host.Close()
		for _, c := range requestClassification {
			args := map[string]any{"method": c.method, "path": c.path}
			if c.body != "" {
				args["body"] = c.body
			}
			action, resources, err := host.CheckPolicy("request", args)
			name := fmt.Sprintf("%s %s %q", c.method, c.path, c.body)
			So(err, ShouldBeNil)
			So(name+" → "+action, ShouldEqual, name+" → "+c.action)
			So(fmt.Sprint(name, " → ", sortedSet(resources)), ShouldEqual, fmt.Sprint(name, " → ", sortedSet(c.resources)))
		}
	})
}

func TestConvenienceToolClassification(t *testing.T) {
	Convey("convenience tools are reads on what they name", t, func() {
		host := opskat.NewTestHost()
		defer host.Close()
		cases := []struct {
			tool      string
			args      map[string]any
			resources []string
		}{
			{"health", map[string]any{}, []string{"_cluster"}},
			{"indices", map[string]any{}, []string{"*"}},
			{"indices", map[string]any{"pattern": "logs-*,metrics-*"}, []string{"logs-*", "metrics-*"}},
			{"indices", map[string]any{"pattern": "_all", "include-hidden": true}, []string{"*"}},
			{"mapping", map[string]any{"index": "a,b"}, []string{"a", "b"}},
			{"search", map[string]any{"index": "logs-*", "q": "x"}, []string{"logs-*"}},
			{"search", map[string]any{"index": "_all"}, []string{"*"}},
		}
		for _, c := range cases {
			action, resources, err := host.CheckPolicy(c.tool, c.args)
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "read")
			So(sortedSet(resources), ShouldResemble, sortedSet(c.resources))
		}
	})
}
