package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestListBuckets(t *testing.T) {
	Convey("list_buckets", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Buckets>
    <Bucket><Name>bucket-a</Name><CreationDate>2024-01-01T00:00:00Z</CreationDate></Bucket>
    <Bucket><Name>bucket-b</Name><CreationDate>2024-06-15T12:00:00Z</CreationDate></Bucket>
  </Buckets>
  <Owner><ID>owner</ID><DisplayName>owner</DisplayName></Owner>
</ListAllMyBucketsResult>`)
		})
		defer th.Close()

		result, err := th.CallTool("list_buckets", map[string]any{})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)

		buckets := out["buckets"].([]any)
		So(len(buckets), ShouldEqual, 2)
		first := buckets[0].(map[string]any)
		So(first["name"], ShouldEqual, "bucket-a")
	})
}

func TestListBucketsEmpty(t *testing.T) {
	Convey("list_buckets returns empty list", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Buckets/>
  <Owner><ID>owner</ID><DisplayName>owner</DisplayName></Owner>
</ListAllMyBucketsResult>`)
		})
		defer th.Close()

		result, err := th.CallTool("list_buckets", map[string]any{})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)

		buckets := out["buckets"].([]any)
		So(len(buckets), ShouldEqual, 0)
	})
}

func TestListBuckets403(t *testing.T) {
	Convey("list_buckets returns error on 403 with XML body", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>AccessDenied</Code>
  <Message>Access Denied</Message>
  <RequestId>18A1D78F94796D4B</RequestId>
  <HostId>dd9025bab4ad464b049177c95eb6ebf374d3b3fd1af9251148b658df7ac2e3e8</HostId>
</Error>`)
		})
		defer th.Close()

		_, err := th.CallTool("list_buckets", map[string]any{})
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "list buckets")
	})
}

func TestListBuckets403EmptyBody(t *testing.T) {
	Convey("list_buckets returns error on 403 with empty body (EOF)", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			// empty body — simulates the EOF deserialization error
		})
		defer th.Close()

		_, err := th.CallTool("list_buckets", map[string]any{})
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "list buckets")
	})
}

func TestListBucketsRequestPath(t *testing.T) {
	Convey("list_buckets sends correct HTTP request", t, func() {
		var capturedMethod, capturedPath string
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			capturedMethod = r.Method
			capturedPath = r.URL.Path
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Buckets/>
  <Owner><ID>owner</ID><DisplayName>owner</DisplayName></Owner>
</ListAllMyBucketsResult>`)
		})
		defer th.Close()

		_, err := th.CallTool("list_buckets", map[string]any{})
		So(err, ShouldBeNil)
		So(capturedMethod, ShouldEqual, "GET")
		So(capturedPath, ShouldEqual, "/")
	})
}

func TestListObjects(t *testing.T) {
	Convey("list_objects", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>default-bucket</Name>
  <Prefix>docs/</Prefix>
  <Delimiter>/</Delimiter>
  <MaxKeys>1000</MaxKeys>
  <IsTruncated>false</IsTruncated>
  <Contents>
    <Key>docs/readme.txt</Key>
    <Size>1024</Size>
    <LastModified>2024-01-15T10:30:00Z</LastModified>
    <ETag>"abc123"</ETag>
    <StorageClass>STANDARD</StorageClass>
  </Contents>
  <CommonPrefixes><Prefix>docs/images/</Prefix></CommonPrefixes>
</ListBucketResult>`)
		})
		defer th.Close()

		result, err := th.CallTool("list_objects", map[string]any{"prefix": "docs/", "delimiter": "/"})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)

		objects := out["objects"].([]any)
		So(len(objects), ShouldEqual, 1)
		obj := objects[0].(map[string]any)
		So(obj["key"], ShouldEqual, "docs/readme.txt")
		So(obj["size"], ShouldEqual, float64(1024))

		prefixes := out["commonPrefixes"].([]any)
		So(len(prefixes), ShouldEqual, 1)
		So(prefixes[0], ShouldEqual, "docs/images/")
	})
}

func TestGetObjectInfo(t *testing.T) {
	Convey("get_object_info", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", "2048")
			w.Header().Set("Last-Modified", "Mon, 15 Jan 2024 10:30:00 GMT")
			w.Header().Set("Etag", `"abc123"`)
			w.WriteHeader(http.StatusOK)
		})
		defer th.Close()

		result, err := th.CallTool("get_object_info", map[string]any{"key": "readme.txt"})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["key"], ShouldEqual, "readme.txt")
		So(out["contentType"], ShouldEqual, "text/plain")
	})
}

func TestDownloadObjectSmall(t *testing.T) {
	Convey("download_object returns inline content for small files", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "hello world")
		})
		defer th.Close()

		result, err := th.CallTool("download_object", map[string]any{"key": "small.txt"})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["content"], ShouldEqual, "hello world")
		So(out["encoding"], ShouldEqual, "utf-8")
	})
}

func TestPresignURL(t *testing.T) {
	Convey("presign_url returns a signed URL", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		defer th.Close()

		result, err := th.CallTool("presign_url", map[string]any{"key": "file.zip", "expires": 600})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["url"], ShouldNotBeEmpty)
		So(out["expiresIn"], ShouldEqual, float64(600))
	})
}

func TestUploadObject(t *testing.T) {
	Convey("upload_object uploads inline content", t, func() {
		var receivedBody string
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PUT" {
				body, _ := io.ReadAll(r.Body)
				receivedBody = string(body)
				w.Header().Set("Etag", `"uploaded123"`)
				w.WriteHeader(http.StatusOK)
			}
		})
		defer th.Close()

		result, err := th.CallTool("upload_object", map[string]any{
			"key":     "test/hello.txt",
			"content": "hello world",
		})
		So(err, ShouldBeNil)
		So(receivedBody, ShouldEqual, "hello world")

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["key"], ShouldEqual, "test/hello.txt")
	})
}

func TestCopyObject(t *testing.T) {
	Convey("copy_object copies between buckets", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<CopyObjectResult>
  <ETag>"copied123"</ETag>
  <LastModified>2024-01-15T10:30:00Z</LastModified>
</CopyObjectResult>`)
		})
		defer th.Close()

		result, err := th.CallTool("copy_object", map[string]any{
			"sourceKey": "src.txt",
			"destKey":   "dst.txt",
		})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["destKey"], ShouldEqual, "dst.txt")
	})
}

func TestMoveObject(t *testing.T) {
	Convey("move_object copies then deletes source", t, func() {
		var methods []string
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			methods = append(methods, r.Method)
			if r.Method == "PUT" {
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprint(w, `<CopyObjectResult><ETag>"m"</ETag><LastModified>2024-01-01T00:00:00Z</LastModified></CopyObjectResult>`)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		})
		defer th.Close()

		result, err := th.CallTool("move_object", map[string]any{
			"sourceKey": "old.txt",
			"destKey":   "new.txt",
		})
		So(err, ShouldBeNil)
		So(result, ShouldNotBeNil)
		So(methods, ShouldContain, "PUT")
		So(methods, ShouldContain, "DELETE")
	})
}

func TestDeleteObject(t *testing.T) {
	Convey("delete_object deletes a single object", t, func() {
		var deletedPath string
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "DELETE" {
				deletedPath = r.URL.Path
				w.WriteHeader(http.StatusNoContent)
			}
		})
		defer th.Close()

		result, err := th.CallTool("delete_object", map[string]any{"key": "trash.txt"})
		So(err, ShouldBeNil)
		So(deletedPath, ShouldContainSubstring, "trash.txt")

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["key"], ShouldEqual, "trash.txt")
	})
}

func TestDeleteObjects(t *testing.T) {
	Convey("delete_objects batch deletes", t, func() {
		th := newTestS3Host(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Deleted><Key>a.txt</Key></Deleted>
  <Deleted><Key>b.txt</Key></Deleted>
</DeleteResult>`)
		})
		defer th.Close()

		result, err := th.CallTool("delete_objects", map[string]any{
			"keys": []string{"a.txt", "b.txt"},
		})
		So(err, ShouldBeNil)

		b, _ := json.Marshal(result)
		var out map[string]any
		json.Unmarshal(b, &out)
		So(out["deletedCount"], ShouldEqual, float64(2))
	})
}
