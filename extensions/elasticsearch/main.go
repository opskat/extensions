// Command elasticsearch is OpsKat's Elasticsearch extension.
//
// The guest is a WASI reactor: the host runs _initialize and never calls main, so
// every declaration happens in init(). manifest.json carries only the capability
// grants; the host reads the rest back through describe().
//
// The extension never holds a secret. The asset's credentials are rendered and
// injected by the host into requests to the asset's endpoint (see esAuth), which
// is why manifest.json declares network.assetEndpoint and no credentials:read.
package main

import (
	"encoding/json"

	opskat "github.com/opskat/opskat/pkg/extsdk"
)

func main() {}

func init() {
	opskat.Extension(opskat.Meta{
		Icon:        "elasticsearch",
		DisplayName: "extension.displayName",
		Description: "extension.description",
		PolicyType:  "elasticsearch",
	})

	opskat.AssetType[esConfig]("elasticsearch").
		Name("assetType.elasticsearch.name").
		Connection(opskat.Connection{SSHTunnel: true, ProxyChain: true, TLS: true}).
		Auth(esAuth).
		TestConnection(testConnection)

	opskat.RegisterConfigValidator(func(raw json.RawMessage) []opskat.ValidationError {
		return validateConfig(raw)
	})

	// Reading is granted to every new asset; writing documents is one group away.
	// delete and admin are in no group, so they ask unless a rule allows them.
	opskat.PolicyGroup("ext:elasticsearch:read-only").
		Name("policy.readOnly.name").Description("policy.readOnly.description").
		Allow(actionRead).Default()
	opskat.PolicyGroup("ext:elasticsearch:read-write").
		Name("policy.readWrite.name").Description("policy.readWrite.description").
		Allow(actionRead, actionWrite)

	// request classifies each call into an action and every index it touches (see
	// policy.go); the host denies it if any index hits a deny rule and runs it
	// unattended only if every index is allowed. Its body may be large (a bulk
	// load), so opsctl can read it from a file.
	opskat.Tool("request", handleRequest).
		RejectArgs(rejectRequest).
		PolicyResources(requestActions, classifyRequest).
		FileParam("body").
		Doc("tools.request.description")
	opskat.Tool("health", handleHealth).
		Policy(actionRead).Resource(func(healthArgs) string { return "_cluster" }).
		Doc("tools.health.description")
	opskat.Tool("indices", handleIndices).
		RejectArgs(rejectIndices).
		PolicyResources([]string{actionRead}, classifyIndices).
		Doc("tools.indices.description")
	opskat.Tool("mapping", handleMapping).
		RejectArgs(rejectMapping).
		PolicyResources([]string{actionRead}, classifyMapping).
		Doc("tools.mapping.description")
	opskat.Tool("search", handleSearch).
		RejectArgs(rejectSearch).
		PolicyResources([]string{actionRead}, classifySearch).
		Doc("tools.search.description")

	// The page keeps its console tabs per asset (console.go). Actions, not tools:
	// only the page calls them, and exec never lists them.
	opskat.RegisterAction("console.load", handleConsoleLoad)
	opskat.RegisterAction("console.save", handleConsoleSave)

	// Opening an asset shows the cluster page (frontend/, built into dist/frontend).
	// The page calls the tools above; the host runs a page's calls directly, since
	// they are the user's own actions, while AI and opsctl calls stay gated.
	opskat.Frontend(opskat.FrontendSpec{
		Entry:  "frontend/index.js",
		Styles: "frontend/style.css",
		Pages: []opskat.Page{
			{ID: "cluster", Slot: "asset.connect", Name: "page.cluster.title", Component: "ElasticsearchPage"},
		},
	})
}
