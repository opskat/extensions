package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// The policy face: every call is one of four actions on a set of resources. A
// resource is an index expression as ES would resolve it — a name, a wildcard
// pattern (`*` / `?` are wildcards to the host), or `*` for every index — or, for
// an API that works on the cluster rather than on indices, its leading path
// segment (`_cluster`, `_snapshot`, …). ES index names cannot start with `_`, so
// the two never collide.
const (
	actionRead   = "read"
	actionWrite  = "write"
	actionDelete = "delete"
	actionAdmin  = "admin"

	allIndices = "*"
)

var requestActions = []string{actionRead, actionWrite, actionDelete, actionAdmin}

// parseRequestPath checks a request path and splits it into decoded segments.
// The path is appended to the asset's endpoint, so it must be exactly that — a
// path starting with a single "/" — and never name a scheme or host of its own.
// Segments are split before decoding, as ES does, so an encoded "/" inside a
// date-math expression stays inside its segment. A "." or ".." segment (encoded
// or not) is refused: a proxy in front of the cluster, or the endpoint's own path
// prefix, would resolve it into a different path than the one classified.
func parseRequestPath(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("path %q must start with / (a path and query string, without scheme or host)", p)
	}
	if strings.HasPrefix(p, "//") {
		return nil, fmt.Errorf("path %q must not name a host; requests always go to the asset's address", p)
	}
	u, err := url.Parse(p)
	if err != nil {
		return nil, fmt.Errorf("invalid path %q: %w", p, err)
	}
	var segs []string
	for _, raw := range strings.Split(u.EscapedPath(), "/") {
		if raw == "" {
			continue
		}
		seg, err := url.PathUnescape(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid path %q: %w", p, err)
		}
		if seg == "." || seg == ".." {
			return nil, fmt.Errorf("path %q must not contain . or .. segments", p)
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

func normalizeMethod(m string) string { return strings.ToUpper(strings.TrimSpace(m)) }

func isReadMethod(m string) bool { return m == "GET" || m == "HEAD" }

// classifyRequest is the request tool's classification. A path the handler will
// refuse is still answered — as admin on every index — since the host asks before
// the handler ever sees the call.
func classifyRequest(args requestArgs) (string, []string) {
	segs, err := parseRequestPath(args.Path)
	if err != nil {
		return actionAdmin, []string{allIndices}
	}
	action, resources := classifyPath(normalizeMethod(args.Method), segs, args.Body)
	return action, resourceSet(resources)
}

// Root APIs that act on indices; called without an index they act on all of them.
var indexScopedAPIs = setOf(
	"_search", "_msearch", "_count", "_mget", "_field_caps", "_validate", "_explain",
	"_termvectors", "_mtermvectors", "_sql", "_eql", "_async_search", "_pit", "_render",
	"_bulk", "_update_by_query", "_delete_by_query", "_reindex",
	"_refresh", "_flush", "_forcemerge", "_cache", "_stats", "_segments", "_recovery", "_shard_stores",
	"_mapping", "_settings", "_alias", "_aliases",
)

// _cat APIs whose optional trailing segment is an index expression.
var catIndexAPIs = setOf("indices", "count", "shards", "segments", "recovery")

func classifyPath(method string, segs []string, body string) (string, []string) {
	if len(segs) == 0 {
		if isReadMethod(method) {
			return actionRead, nil
		}
		return actionAdmin, nil
	}
	first := segs[0]
	switch {
	case !strings.HasPrefix(first, "_") || strings.Split(first, ",")[0] == "_all":
		return classifyAPI(method, segs[1:], indexResources(first), body)
	case indexScopedAPIs[first]:
		return classifyAPI(method, segs, nil, body)
	case first == "_data_stream":
		// _modify, _migrate, _promote, _stats name the API; the data stream, if
		// any, follows it.
		if len(segs) > 1 && strings.HasPrefix(segs[1], "_") {
			return classifyAPI(method, segs, targetsAt(segs, 2), body)
		}
		return classifyAPI(method, segs, targetsAt(segs, 1), body)
	case first == "_resolve":
		return classifyAPI(method, segs, targetsAt(segs, 2), body)
	case first == "_cat" && len(segs) > 1 && catIndexAPIs[segs[1]]:
		return classifyAPI(method, segs, targetsAt(segs, 2), body)
	}
	// A cluster-level API: its leading segment is the resource.
	if isReadMethod(method) {
		return actionRead, []string{first}
	}
	return actionAdmin, []string{first}
}

// targetsAt is the index expression at segs[i], or nil when the path names none.
func targetsAt(segs []string, i int) []string {
	if len(segs) <= i {
		return nil
	}
	return indexResources(segs[i])
}

// Search-family APIs: a POST (or GET) only reads.
var readAPIs = setOf(
	"_search", "_msearch", "_count", "_mget", "_field_caps", "_validate", "_explain",
	"_termvectors", "_mtermvectors", "_sql", "_eql", "_async_search", "_pit", "_render",
)

// Maintenance APIs ES also accepts as GET; they change the index all the same.
var maintenanceAPIs = setOf("_refresh", "_flush", "_forcemerge", "_cache")

// classifyAPI classifies api (the path after the index expression, or the whole
// path for a root API) against targets, the indices the path names — nil when
// it names none, which for an index API means every index.
func classifyAPI(method string, api []string, targets []string, body string) (string, []string) {
	scope := targets
	if scope == nil {
		scope = []string{allIndices}
	}
	name := ""
	if len(api) > 0 {
		name = api[0]
	}

	switch name {
	case "_bulk":
		resources, deletes := bulkTargets(body, scope)
		if isReadMethod(method) {
			return actionRead, resources
		}
		if method != "POST" && method != "PUT" {
			return actionAdmin, resources
		}
		// A bulk that deletes documents is a delete: a `deny delete:…` must not be
		// escapable by wrapping the delete in _bulk.
		if deletes {
			return actionDelete, resources
		}
		return actionWrite, resources
	case "_mget":
		return readOrAdmin(method, mgetTargets(body, targets))
	case "_msearch":
		return readOrAdmin(method, msearchTargets(body, scope))
	case "_reindex":
		if isReadMethod(method) {
			return actionRead, scope
		}
		if len(api) == 1 {
			return actionAdmin, reindexTargets(body)
		}
		return actionAdmin, scope
	case "_aliases":
		if isReadMethod(method) {
			return actionRead, scope
		}
		if targets != nil {
			return actionAdmin, scope
		}
		// remove_index deletes the index: classified as the delete it is.
		resources, removesIndex := aliasActionTargets(body)
		if removesIndex {
			return actionDelete, resources
		}
		return actionAdmin, resources
	case "_rollover", "_shrink", "_split", "_clone":
		// The new index named after the API is touched as much as the source.
		return actionAdmin, append(append([]string{}, scope...), targetsAt(api, 1)...)
	}

	if maintenanceAPIs[name] {
		return actionAdmin, scope
	}
	if isReadMethod(method) {
		return actionRead, scope
	}
	if readAPIs[name] {
		switch {
		case method == "POST":
			return actionRead, scope
		case method == "DELETE" && closesReadContext(api, targets):
			return actionRead, scope
		}
		return actionAdmin, scope
	}

	switch {
	case name == "" && method == "DELETE" && targets != nil:
		return actionDelete, scope // delete index
	case name == "_doc" && (method == "PUT" || method == "POST"):
		return actionWrite, scope
	case name == "_doc" && method == "DELETE":
		return actionDelete, scope
	case name == "_create" && (method == "PUT" || method == "POST"):
		return actionWrite, scope
	case name == "_update" && method == "POST":
		return actionWrite, scope
	case name == "_update_by_query" && len(api) == 1 && method == "POST":
		return actionWrite, scope
	case name == "_delete_by_query" && len(api) == 1 && method == "POST":
		return actionDelete, scope
	case name == "_data_stream" && method == "DELETE" && targets != nil:
		return actionDelete, scope // deleting a data stream deletes its backing indices
	}
	return actionAdmin, scope
}

// closesReadContext reports the two DELETEs that release a search context rather
// than deleting anything: clearing a scroll and closing a point in time. Deleting
// a stored async / EQL / SQL search result is not one of them and stays admin.
func closesReadContext(api []string, targets []string) bool {
	if targets != nil {
		return false
	}
	switch api[0] {
	case "_search":
		return len(api) >= 2 && api[1] == "scroll"
	case "_pit":
		return true
	}
	return false
}

func readOrAdmin(method string, resources []string) (string, []string) {
	if isReadMethod(method) || method == "POST" {
		return actionRead, resources
	}
	return actionAdmin, resources
}

// indexResources turns one index expression into resources: comma-separated
// names, `_all` as `*`, a date-math name as the wildcard it can resolve to, and
// exclusions (`-name`) dropped since they only narrow the expression. An
// expression that names nothing positive stands for every index.
func indexResources(expr string) []string {
	var out []string
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "", strings.HasPrefix(part, "-"):
			continue
		case part == "_all":
			part = allIndices
		case strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">"):
			part = dateMathPattern(part[1 : len(part)-1])
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return []string{allIndices}
	}
	return out
}

// dateMathPattern widens a date-math index name (`logs-{now/d}`, braces nested
// for the format) to a wildcard: every `{…}` group becomes `*`.
func dateMathPattern(s string) string {
	var sb strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '{':
			if depth == 0 {
				sb.WriteByte('*')
			}
			depth++
		case r == '}' && depth > 0:
			depth--
		case depth == 0:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// indexValue decodes an index given as a string expression or a list of them.
func indexValue(raw json.RawMessage) ([]string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return indexResources(s), true
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return nil, false
	}
	var out []string
	for _, e := range list {
		out = append(out, indexResources(e)...)
	}
	return out, true
}

// ndjsonLines splits an NDJSON body into its lines as ES reads them: every "\n"
// ends one, and text after the last "\n" is a line of its own (the request tool
// adds the final newline ES requires). Blank lines are kept — ES pairs _bulk and
// _msearch lines by position, blank ones included.
func ndjsonLines(body string) []string {
	lines := strings.Split(body, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

// bulkTargets collects the indices a bulk body writes: each action line's
// _index, the path's scope for an action line without one (or for a body with no
// action at all). Lines pair as ES pairs them: a blank action line is skipped,
// and the line after index / create / update is its document, blank or not. A
// line that is not an action stops the scan and widens the call to every index —
// the pairing of the remaining lines can no longer be trusted.
func bulkTargets(body string, scope []string) (resources []string, deletes bool) {
	lines := ndjsonLines(body)
	usesScope, sawAction := false, false
	for i := 0; i < len(lines); i++ {
		if isBlank(lines[i]) {
			continue
		}
		sawAction = true
		var action map[string]json.RawMessage
		if json.Unmarshal([]byte(lines[i]), &action) != nil || len(action) != 1 {
			resources = append(resources, allIndices)
			break
		}
		var op string
		var meta struct {
			Index string `json:"_index"`
		}
		for k, v := range action {
			op = k
			if json.Unmarshal(v, &meta) != nil {
				op = ""
			}
		}
		switch op {
		case "delete":
			deletes = true
		case "index", "create", "update":
			i++ // the source / partial document line, blank or not
		default:
			resources = append(resources, allIndices)
			i = len(lines)
			continue
		}
		if meta.Index == "" {
			usesScope = true
		} else {
			resources = append(resources, indexResources(meta.Index)...)
		}
	}
	if usesScope || !sawAction {
		resources = append(resources, scope...)
	}
	return resources, deletes
}

// msearchTargets collects each search header's index and indices, the path's
// scope for a header naming neither. Lines pair as ES pairs them: header, body,
// header, … by position, a blank header line being an empty header; only a body
// that starts with "\n" has that one line skipped.
func msearchTargets(body string, scope []string) []string {
	lines := ndjsonLines(body)
	if strings.HasPrefix(body, "\n") {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return scope
	}
	var out []string
	for i := 0; i < len(lines); i += 2 {
		if isBlank(lines[i]) {
			out = append(out, scope...)
			continue
		}
		var header struct {
			Index   json.RawMessage `json:"index"`
			Indices json.RawMessage `json:"indices"`
		}
		if json.Unmarshal([]byte(lines[i]), &header) != nil {
			return append(out, allIndices)
		}
		named := false
		for _, raw := range []json.RawMessage{header.Index, header.Indices} {
			if raw == nil {
				continue
			}
			indices, ok := indexValue(raw)
			if !ok {
				return append(out, allIndices)
			}
			out = append(out, indices...)
			named = true
		}
		if !named {
			out = append(out, scope...)
		}
	}
	return out
}

// mgetTargets collects each doc's _index; ids, and docs without one, read from
// the path's index (every index when the path names none).
func mgetTargets(body string, targets []string) []string {
	var req struct {
		Docs []struct {
			Index string `json:"_index"`
		} `json:"docs"`
		IDs []json.RawMessage `json:"ids"`
	}
	if strings.TrimSpace(body) != "" && json.Unmarshal([]byte(body), &req) != nil {
		return []string{allIndices}
	}
	usesPath := len(req.IDs) > 0 || len(req.Docs) == 0
	var out []string
	for _, d := range req.Docs {
		if d.Index == "" {
			usesPath = true
			continue
		}
		out = append(out, indexResources(d.Index)...)
	}
	if usesPath {
		if targets == nil {
			return append(out, allIndices)
		}
		out = append(out, targets...)
	}
	return out
}

// reindexTargets is the source and destination of a reindex; either one missing
// or unreadable widens the call to every index, and so does a script, which can
// send each document to any index (ctx._index).
func reindexTargets(body string) []string {
	var req struct {
		Source struct {
			Index json.RawMessage `json:"index"`
		} `json:"source"`
		Dest struct {
			Index json.RawMessage `json:"index"`
		} `json:"dest"`
		Script json.RawMessage `json:"script"`
	}
	if json.Unmarshal([]byte(body), &req) != nil || req.Script != nil {
		return []string{allIndices}
	}
	var out []string
	for _, raw := range []json.RawMessage{req.Source.Index, req.Dest.Index} {
		indices, ok := indexValue(raw)
		if !ok {
			return append(out, allIndices)
		}
		out = append(out, indices...)
	}
	return out
}

// aliasActionTargets collects the indices an _aliases body acts on, and whether
// any action is remove_index. An unreadable body, or an action naming no index,
// widens the call to every index.
func aliasActionTargets(body string) (resources []string, removesIndex bool) {
	var req struct {
		Actions []map[string]struct {
			Index   json.RawMessage `json:"index"`
			Indices json.RawMessage `json:"indices"`
		} `json:"actions"`
	}
	if json.Unmarshal([]byte(body), &req) != nil || len(req.Actions) == 0 {
		return []string{allIndices}, false
	}
	for _, action := range req.Actions {
		for op, meta := range action {
			if op == "remove_index" {
				removesIndex = true
			}
			found := false
			for _, raw := range []json.RawMessage{meta.Index, meta.Indices} {
				if indices, ok := indexValue(raw); ok {
					resources = append(resources, indices...)
					found = true
				}
			}
			if !found {
				resources = append(resources, allIndices)
			}
		}
	}
	return resources, removesIndex
}

// resourceSet dedupes and sorts resources; never nil, so "no resource" is an
// explicit empty list.
func resourceSet(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range in {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
