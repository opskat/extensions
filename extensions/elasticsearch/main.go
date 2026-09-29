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
		Icon:        "search",
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
}
