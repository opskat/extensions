package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/opskat/extensions/sdk/go/opskat"
)

const previewMaxSize = 512 * 1024 // 512 KiB preview limit

// handleListBucketsAction returns the list of available buckets and the default bucket from config.
func handleListBucketsAction(ctx *opskat.ActionContext) (any, error) {
	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}
	out, err := client.ListBuckets(s3ctx(), &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}
	buckets := make([]map[string]any, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		entry := map[string]any{"name": aws.ToString(b.Name)}
		if b.CreationDate != nil {
			entry["creationDate"] = b.CreationDate.Format(time.RFC3339)
		}
		buckets = append(buckets, entry)
	}
	return map[string]any{
		"buckets":       buckets,
		"defaultBucket": cfg.Bucket,
	}, nil
}

// handleTestConnection tests connectivity using the config passed as action args.
// The config contains plaintext credentials (not yet saved/encrypted).
func handleTestConnection(ctx *opskat.ActionContext) (any, error) {
	var cfg OSSConfig
	if err := json.Unmarshal(ctx.Args, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	client := newS3Client(cfg)
	_, err := client.ListBuckets(s3ctx(), &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	return map[string]string{"status": "ok"}, nil
}

func handleBrowse(ctx *opskat.ActionContext) (any, error) {
	var args struct {
		Bucket            string `json:"bucket"`
		Prefix            string `json:"prefix"`
		Delimiter         string `json:"delimiter"`
		MaxKeys           int32  `json:"maxKeys"`
		ContinuationToken string `json:"continuationToken"`
	}
	json.Unmarshal(ctx.Args, &args) //nolint:errcheck
	if args.Delimiter == "" {
		args.Delimiter = "/"
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)
	input := &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Delimiter: aws.String(args.Delimiter),
	}
	if args.Prefix != "" {
		input.Prefix = aws.String(args.Prefix)
	}
	if args.MaxKeys > 0 {
		input.MaxKeys = aws.Int32(args.MaxKeys)
	}
	if args.ContinuationToken != "" {
		input.ContinuationToken = aws.String(args.ContinuationToken)
	}

	out, err := client.ListObjectsV2(s3ctx(), input)
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}

	objects := make([]map[string]any, 0, len(out.Contents))
	for _, obj := range out.Contents {
		entry := map[string]any{
			"key":  aws.ToString(obj.Key),
			"size": aws.ToInt64(obj.Size),
		}
		if obj.LastModified != nil {
			entry["lastModified"] = obj.LastModified.Format(time.RFC3339)
		}
		if obj.ETag != nil {
			entry["etag"] = aws.ToString(obj.ETag)
		}
		objects = append(objects, entry)
	}

	prefixes := make([]string, 0, len(out.CommonPrefixes))
	for _, cp := range out.CommonPrefixes {
		prefixes = append(prefixes, aws.ToString(cp.Prefix))
	}

	result := map[string]any{
		"bucket":         bucket,
		"prefix":         args.Prefix,
		"objects":        objects,
		"commonPrefixes": prefixes,
		"isTruncated":    out.IsTruncated != nil && *out.IsTruncated,
	}
	if out.NextContinuationToken != nil {
		result["nextContinuationToken"] = aws.ToString(out.NextContinuationToken)
	}
	return result, nil
}

func handleSearch(ctx *opskat.ActionContext) (any, error) {
	var args struct {
		Bucket  string `json:"bucket"`
		Prefix  string `json:"prefix"`
		MaxKeys int32  `json:"maxKeys"`
	}
	json.Unmarshal(ctx.Args, &args) //nolint:errcheck
	if args.MaxKeys <= 0 {
		args.MaxKeys = 100
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)
	out, err := client.ListObjectsV2(s3ctx(), &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucket),
		Prefix:  aws.String(args.Prefix),
		MaxKeys: aws.Int32(args.MaxKeys),
	})
	if err != nil {
		return nil, fmt.Errorf("search objects: %w", err)
	}

	objects := make([]map[string]any, 0, len(out.Contents))
	for _, obj := range out.Contents {
		entry := map[string]any{
			"key":  aws.ToString(obj.Key),
			"size": aws.ToInt64(obj.Size),
		}
		if obj.LastModified != nil {
			entry["lastModified"] = obj.LastModified.Format(time.RFC3339)
		}
		objects = append(objects, entry)
	}

	return map[string]any{
		"bucket":  bucket,
		"prefix":  args.Prefix,
		"objects": objects,
	}, nil
}

func handlePreview(ctx *opskat.ActionContext) (any, error) {
	var args struct {
		Bucket  string `json:"bucket"`
		Key     string `json:"key"`
		MaxSize int64  `json:"maxSize"`
	}
	json.Unmarshal(ctx.Args, &args) //nolint:errcheck
	if args.MaxSize <= 0 {
		args.MaxSize = previewMaxSize
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)
	rangeHeader := fmt.Sprintf("bytes=0-%d", args.MaxSize-1)
	out, err := client.GetObject(s3ctx(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(args.Key),
		Range:  aws.String(rangeHeader),
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	contentType := aws.ToString(out.ContentType)
	result := map[string]any{
		"key":         args.Key,
		"contentType": contentType,
		"size":        len(data),
		"truncated":   int64(len(data)) >= args.MaxSize,
	}
	if isTextContent(contentType) {
		result["encoding"] = "utf-8"
		result["content"] = string(data)
	} else {
		result["encoding"] = "base64"
		result["content"] = base64.StdEncoding.EncodeToString(data)
	}
	return result, nil
}

func handleUpload(ctx *opskat.ActionContext) (any, error) {
	var args struct {
		Bucket  string `json:"bucket"`
		Key     string `json:"key"`
		Content string `json:"content"` // base64-encoded file content (for DevServer/testing)
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)

	var body io.Reader
	var contentLength int64

	if args.Content != "" {
		data, err := base64.StdEncoding.DecodeString(args.Content)
		if err != nil {
			return nil, fmt.Errorf("decode content: %w", err)
		}
		body = bytes.NewReader(data)
		contentLength = int64(len(data))
	} else {
		filePath, err := opskat.FileDialog("open", opskat.DialogOptions{
			Title: "Select file to upload",
		})
		if err != nil {
			return nil, fmt.Errorf("file dialog: %w", err)
		}
		fh, err := opskat.IOOpen("file", map[string]any{"path": filePath, "mode": "read"})
		if err != nil {
			return nil, fmt.Errorf("open file: %w", err)
		}
		defer fh.Close()
		data, err := io.ReadAll(fh)
		if err != nil {
			return nil, fmt.Errorf("read file: %w", err)
		}
		body = bytes.NewReader(data)
		contentLength = int64(len(data))
	}

	ctx.Events.Send("progress", map[string]any{"loaded": 0, "total": contentLength})

	out, err := client.PutObject(s3ctx(), &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(args.Key),
		Body:          body,
		ContentLength: aws.Int64(contentLength),
	})
	if err != nil {
		return nil, fmt.Errorf("put object: %w", err)
	}

	ctx.Events.Send("progress", map[string]any{"loaded": contentLength, "total": contentLength})

	return map[string]any{
		"key":    args.Key,
		"bucket": bucket,
		"etag":   aws.ToString(out.ETag),
		"size":   contentLength,
	}, nil
}

func handleDownload(ctx *opskat.ActionContext) (any, error) {
	var args struct {
		Bucket   string `json:"bucket"`
		Key      string `json:"key"`
		SavePath string `json:"savePath"` // optional explicit save path
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)
	out, err := client.GetObject(s3ctx(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(args.Key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer out.Body.Close()

	total := aws.ToInt64(out.ContentLength)
	ctx.Events.Send("progress", map[string]any{"loaded": 0, "total": total})

	savePath := args.SavePath
	if savePath == "" {
		fileName := args.Key
		if idx := strings.LastIndex(args.Key, "/"); idx >= 0 {
			fileName = args.Key[idx+1:]
		}
		savePath, err = opskat.FileDialog("save", opskat.DialogOptions{
			Title:       "Save file",
			DefaultName: fileName,
		})
		if err != nil {
			return nil, fmt.Errorf("file dialog: %w", err)
		}
	}

	fh, err := opskat.IOOpen("file", map[string]any{"path": savePath, "mode": "write"})
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	written, err := io.Copy(fh, out.Body)
	fh.Close()
	if err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	ctx.Events.Send("progress", map[string]any{"loaded": written, "total": total})

	return map[string]any{
		"key":     args.Key,
		"savedTo": savePath,
		"size":    written,
	}, nil
}

func handleBatchDelete(ctx *opskat.ActionContext) (any, error) {
	var args struct {
		Bucket string   `json:"bucket"`
		Keys   []string `json:"keys"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)
	total := len(args.Keys)

	objects := make([]types.ObjectIdentifier, total)
	for i, key := range args.Keys {
		objects[i] = types.ObjectIdentifier{Key: aws.String(key)}
	}

	ctx.Events.Send("progress", map[string]any{"done": 0, "total": total})

	out, err := client.DeleteObjects(s3ctx(), &s3.DeleteObjectsInput{
		Bucket: aws.String(bucket),
		Delete: &types.Delete{Objects: objects},
	})
	if err != nil {
		return nil, fmt.Errorf("delete objects: %w", err)
	}

	deleted := make([]string, 0, len(out.Deleted))
	for _, d := range out.Deleted {
		deleted = append(deleted, aws.ToString(d.Key))
	}

	ctx.Events.Send("progress", map[string]any{"done": len(deleted), "total": total})

	if len(out.Errors) > 0 {
		errKeys := make([]string, 0, len(out.Errors))
		for _, e := range out.Errors {
			errKeys = append(errKeys, aws.ToString(e.Key))
		}
		return map[string]any{
			"bucket":       bucket,
			"deleted":      deleted,
			"deletedCount": len(deleted),
			"errors":       errKeys,
			"errorCount":   len(errKeys),
		}, nil
	}

	return map[string]any{
		"bucket":       bucket,
		"deleted":      deleted,
		"deletedCount": len(deleted),
	}, nil
}

func handleBatchCopy(ctx *opskat.ActionContext) (any, error) {
	var args struct {
		Bucket     string   `json:"bucket"`
		Keys       []string `json:"keys"`
		DestBucket string   `json:"destBucket"`
		DestPrefix string   `json:"destPrefix"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	srcBucket := resolveBucket(args.Bucket, cfg)
	dstBucket := resolveBucket(args.DestBucket, cfg)
	total := len(args.Keys)

	ctx.Events.Send("progress", map[string]any{"done": 0, "total": total})

	copied := 0
	for _, key := range args.Keys {
		destKey := args.DestPrefix + key
		if idx := strings.LastIndex(key, "/"); idx >= 0 {
			destKey = args.DestPrefix + key[idx+1:]
		}
		_, err := client.CopyObject(s3ctx(), &s3.CopyObjectInput{
			Bucket:     aws.String(dstBucket),
			Key:        aws.String(destKey),
			CopySource: aws.String(srcBucket + "/" + url.PathEscape(key)),
		})
		if err != nil {
			return nil, fmt.Errorf("copy %s: %w", key, err)
		}
		copied++
		ctx.Events.Send("progress", map[string]any{"done": copied, "total": total})
	}

	return map[string]any{
		"srcBucket":   srcBucket,
		"destBucket":  dstBucket,
		"copiedCount": copied,
	}, nil
}

func handleGetPresignedURL(ctx *opskat.ActionContext) (any, error) {
	var args struct {
		Bucket  string `json:"bucket"`
		Key     string `json:"key"`
		Expires int    `json:"expires"`
	}
	json.Unmarshal(ctx.Args, &args) //nolint:errcheck
	if args.Expires <= 0 {
		args.Expires = 3600
	}

	client, cfg, err := getPresignClient(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)
	presignClient := s3.NewPresignClient(client)
	req, err := presignClient.PresignGetObject(s3ctx(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(args.Key),
	}, s3.WithPresignExpires(time.Duration(args.Expires)*time.Second))
	if err != nil {
		return nil, fmt.Errorf("presign: %w", err)
	}

	return map[string]any{
		"url":       req.URL,
		"expiresIn": args.Expires,
	}, nil
}
