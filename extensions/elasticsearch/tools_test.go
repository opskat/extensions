package main

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	opskat "github.com/opskat/opskat/pkg/extsdk"

	. "github.com/smartystreets/goconvey/convey"
)

// The tools run through TestHost against a mock cluster. The asset's endpoint
// carries a path prefix, so every request must land under it.
var esAsset = opskat.Asset{ID: 11, Name: "logs cluster", Type: "elasticsearch"}

const esEndpoint = "https://es.example:9200/gw/"

// seenRequest is what the mock cluster received.
type seenRequest struct {
	Method, Path, RawQuery, ContentType, Body string
}

// route answers one request: status, body and content type.
type route struct {
	status      int
	body        string
	contentType string
}

// cluster installs a TestHost whose HTTP mock answers by URL path (query ignored)
// and records every request it saw. A path with no route is a 404 the test did
// not expect, surfaced as ES would.
func cluster(t *testing.T, routes map[string]route) (*opskat.TestHost, *[]seenRequest) {
	t.Helper()
	var seen []seenRequest
	host := opskat.NewTestHost(
		opskat.WithAssetConfig(esAsset.ID, map[string]any{"endpoint": esEndpoint, "authType": "none"}),
		opskat.WithMockHTTP(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			seen = append(seen, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), string(body)})
			rt, ok := routes[r.URL.Path]
			if !ok {
				rt = route{status: 404, body: `{"error":{"type":"unexpected_route","reason":"no route ` + r.URL.Path + `"},"status":404}`}
			}
			ct := rt.contentType
			if ct == "" {
				ct = "application/json"
			}
			w.Header().Set("Content-Type", ct)
			w.WriteHeader(rt.status)
			_, _ = w.Write([]byte(rt.body))
		}),
	)
	return host, &seen
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRequestTool(t *testing.T) {
	Convey("request", t, func() {
		Convey("sends the method, path and query under the endpoint's prefix and returns {status, body}", func() {
			host, seen := cluster(t, map[string]route{
				"/gw/_cluster/health": {status: 200, body: `{"cluster_name":"c1","status":"green","number_of_nodes":3}`},
			})
			defer host.Close()
			out, err := host.CallTool(esAsset, "request", map[string]any{"method": "get", "path": "/_cluster/health?level=indices"})
			So(err, ShouldBeNil)
			res := asMap(t, out)
			So(res["status"], ShouldEqual, 200)
			So(res["body"], ShouldResemble, map[string]any{"cluster_name": "c1", "status": "green", "number_of_nodes": float64(3)})
			So(*seen, ShouldHaveLength, 1)
			So((*seen)[0].Method, ShouldEqual, "GET")
			So((*seen)[0].RawQuery, ShouldEqual, "level=indices")
		})
		Convey("a JSON body is sent as JSON", func() {
			host, seen := cluster(t, map[string]route{"/gw/logs/_doc/1": {status: 201, body: `{"result":"created"}`}})
			defer host.Close()
			out, err := host.CallTool(esAsset, "request", map[string]any{"method": "PUT", "path": "/logs/_doc/1", "body": `{"msg":"hi"}`})
			So(err, ShouldBeNil)
			So(asMap(t, out)["status"], ShouldEqual, 201)
			So((*seen)[0].ContentType, ShouldEqual, "application/json")
			So((*seen)[0].Body, ShouldEqual, `{"msg":"hi"}`)
		})
		Convey("an NDJSON body is sent as NDJSON and gets its final newline", func() {
			host, seen := cluster(t, map[string]route{"/gw/_bulk": {status: 200, body: `{"errors":false}`}})
			defer host.Close()
			_, err := host.CallTool(esAsset, "request", map[string]any{"method": "POST", "path": "/_bulk",
				"body": "{\"index\":{\"_index\":\"a\"}}\n{\"f\":1}"})
			So(err, ShouldBeNil)
			So((*seen)[0].ContentType, ShouldEqual, "application/x-ndjson")
			So((*seen)[0].Body, ShouldEqual, "{\"index\":{\"_index\":\"a\"}}\n{\"f\":1}\n")
		})
		Convey("4xx and 5xx are results carrying ES's error body, not failures", func() {
			host, _ := cluster(t, map[string]route{
				"/gw/missing/_search": {status: 404, body: `{"error":{"type":"index_not_found_exception","reason":"no such index [missing]"},"status":404}`},
				"/gw/_nodes":          {status: 503, body: `upstream down`, contentType: "text/plain"},
			})
			defer host.Close()
			out, err := host.CallTool(esAsset, "request", map[string]any{"method": "POST", "path": "/missing/_search", "body": `{}`})
			So(err, ShouldBeNil)
			res := asMap(t, out)
			So(res["status"], ShouldEqual, 404)
			So(res["body"].(map[string]any)["error"].(map[string]any)["type"], ShouldEqual, "index_not_found_exception")

			out, err = host.CallTool(esAsset, "request", map[string]any{"method": "GET", "path": "/_nodes"})
			So(err, ShouldBeNil)
			So(asMap(t, out), ShouldResemble, map[string]any{"status": float64(503), "body": "upstream down"})
		})
		Convey("a non-JSON answer is returned as text, an empty one as an empty string", func() {
			host, _ := cluster(t, map[string]route{
				"/gw/_cat/health": {status: 200, body: "1700000000 12:00:00 c1 green 3\n", contentType: "text/plain"},
				"/gw/logs":        {status: 200, body: ""},
			})
			defer host.Close()
			out, err := host.CallTool(esAsset, "request", map[string]any{"method": "GET", "path": "/_cat/health"})
			So(err, ShouldBeNil)
			So(asMap(t, out)["body"], ShouldEqual, "1700000000 12:00:00 c1 green 3\n")
			out, err = host.CallTool(esAsset, "request", map[string]any{"method": "HEAD", "path": "/logs"})
			So(err, ShouldBeNil)
			So(asMap(t, out), ShouldResemble, map[string]any{"status": float64(200), "body": ""})
		})
		Convey("a path carrying a scheme or host, or not starting with /, is refused without a request", func() {
			host, seen := cluster(t, map[string]route{})
			defer host.Close()
			for _, p := range []string{"http://evil:9200/_search", "https://evil/x", "//evil/_search", "_search", "", " /_search", "/logs/../_search", "/logs/%2e%2e/_search", "/./_search"} {
				_, err := host.CallTool(esAsset, "request", map[string]any{"method": "GET", "path": p})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "path")
			}
			So(*seen, ShouldBeEmpty)
		})
		Convey("a method ES does not speak is refused without a request", func() {
			host, seen := cluster(t, map[string]route{})
			defer host.Close()
			for _, m := range []string{"TRACE", "PATCH", ""} {
				_, err := host.CallTool(esAsset, "request", map[string]any{"method": m, "path": "/"})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "method")
			}
			So(*seen, ShouldBeEmpty)
		})
		Convey("a transport failure is a tool failure", func() {
			host := opskat.NewTestHost(opskat.WithAssetConfig(esAsset.ID, map[string]any{"endpoint": esEndpoint}))
			defer host.Close()
			_, err := host.CallTool(esAsset, "request", map[string]any{"method": "GET", "path": "/"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "cannot reach Elasticsearch")
		})
	})
}

func TestHealthTool(t *testing.T) {
	Convey("health", t, func() {
		Convey("summarizes the cluster from / and _cluster/health", func() {
			host, _ := cluster(t, map[string]route{
				"/gw/": {status: 200, body: `{"cluster_name":"c1","version":{"number":"8.19.0"}}`},
				"/gw/_cluster/health": {status: 200, body: `{"cluster_name":"c1","status":"yellow","number_of_nodes":3,"number_of_data_nodes":2,` +
					`"active_primary_shards":5,"active_shards":9,"unassigned_shards":1}`},
			})
			defer host.Close()
			out, err := host.CallTool(esAsset, "health", map[string]any{})
			So(err, ShouldBeNil)
			So(asMap(t, out), ShouldResemble, map[string]any{
				"clusterName": "c1", "version": "8.19.0", "status": "yellow",
				"numberOfNodes": float64(3), "numberOfDataNodes": float64(2),
				"activeShards": float64(9), "activePrimaryShards": float64(5), "unassignedShards": float64(1),
			})
		})
		Convey("an ES error fails with the status, type and reason", func() {
			host, _ := cluster(t, map[string]route{
				"/gw/":                {status: 200, body: `{"cluster_name":"c1","version":{"number":"7.17.0"}}`},
				"/gw/_cluster/health": {status: 403, body: `{"error":{"type":"security_exception","reason":"action [cluster:monitor/health] is unauthorized"},"status":403}`},
			})
			defer host.Close()
			_, err := host.CallTool(esAsset, "health", map[string]any{})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "403")
			So(err.Error(), ShouldContainSubstring, "security_exception")
			So(err.Error(), ShouldContainSubstring, "action [cluster:monitor/health] is unauthorized")
		})
	})
}

func TestIndicesTool(t *testing.T) {
	Convey("indices", t, func() {
		rows := `[{"health":"green","status":"open","index":"logs-2","docs.count":"20","store.size":"2048"},` +
			`{"health":"yellow","status":"open","index":"logs-1","docs.count":"10","store.size":"1024"},` +
			`{"health":null,"status":"close","index":"logs-0","docs.count":null,"store.size":null}]`

		Convey("lists every index by default, sorted, with numeric counts and sizes", func() {
			host, seen := cluster(t, map[string]route{"/gw/_cat/indices": {status: 200, body: rows}})
			defer host.Close()
			out, err := host.CallTool(esAsset, "indices", map[string]any{})
			So(err, ShouldBeNil)
			So(asMap(t, out)["indices"], ShouldResemble, []any{
				map[string]any{"index": "logs-0", "health": "", "status": "close", "docsCount": nil, "storeSize": nil},
				map[string]any{"index": "logs-1", "health": "yellow", "status": "open", "docsCount": float64(10), "storeSize": float64(1024)},
				map[string]any{"index": "logs-2", "health": "green", "status": "open", "docsCount": float64(20), "storeSize": float64(2048)},
			})
			q := (*seen)[0].RawQuery
			So(q, ShouldContainSubstring, "format=json")
			So(q, ShouldContainSubstring, "bytes=b")
			So(q, ShouldContainSubstring, "expand_wildcards=open%2Cclosed")
		})
		Convey("narrows to a pattern and includes hidden indices on request", func() {
			host, seen := cluster(t, map[string]route{"/gw/_cat/indices/logs-*,.ds-*": {status: 200, body: `[]`}})
			defer host.Close()
			out, err := host.CallTool(esAsset, "indices", map[string]any{"pattern": "logs-*,.ds-*", "include-hidden": true})
			So(err, ShouldBeNil)
			So(asMap(t, out)["indices"], ShouldResemble, []any{})
			So((*seen)[0].RawQuery, ShouldContainSubstring, "expand_wildcards=all")
		})
		Convey("an ES error fails with the status, type and reason", func() {
			host, _ := cluster(t, map[string]route{"/gw/_cat/indices/nope": {status: 404,
				body: `{"error":{"type":"index_not_found_exception","reason":"no such index [nope]"},"status":404}`}})
			defer host.Close()
			_, err := host.CallTool(esAsset, "indices", map[string]any{"pattern": "nope"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "404")
			So(err.Error(), ShouldContainSubstring, "index_not_found_exception")
			So(err.Error(), ShouldContainSubstring, "no such index [nope]")
		})
	})
}

func TestMappingTool(t *testing.T) {
	Convey("mapping", t, func() {
		Convey("returns the index's mapping as ES reports it", func() {
			host, seen := cluster(t, map[string]route{"/gw/logs/_mapping": {status: 200,
				body: `{"logs-000001":{"mappings":{"properties":{"msg":{"type":"text"}}}}}`}})
			defer host.Close()
			out, err := host.CallTool(esAsset, "mapping", map[string]any{"index": "logs"})
			So(err, ShouldBeNil)
			So(asMap(t, out), ShouldResemble, map[string]any{"logs-000001": map[string]any{"mappings": map[string]any{
				"properties": map[string]any{"msg": map[string]any{"type": "text"}}}}})
			So((*seen)[0].Method, ShouldEqual, "GET")
		})
		Convey("an ES error fails with the status, type and reason", func() {
			host, _ := cluster(t, map[string]route{"/gw/nope/_mapping": {status: 404,
				body: `{"error":{"root_cause":[],"type":"index_not_found_exception","reason":"no such index [nope]"},"status":404}`}})
			defer host.Close()
			_, err := host.CallTool(esAsset, "mapping", map[string]any{"index": "nope"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "404")
			So(err.Error(), ShouldContainSubstring, "index_not_found_exception")
			So(err.Error(), ShouldContainSubstring, "no such index [nope]")
		})
	})
}

func TestSearchTool(t *testing.T) {
	Convey("search", t, func() {
		hits := `{"took":7,"timed_out":false,"hits":{"total":{"value":1234,"relation":"gte"},"hits":[` +
			`{"_index":"logs-1","_id":"a","_score":1.0,"_source":{"msg":"hi"}},` +
			`{"_index":"logs-2","_id":"b","_score":0.5,"_source":{"msg":"yo"}}]}}`

		Convey("sends the DSL query and returns total, took and each hit's index, id and source", func() {
			host, seen := cluster(t, map[string]route{"/gw/logs-*/_search": {status: 200, body: hits}})
			defer host.Close()
			out, err := host.CallTool(esAsset, "search", map[string]any{
				"index": "logs-*", "query": `{"match":{"msg":"hi"}}`, "sort": "@timestamp:desc", "size": 5,
			})
			So(err, ShouldBeNil)
			So(asMap(t, out), ShouldResemble, map[string]any{
				"total": float64(1234), "totalRelation": "gte", "took": float64(7),
				"hits": []any{
					map[string]any{"_index": "logs-1", "_id": "a", "_source": map[string]any{"msg": "hi"}},
					map[string]any{"_index": "logs-2", "_id": "b", "_source": map[string]any{"msg": "yo"}},
				},
			})
			req := (*seen)[0]
			So(req.Method, ShouldEqual, "POST")
			So(req.ContentType, ShouldEqual, "application/json")
			var body map[string]any
			So(json.Unmarshal([]byte(req.Body), &body), ShouldBeNil)
			So(body, ShouldResemble, map[string]any{"query": map[string]any{"match": map[string]any{"msg": "hi"}}})
			So(req.RawQuery, ShouldContainSubstring, "size=5")
			So(req.RawQuery, ShouldContainSubstring, "sort=%40timestamp%3Adesc")
		})
		Convey("a query string goes in q, and size defaults to 10", func() {
			host, seen := cluster(t, map[string]route{"/gw/logs/_search": {status: 200, body: hits}})
			defer host.Close()
			_, err := host.CallTool(esAsset, "search", map[string]any{"index": "logs", "q": "msg:hi AND level:error"})
			So(err, ShouldBeNil)
			req := (*seen)[0]
			So(req.Body, ShouldEqual, "")
			So(req.RawQuery, ShouldContainSubstring, "q=msg%3Ahi+AND+level%3Aerror")
			So(req.RawQuery, ShouldContainSubstring, "size=10")
		})
		Convey("bad arguments are refused without a request", func() {
			host, seen := cluster(t, map[string]route{})
			defer host.Close()
			for _, args := range []map[string]any{
				{"index": "logs", "size": 101},
				{"index": "logs", "size": -1},
				{"index": "logs", "q": "x", "query": `{"match_all":{}}`},
				{"index": "logs", "query": `{"match_all":`},
				{"index": "logs", "query": `[1]`},
			} {
				_, err := host.CallTool(esAsset, "search", args)
				So(err, ShouldNotBeNil)
			}
			So(*seen, ShouldBeEmpty)
		})
		Convey("an ES error fails with the status, type and reason", func() {
			host, _ := cluster(t, map[string]route{"/gw/logs/_search": {status: 400,
				body: `{"error":{"type":"parsing_exception","reason":"unknown query [mtch]"},"status":400}`}})
			defer host.Close()
			_, err := host.CallTool(esAsset, "search", map[string]any{"index": "logs", "query": `{"mtch":{}}`})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "400")
			So(err.Error(), ShouldContainSubstring, "parsing_exception")
			So(err.Error(), ShouldContainSubstring, "unknown query [mtch]")
		})
	})
}
