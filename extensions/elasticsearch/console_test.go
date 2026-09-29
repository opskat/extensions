package main

import (
	"testing"

	opskat "github.com/opskat/opskat/pkg/extsdk"

	. "github.com/smartystreets/goconvey/convey"
)

// The page saves its console tabs through console.save and reads them back
// through console.load; both run against the page's asset.

func loadConsoles(host *opskat.TestHost, asset opskat.Asset) (any, error) {
	return host.CallAction(asset, "console.load", map[string]any{}, nil)
}

func TestConsoleActions(t *testing.T) {
	other := opskat.Asset{ID: 12, Name: "metrics cluster", Type: "elasticsearch"}
	saved := map[string]any{"consoles": []any{
		map[string]any{"number": float64(1), "text": "GET /_cluster/health"},
		map[string]any{"number": float64(3), "text": "# bulk\nPOST /_bulk\n{\"index\":{}}\n{\"a\":1}"},
	}}

	Convey("console tabs are saved per asset", t, func() {
		host := opskat.NewTestHost()
		defer host.Close()

		Convey("an asset with nothing saved loads an empty list", func() {
			got, err := loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, map[string]any{"consoles": []any{}})
		})

		Convey("what was saved loads back as it was", func() {
			_, err := host.CallAction(esAsset, "console.save", saved, nil)
			So(err, ShouldBeNil)
			got, err := loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, saved)
		})

		Convey("a later save replaces the earlier one, an empty list included", func() {
			_, err := host.CallAction(esAsset, "console.save", saved, nil)
			So(err, ShouldBeNil)
			_, err = host.CallAction(esAsset, "console.save", map[string]any{"consoles": []any{}}, nil)
			So(err, ShouldBeNil)
			got, err := loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, map[string]any{"consoles": []any{}})
		})

		Convey("one asset's consoles are not another's", func() {
			_, err := host.CallAction(esAsset, "console.save", saved, nil)
			So(err, ShouldBeNil)
			got, err := loadConsoles(host, other)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, map[string]any{"consoles": []any{}})

			_, err = host.CallAction(other, "console.save", map[string]any{"consoles": []any{
				map[string]any{"number": 1, "text": "GET /metrics/_search"},
			}}, nil)
			So(err, ShouldBeNil)
			got, err = loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, saved)
		})

		Convey("a call not scoped to an asset is refused", func() {
			_, err := host.CallAction(opskat.Asset{}, "console.save", saved, nil)
			So(err, ShouldNotBeNil)
			_, err = loadConsoles(host, opskat.Asset{})
			So(err, ShouldNotBeNil)
		})

		Convey("malformed save arguments are refused and leave the saved tabs alone", func() {
			_, err := host.CallAction(esAsset, "console.save", saved, nil)
			So(err, ShouldBeNil)
			for _, bad := range []any{
				map[string]any{},
				map[string]any{"consoles": nil},
				map[string]any{"consoles": "GET /"},
				map[string]any{"consoles": []any{map[string]any{"number": 0, "text": ""}}},
				map[string]any{"consoles": []any{map[string]any{"number": 1.5, "text": ""}}},
				map[string]any{"consoles": []any{map[string]any{"number": 1}}},
				map[string]any{"consoles": []any{map[string]any{"number": 1, "text": 7}}},
				map[string]any{"consoles": []any{
					map[string]any{"number": 2, "text": "a"},
					map[string]any{"number": 2, "text": "b"},
				}},
				map[string]any{"consoles": []any{}, "assetId": 12},
				[]any{},
			} {
				_, err := host.CallAction(esAsset, "console.save", bad, nil)
				So(err, ShouldNotBeNil)
			}
			got, err := loadConsoles(host, esAsset)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, saved)
		})

		Convey("load takes no arguments", func() {
			_, err := host.CallAction(esAsset, "console.load", map[string]any{"asset": 12}, nil)
			So(err, ShouldNotBeNil)
		})
	})
}
