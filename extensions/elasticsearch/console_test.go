package main

import (
	"encoding/json"
	"testing"

	opskat "github.com/opskat/opskat/pkg/extsdk"

	. "github.com/smartystreets/goconvey/convey"
)

// The page saves its console tabs through the page-only tools console.save and
// reads them back through console.load; both run against the page's asset.
// console.save takes the tabs as JSON text in "consoles": a tool parameter is a
// scalar or a list of strings, never a nested object.

func saveArgs(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return map[string]any{"consoles": string(raw)}
}

func loadConsoles(host *opskat.TestHost, asset opskat.Asset) (any, error) {
	return host.CallTool(asset, "console.load", map[string]any{})
}

func TestConsoleActions(t *testing.T) {
	other := opskat.Asset{ID: 12, Name: "metrics cluster", Type: "elasticsearch"}
	savedList := []any{
		map[string]any{"number": float64(1), "text": "GET /_cluster/health"},
		map[string]any{"number": float64(3), "text": "# bulk\nPOST /_bulk\n{\"index\":{}}\n{\"a\":1}"},
	}
	saved := map[string]any{"consoles": savedList}

	Convey("console tabs are saved per asset", t, func() {
		host := opskat.NewTestHost()
		defer host.Close()

		Convey("an asset with nothing saved loads an empty list", func() {
			got, err := loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, map[string]any{"consoles": []any{}})
		})

		Convey("what was saved loads back as it was", func() {
			_, err := host.CallTool(esAsset, "console.save", saveArgs(savedList))
			So(err, ShouldBeNil)
			got, err := loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, saved)
		})

		Convey("a later save replaces the earlier one, an empty list included", func() {
			_, err := host.CallTool(esAsset, "console.save", saveArgs(savedList))
			So(err, ShouldBeNil)
			_, err = host.CallTool(esAsset, "console.save", saveArgs([]any{}))
			So(err, ShouldBeNil)
			got, err := loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, map[string]any{"consoles": []any{}})
		})

		Convey("one asset's consoles are not another's", func() {
			_, err := host.CallTool(esAsset, "console.save", saveArgs(savedList))
			So(err, ShouldBeNil)
			got, err := loadConsoles(host, other)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, map[string]any{"consoles": []any{}})

			_, err = host.CallTool(other, "console.save", saveArgs([]any{
				map[string]any{"number": 1, "text": "GET /metrics/_search"},
			}))
			So(err, ShouldBeNil)
			got, err = loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, saved)
		})

		Convey("a call not scoped to an asset is refused", func() {
			_, err := host.CallTool(opskat.Asset{}, "console.save", saveArgs(savedList))
			So(err, ShouldNotBeNil)
			_, err = loadConsoles(host, opskat.Asset{})
			So(err, ShouldNotBeNil)
		})

		Convey("malformed save arguments are refused and leave the saved tabs alone", func() {
			_, err := host.CallTool(esAsset, "console.save", saveArgs(savedList))
			So(err, ShouldBeNil)
			for _, bad := range []map[string]any{
				{},
				{"consoles": ""},
				{"consoles": "null"},
				{"consoles": "{}"},
				{"consoles": `"GET /"`},
				{"consoles": "not json"},
				{"consoles": `[{"number":0,"text":""}]`},
				{"consoles": `[{"number":1.5,"text":""}]`},
				{"consoles": `[{"number":1}]`},
				{"consoles": `[{"number":1,"text":7}]`},
				{"consoles": `[{"number":2,"text":"a"},{"number":2,"text":"b"}]`},
				{"consoles": `[{"number":1,"text":"a","extra":true}]`},
				{"consoles": `[] []`},
			} {
				_, err := host.CallTool(esAsset, "console.save", bad)
				So(err, ShouldNotBeNil)
			}
			got, err := loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, saved)
		})
	})
}
