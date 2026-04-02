package main

import "encoding/json"

// checkPolicy maps a tool invocation to (action, resource) for the policy engine.
func checkPolicy(tool string, args json.RawMessage) (string, string) {
	var p struct {
		Bucket     string `json:"bucket"`
		Key        string `json:"key"`
		DestBucket string `json:"destBucket"`
		DestKey    string `json:"destKey"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck // best-effort parse

	switch tool {
	case "list_buckets":
		return "list", "*"

	case "list_objects":
		bucket := p.Bucket
		if bucket == "" {
			bucket = "*"
		}
		return "list", bucket + "/*"

	case "get_object_info", "download_object", "presign_url":
		return "read", objectResource(p.Bucket, p.Key)

	case "upload_object":
		return "write", objectResource(p.Bucket, p.Key)

	case "copy_object", "move_object":
		return "write", objectResource(p.DestBucket, p.DestKey)

	case "delete_object":
		return "delete", objectResource(p.Bucket, p.Key)

	case "delete_objects":
		bucket := p.Bucket
		if bucket == "" {
			bucket = "*"
		}
		return "delete", bucket + "/*"

	default:
		return "admin", "*"
	}
}

func objectResource(bucket, key string) string {
	if bucket == "" {
		bucket = "*"
	}
	if key == "" {
		key = "*"
	}
	return bucket + "/" + key
}
