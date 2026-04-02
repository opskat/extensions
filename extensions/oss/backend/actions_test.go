package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/opskat/extensions/sdk/go/opskat"
	. "github.com/smartystreets/goconvey/convey"
)

func TestBrowseAction(t *testing.T) {
	Convey("browse action lists objects", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>default-bucket</Name><Prefix></Prefix><MaxKeys>1000</MaxKeys>
  <IsTruncated>false</IsTruncated>
  <Contents><Key>file1.txt</Key><Size>100</Size><LastModified>2024-01-01T00:00:00Z</LastModified><ETag>"e1"</ETag><StorageClass>STANDARD</StorageClass></Contents>
</ListBucketResult>`)
		})
		defer th.Close()

		result, err := th.CallAction("browse", map[string]any{}, nil)
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		objects := out["objects"].([]any)
		So(len(objects), ShouldBeGreaterThan, 0)
	})
}

func TestSearchAction(t *testing.T) {
	Convey("search action filters by prefix", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>default-bucket</Name><Prefix>doc</Prefix><MaxKeys>100</MaxKeys>
  <IsTruncated>false</IsTruncated>
  <Contents><Key>docs/readme.txt</Key><Size>50</Size><LastModified>2024-01-01T00:00:00Z</LastModified><ETag>"s1"</ETag><StorageClass>STANDARD</StorageClass></Contents>
</ListBucketResult>`)
		})
		defer th.Close()

		result, err := th.CallAction("search", map[string]any{"prefix": "doc"}, nil)
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		objects := out["objects"].([]any)
		So(len(objects), ShouldEqual, 1)
	})
}

func TestPreviewAction(t *testing.T) {
	Convey("preview action returns object content", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "preview content")
		})
		defer th.Close()

		result, err := th.CallAction("preview", map[string]any{"key": "readme.txt"}, nil)
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["content"], ShouldEqual, "preview content")
		So(out["contentType"], ShouldEqual, "text/plain")
	})
}

func TestGetPresignedURLAction(t *testing.T) {
	Convey("get_presigned_url action returns a URL", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		defer th.Close()

		result, err := th.CallAction("get_presigned_url", map[string]any{"key": "file.zip"}, nil)
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["url"], ShouldNotBeEmpty)
	})
}

func TestBatchDeleteAction(t *testing.T) {
	Convey("batch_delete action deletes with progress events", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Deleted><Key>a.txt</Key></Deleted>
  <Deleted><Key>b.txt</Key></Deleted>
</DeleteResult>`)
		})
		defer th.Close()

		var events []opskat.TestEvent
		result, err := th.CallAction("batch_delete", map[string]any{
			"keys": []string{"a.txt", "b.txt"},
		}, func(e opskat.TestEvent) {
			events = append(events, e)
		})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["deletedCount"], ShouldEqual, float64(2))

		So(len(events), ShouldBeGreaterThan, 0)
		So(events[len(events)-1].Type, ShouldEqual, "progress")
	})
}

func TestBatchCopyAction(t *testing.T) {
	Convey("batch_copy action copies with progress events", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<CopyObjectResult><ETag>"c"</ETag><LastModified>2024-01-01T00:00:00Z</LastModified></CopyObjectResult>`)
		})
		defer th.Close()

		var events []opskat.TestEvent
		result, err := th.CallAction("batch_copy", map[string]any{
			"keys":       []string{"x.txt", "y.txt"},
			"destPrefix": "backup/",
		}, func(e opskat.TestEvent) {
			events = append(events, e)
		})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["copiedCount"], ShouldEqual, float64(2))

		So(len(events), ShouldBeGreaterThan, 0)
	})
}
