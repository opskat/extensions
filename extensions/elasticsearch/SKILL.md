---
name: elasticsearch
description: Elasticsearch clusters (7.10+, 8.x, 9.x). Use it to check cluster health, list indices, read mappings, search documents, and send any other REST request to the cluster.
---

# Elasticsearch

An `elasticsearch` asset is one cluster. Every request goes to the address configured
on the asset, through its SSH tunnel / proxy / TLS settings, with its credentials
added by OpsKat — you never handle a password, and you cannot point a request at
another host.

## Prefer the convenience tools

Reach for these first; their results are compact and their errors say what went wrong:

- `health` — cluster name, version, status (green / yellow / red), node count and
  shard counts (active, primary, unassigned). Start here when diagnosing a cluster.
- `indices` — index name, health, status, document count and store size (bytes).
  Narrow it with a pattern such as `logs-*`; hidden and system indices (names
  starting with `.`) are listed only when asked for.
- `mapping` — the field mapping of an index. Read it before writing a query against
  fields you have not seen.
- `search` — hits (`_index`, `_id`, `_source`), total and time taken. Give either a
  Query DSL object (the value of `"query"`, e.g. `{"match":{"message":"timeout"}}`) or
  a Lucene query string (`level:error AND service:api`), not both. Results are capped
  at 100 hits; aggregate or narrow the query rather than paging through thousands.

These tools fail when Elasticsearch answers with an error, and the error carries the
HTTP status and Elasticsearch's own `type` and `reason` (for example
`index_not_found_exception: no such index [logs]`).

## `request` — everything else

`request` sends any REST call: a method, a path with its query string
(`/logs-*/_count?q=level:error`) and optionally a body. The path must start with `/`
and never contain a scheme or host. The result is `{status, body}` with the body
parsed when it is JSON and as text otherwise; a 4xx or 5xx answer is a result, not a
failure, so read `status` before trusting `body`. Only a cluster that cannot be
reached fails the call.

Use APIs common to 7.10+, 8.x and 9.x. Ask `_cat` APIs for JSON (`?format=json`).
`_bulk` and `_msearch` take NDJSON bodies (one JSON object per line).

Large bodies — a bulk load, a long NDJSON file — should not be pasted onto the
command line. From opsctl, read the body from a file or stdin:

```
opsctl exec <asset> -- request --method POST --path /_bulk --body-file docs.ndjson
cat docs.ndjson | opsctl exec <asset> -- request --method POST --path /_bulk --body-file -
```

`--body-file` exists only in opsctl; through `exec` in the assistant, pass `--body`.

## What the permissions mean

Every call is classified as one action on a set of resources:

- `read` — GET and HEAD, plus searches sent as POST (`_search`, `_msearch`, `_count`,
  `_mget`, `_field_caps`, `_validate`, `_explain`, term vectors, SQL, EQL, async
  search, search templates, point in time and scroll, including closing them).
  `health`, `indices`, `mapping` and `search` are always `read`.
- `write` — document writes: `_doc`, `_create`, `_update`, `_bulk`, `_update_by_query`.
- `delete` — deleting documents or indices, `_delete_by_query`, a `_bulk` body that
  contains `delete` actions, and `_aliases` with `remove_index`.
- `admin` — everything else: creating indices, changing mappings or settings,
  aliases, open / close, refresh / flush / force merge, `_reindex`, rollover /
  shrink / split / clone, templates, ILM, ingest pipelines, snapshots, cluster
  settings, security, task cancellation. A request that is not recognized is `admin`.

The resources are the indices the call touches: each name in a comma-separated list,
the indices named inside `_bulk`, `_mget` and `_msearch` bodies, and both source and
destination of `_reindex`. `_all`, or an index API called without an index, is `*`;
a pattern such as `logs-*` stays a pattern. A cluster-level API is its first path
segment: `_cluster`, `_nodes`, `_snapshot`, `_security`, `_ilm`, `_ingest`, `_tasks`.

A new asset allows `read`. The read-write group adds `write`. `delete` and `admin`
ask the user unless a rule allows them. The user can refine this with rules on the
asset, for example:

- `deny delete:prod-*` — never delete from `prod-` indices, even when other deletes
  are allowed;
- `allow write:logs-*` — write to `logs-` indices without asking;
- `allow admin:_snapshot` — manage snapshots without asking.

A call naming several indices is refused if any one of them is denied, and runs
unattended only if every one is allowed; otherwise the user is asked. So
`DELETE /logs-old,prod-1` is refused under `deny delete:prod-*`, and a wildcard such
as `DELETE /*` is refused as well, since it could reach `prod-` indices. When a call
is refused, say so and let the user change the rules — do not look for another
request that reaches the same indices.
