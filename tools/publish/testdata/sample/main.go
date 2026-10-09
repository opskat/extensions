// Command sample is the extension the publisher's tests package and describe: a
// real WASM guest, so the metadata the tests read back comes through the same
// describe() path the app takes.
package main

import opskat "github.com/opskat/opskat/pkg/extsdk"

func main() {}

type sampleConfig struct {
	Host string `json:"host" title:"Host"`
}

type pingArgs struct {
	Msg string `json:"msg,omitempty" desc:"Message to echo back"`
}

func init() {
	opskat.Extension(opskat.Meta{
		Icon:        "radar",
		DisplayName: "extension.displayName",
		Description: "extension.description",
		PolicyType:  "sample",
	})
	opskat.AssetType[sampleConfig]("sample").Name("assetType.sample.name")
	opskat.Tool("ping", func(_ *opskat.ToolContext, args pingArgs) (any, error) {
		return map[string]string{"msg": args.Msg}, nil
	}).Doc("tools.ping.description").Policy("read")
}
