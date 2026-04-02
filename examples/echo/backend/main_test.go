package main

import (
	"encoding/json"
	"testing"

	"github.com/opskat/extensions/sdk/go/opskat"
	. "github.com/smartystreets/goconvey/convey"
)

func TestEchoTool(t *testing.T) {
	Convey("echo extension", t, func() {
		// init() registers handlers, so they're already registered
		th := opskat.NewTestHost()
		defer th.Close()

		Convey("echo tool returns message with counter", func() {
			result, err := th.CallTool("echo", map[string]string{"message": "hello"})
			So(err, ShouldBeNil)

			b, _ := json.Marshal(result)
			var out map[string]any
			json.Unmarshal(b, &out)
			So(out["echo"], ShouldEqual, "hello")
			So(out["counter"], ShouldNotBeNil)
		})

		Convey("echo action emits progress events", func() {
			var events []opskat.TestEvent
			result, err := th.CallAction("echo_stream", map[string]string{"message": "world"}, func(e opskat.TestEvent) {
				events = append(events, e)
			})
			So(err, ShouldBeNil)
			So(len(events), ShouldBeGreaterThan, 0)
			So(events[0].Type, ShouldEqual, "echo")

			b, _ := json.Marshal(result)
			var out map[string]string
			json.Unmarshal(b, &out)
			So(out["status"], ShouldEqual, "done")
		})

		Convey("policy checker classifies echo tool", func() {
			action, resource, err := th.CheckPolicy("echo", map[string]string{"message": "test"})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "echo")
			So(resource, ShouldEqual, "*")
		})
	})
}
