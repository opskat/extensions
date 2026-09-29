package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	opskat "github.com/opskat/opskat/pkg/extsdk"
)

// Tool arguments are the schema: the JSON name is the flag.

type requestArgs struct {
	Method string `json:"method" desc:"HTTP method: GET, HEAD, POST, PUT or DELETE"`
	Path   string `json:"path" desc:"Path and query string starting with /, e.g. /logs-*/_search?size=5. Never a scheme or host: the request always goes to the asset's address"`
	Body   string `json:"body,omitempty" desc:"Request body: JSON, or NDJSON for _bulk and _msearch"`
}

type healthArgs struct{}

type indicesArgs struct {
	Pattern       string `json:"pattern,omitempty" desc:"Index name or pattern, comma-separated for several (default: every index)"`
	IncludeHidden bool   `json:"include-hidden,omitempty" desc:"Also list hidden and system indices"`
}

type mappingArgs struct {
	Index string `json:"index" desc:"Index, alias or pattern"`
}

type searchArgs struct {
	Index string `json:"index" desc:"Index, alias or pattern to search, comma-separated for several"`
	Query string `json:"query,omitempty" desc:"Query DSL as a JSON object: the value of \"query\", e.g. {\"match\":{\"message\":\"timeout\"}}"`
	Q     string `json:"q,omitempty" desc:"Lucene query string instead of --query, e.g. level:error AND service:api"`
	Sort  string `json:"sort,omitempty" desc:"field:asc or field:desc, comma-separated for several"`
	Size  int    `json:"size,omitempty" desc:"Number of hits to return, 1 to 100 (default 10)"`
}

const (
	defaultSearchSize = 10
	maxSearchSize     = 100
)

// esURL puts a path (with its query) under the endpoint, keeping the endpoint's
// own path prefix.
func esURL(endpoint, pathAndQuery string) string {
	return strings.TrimRight(endpoint, "/") + pathAndQuery
}

// esClient sends requests to the call's asset. The host scopes them to its
// endpoint, dials its tunnel / proxy / TLS and injects its credentials.
type esClient struct {
	endpoint string
	http     *http.Client
}

func clientFor(ctx *opskat.ToolContext) (*esClient, error) {
	raw, err := ctx.AssetConfig()
	if err != nil {
		return nil, err
	}
	var cfg esConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode asset config: %w", err)
	}
	return &esClient{endpoint: cfg.Endpoint, http: &http.Client{Transport: opskat.NewHTTPTransport()}}, nil
}

// do sends one request. Only a request that never got an answer is an error;
// whatever status ES answers with is the caller's to judge.
func (c *esClient) do(method, pathAndQuery, body, contentType string) (int, []byte, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, esURL(c.endpoint, pathAndQuery), reader)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("cannot reach Elasticsearch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read Elasticsearch response: %w", err)
	}
	return resp.StatusCode, data, nil
}

// call is do for the convenience tools: an ES error fails the call with the
// status and ES's type and reason, and a success is decoded into out.
func (c *esClient) call(method, pathAndQuery, body string, out any) error {
	contentType := ""
	if body != "" {
		contentType = "application/json"
	}
	status, data, err := c.do(method, pathAndQuery, body, contentType)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		typ, reason := esErrorDetail(data)
		if typ != "" {
			return fmt.Errorf("Elasticsearch returned HTTP %d: %s: %s", status, typ, reason)
		}
		return fmt.Errorf("Elasticsearch returned HTTP %d: %s", status, reason)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode Elasticsearch response: %w", err)
	}
	return nil
}

var requestMethods = setOf("GET", "HEAD", "POST", "PUT", "DELETE")

// requestResult is ES's answer as-is: the body parsed when it is JSON, text
// otherwise (an empty body is "").
type requestResult struct {
	Status int `json:"status"`
	Body   any `json:"body"`
}

func checkRequestMethod(m string) error {
	if !requestMethods[normalizeMethod(m)] {
		return fmt.Errorf("method %q is not one of GET, HEAD, POST, PUT, DELETE", m)
	}
	return nil
}

func handleRequest(ctx *opskat.ToolContext, args requestArgs) (any, error) {
	if err := checkRequestMethod(args.Method); err != nil {
		return nil, err
	}
	method := normalizeMethod(args.Method)
	segs, err := parseRequestPath(args.Path)
	if err != nil {
		return nil, err
	}
	c, err := clientFor(ctx)
	if err != nil {
		return nil, err
	}
	body, contentType := args.Body, ""
	if body != "" {
		contentType = "application/json"
		if isNDJSONAPI(segs) {
			contentType = "application/x-ndjson"
			if !strings.HasSuffix(body, "\n") {
				body += "\n" // ES rejects an NDJSON body without its final newline
			}
		}
	}
	status, data, err := c.do(method, args.Path, body, contentType)
	if err != nil {
		return nil, err
	}
	result := requestResult{Status: status, Body: string(data)}
	if len(bytes.TrimSpace(data)) > 0 && json.Valid(data) {
		result.Body = json.RawMessage(data)
	}
	return result, nil
}

// isNDJSONAPI reports a _bulk or _msearch request, rooted or under an index.
func isNDJSONAPI(segs []string) bool {
	for i := 0; i < len(segs) && i < 2; i++ {
		if segs[i] == "_bulk" || segs[i] == "_msearch" {
			return true
		}
	}
	return false
}

type healthResult struct {
	ClusterName         string `json:"clusterName"`
	Version             string `json:"version"`
	Status              string `json:"status"`
	NumberOfNodes       int    `json:"numberOfNodes"`
	NumberOfDataNodes   int    `json:"numberOfDataNodes"`
	ActiveShards        int    `json:"activeShards"`
	ActivePrimaryShards int    `json:"activePrimaryShards"`
	UnassignedShards    int    `json:"unassignedShards"`
}

func handleHealth(ctx *opskat.ToolContext, _ healthArgs) (any, error) {
	c, err := clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var root struct {
		ClusterName string `json:"cluster_name"`
		Version     struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if err := c.call(http.MethodGet, "/", "", &root); err != nil {
		return nil, err
	}
	var h struct {
		Status              string `json:"status"`
		NumberOfNodes       int    `json:"number_of_nodes"`
		NumberOfDataNodes   int    `json:"number_of_data_nodes"`
		ActiveShards        int    `json:"active_shards"`
		ActivePrimaryShards int    `json:"active_primary_shards"`
		UnassignedShards    int    `json:"unassigned_shards"`
	}
	if err := c.call(http.MethodGet, "/_cluster/health", "", &h); err != nil {
		return nil, err
	}
	return healthResult{
		ClusterName:         root.ClusterName,
		Version:             root.Version.Number,
		Status:              h.Status,
		NumberOfNodes:       h.NumberOfNodes,
		NumberOfDataNodes:   h.NumberOfDataNodes,
		ActiveShards:        h.ActiveShards,
		ActivePrimaryShards: h.ActivePrimaryShards,
		UnassignedShards:    h.UnassignedShards,
	}, nil
}

// indexInfo is one row of the index list. Counts are null for a closed index.
type indexInfo struct {
	Index     string `json:"index"`
	Health    string `json:"health"`
	Status    string `json:"status"`
	DocsCount *int64 `json:"docsCount"`
	StoreSize *int64 `json:"storeSize"`
}

func handleIndices(ctx *opskat.ToolContext, args indicesArgs) (any, error) {
	c, err := clientFor(ctx)
	if err != nil {
		return nil, err
	}
	p := "/_cat/indices"
	if args.Pattern != "" {
		p += "/" + url.PathEscape(args.Pattern)
	}
	q := url.Values{
		"format":           {"json"},
		"bytes":            {"b"},
		"h":                {"index,health,status,docs.count,store.size"},
		"expand_wildcards": {"open,closed"},
	}
	if args.IncludeHidden {
		q.Set("expand_wildcards", "all")
	}
	var rows []struct {
		Index     string  `json:"index"`
		Health    string  `json:"health"`
		Status    string  `json:"status"`
		DocsCount *string `json:"docs.count"`
		StoreSize *string `json:"store.size"`
	}
	if err := c.call(http.MethodGet, p+"?"+q.Encode(), "", &rows); err != nil {
		return nil, err
	}
	// 7.x does not flag its system indices hidden, so they are hidden by name —
	// unless the pattern itself names indices starting with ".", which ES reads as
	// asking for them too.
	showDot := args.IncludeHidden || namesDotIndices(args.Pattern)
	out := make([]indexInfo, 0, len(rows))
	for _, r := range rows {
		if !showDot && strings.HasPrefix(r.Index, ".") {
			continue
		}
		docs, err := optionalCount(r.DocsCount)
		if err != nil {
			return nil, fmt.Errorf("index %s: docs.count: %w", r.Index, err)
		}
		size, err := optionalCount(r.StoreSize)
		if err != nil {
			return nil, fmt.Errorf("index %s: store.size: %w", r.Index, err)
		}
		out = append(out, indexInfo{Index: r.Index, Health: r.Health, Status: r.Status, DocsCount: docs, StoreSize: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return map[string]any{"indices": out}, nil
}

// namesDotIndices reports a pattern with an expression starting with ".".
func namesDotIndices(pattern string) bool {
	for _, expr := range strings.Split(pattern, ",") {
		if strings.HasPrefix(strings.TrimSpace(expr), ".") {
			return true
		}
	}
	return false
}

// optionalCount parses a _cat number, which ES sends as a string (null when the
// index is closed).
func optionalCount(s *string) (*int64, error) {
	if s == nil {
		return nil, nil
	}
	n, err := strconv.ParseInt(*s, 10, 64)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func handleMapping(ctx *opskat.ToolContext, args mappingArgs) (any, error) {
	if args.Index == "" {
		return nil, errors.New("index is required")
	}
	c, err := clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var mapping json.RawMessage
	if err := c.call(http.MethodGet, "/"+url.PathEscape(args.Index)+"/_mapping", "", &mapping); err != nil {
		return nil, err
	}
	return mapping, nil
}

type searchHit struct {
	Index  string          `json:"_index"`
	ID     string          `json:"_id"`
	Source json.RawMessage `json:"_source"`
}

type searchResult struct {
	Total         int64       `json:"total"`
	TotalRelation string      `json:"totalRelation"`
	Took          int64       `json:"took"`
	Hits          []searchHit `json:"hits"`
}

func handleSearch(ctx *opskat.ToolContext, args searchArgs) (any, error) {
	if args.Index == "" {
		return nil, errors.New("index is required")
	}
	size := args.Size
	if size == 0 {
		size = defaultSearchSize
	}
	if size < 1 || size > maxSearchSize {
		return nil, fmt.Errorf("size must be between 1 and %d, got %d", maxSearchSize, args.Size)
	}
	if args.Query != "" && args.Q != "" {
		return nil, errors.New("give either --query (DSL) or --q (query string), not both")
	}
	body := ""
	if args.Query != "" {
		var clause map[string]json.RawMessage
		if err := json.Unmarshal([]byte(args.Query), &clause); err != nil {
			return nil, fmt.Errorf("--query must be a JSON object: %w", err)
		}
		data, err := json.Marshal(map[string]json.RawMessage{"query": json.RawMessage(args.Query)})
		if err != nil {
			return nil, err
		}
		body = string(data)
	}
	q := url.Values{"size": {strconv.Itoa(size)}}
	if args.Q != "" {
		q.Set("q", args.Q)
	}
	if args.Sort != "" {
		q.Set("sort", args.Sort)
	}

	c, err := clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Took int64 `json:"took"`
		Hits struct {
			Total struct {
				Value    int64  `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []searchHit `json:"hits"`
		} `json:"hits"`
	}
	if err := c.call(http.MethodPost, "/"+url.PathEscape(args.Index)+"/_search?"+q.Encode(), body, &resp); err != nil {
		return nil, err
	}
	hits := resp.Hits.Hits
	if hits == nil {
		hits = []searchHit{}
	}
	return searchResult{
		Total:         resp.Hits.Total.Value,
		TotalRelation: resp.Hits.Total.Relation,
		Took:          resp.Took,
		Hits:          hits,
	}, nil
}

// The convenience tools only read; each reports what it names.

func classifyIndices(args indicesArgs) (string, []string) {
	if args.Pattern == "" {
		return actionRead, []string{allIndices}
	}
	return actionRead, resourceSet(indexResources(args.Pattern))
}

func classifyMapping(args mappingArgs) (string, []string) {
	return actionRead, resourceSet(indexResources(args.Index))
}

func classifySearch(args searchArgs) (string, []string) {
	return actionRead, resourceSet(indexResources(args.Index))
}
