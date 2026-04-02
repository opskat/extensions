package main

import (
	"github.com/opskat/extensions/sdk/go/opskat"
)

func init() {
	// AI tools
	opskat.RegisterTool("list_buckets", handleListBuckets)
	opskat.RegisterTool("list_objects", handleListObjects)
	opskat.RegisterTool("get_object_info", handleGetObjectInfo)
	opskat.RegisterTool("download_object", handleDownloadObject)
	opskat.RegisterTool("upload_object", handleUploadObject)
	opskat.RegisterTool("copy_object", handleCopyObject)
	opskat.RegisterTool("move_object", handleMoveObject)
	opskat.RegisterTool("delete_object", handleDeleteObject)
	opskat.RegisterTool("delete_objects", handleDeleteObjects)
	opskat.RegisterTool("presign_url", handlePresignURL)

	// Frontend actions
	opskat.RegisterAction("test_connection", handleTestConnection)
	opskat.RegisterAction("list_buckets", handleListBucketsAction)
	opskat.RegisterAction("browse", handleBrowse)
	opskat.RegisterAction("upload", handleUpload)
	opskat.RegisterAction("download", handleDownload)
	opskat.RegisterAction("batch_delete", handleBatchDelete)
	opskat.RegisterAction("batch_copy", handleBatchCopy)
	opskat.RegisterAction("get_presigned_url", handleGetPresignedURL)
	opskat.RegisterAction("search", handleSearch)
	opskat.RegisterAction("preview", handlePreview)

	// Policy
	opskat.RegisterPolicy(checkPolicy)
}

func main() {
	opskat.Run()
}

