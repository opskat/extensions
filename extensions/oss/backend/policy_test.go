package main

import (
	"testing"

	"github.com/opskat/extensions/sdk/go/opskat"
	. "github.com/smartystreets/goconvey/convey"
)

func TestCheckPolicy(t *testing.T) {
	Convey("policy checker", t, func() {
		th := opskat.NewTestHost()
		defer th.Close()

		Convey("list_buckets maps to list/*", func() {
			action, resource, err := th.CheckPolicy("list_buckets", map[string]any{})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "list")
			So(resource, ShouldEqual, "*")
		})

		Convey("list_objects maps to list/bucket/*", func() {
			action, resource, err := th.CheckPolicy("list_objects", map[string]any{"bucket": "mybucket", "prefix": "docs/"})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "list")
			So(resource, ShouldEqual, "mybucket/*")
		})

		Convey("get_object_info maps to read/bucket/key", func() {
			action, resource, err := th.CheckPolicy("get_object_info", map[string]any{"bucket": "b", "key": "k"})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "read")
			So(resource, ShouldEqual, "b/k")
		})

		Convey("download_object maps to read", func() {
			action, resource, err := th.CheckPolicy("download_object", map[string]any{"bucket": "b", "key": "file.txt"})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "read")
			So(resource, ShouldEqual, "b/file.txt")
		})

		Convey("presign_url maps to read", func() {
			action, resource, err := th.CheckPolicy("presign_url", map[string]any{"bucket": "b", "key": "file.txt"})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "read")
			So(resource, ShouldEqual, "b/file.txt")
		})

		Convey("upload_object maps to write", func() {
			action, resource, err := th.CheckPolicy("upload_object", map[string]any{"bucket": "b", "key": "new.txt"})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "write")
			So(resource, ShouldEqual, "b/new.txt")
		})

		Convey("copy_object maps to write", func() {
			action, resource, err := th.CheckPolicy("copy_object", map[string]any{"sourceKey": "a.txt", "destBucket": "b", "destKey": "c.txt"})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "write")
			So(resource, ShouldEqual, "b/c.txt")
		})

		Convey("move_object maps to write", func() {
			action, resource, err := th.CheckPolicy("move_object", map[string]any{"sourceKey": "a.txt", "destBucket": "b", "destKey": "c.txt"})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "write")
			So(resource, ShouldEqual, "b/c.txt")
		})

		Convey("delete_object maps to delete", func() {
			action, resource, err := th.CheckPolicy("delete_object", map[string]any{"bucket": "b", "key": "old.txt"})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "delete")
			So(resource, ShouldEqual, "b/old.txt")
		})

		Convey("delete_objects maps to delete", func() {
			action, resource, err := th.CheckPolicy("delete_objects", map[string]any{"bucket": "b", "keys": []string{"a", "b"}})
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "delete")
			So(resource, ShouldEqual, "b/*")
		})
	})
}
