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

const maxInlineSize = 1 << 20 // 1 MiB — files larger than this use FileDialog

// --- List Tools ---

func handleListBuckets(ctx *opskat.ToolContext) (any, error) {
	client, _, err := getS3Client(assetIDFromArgs(ctx.Args))
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
	return map[string]any{"buckets": buckets}, nil
}

func handleListObjects(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		Bucket            string `json:"bucket"`
		Prefix            string `json:"prefix"`
		Delimiter         string `json:"delimiter"`
		MaxKeys           int32  `json:"maxKeys"`
		ContinuationToken string `json:"continuationToken"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	}
	if args.Prefix != "" {
		input.Prefix = aws.String(args.Prefix)
	}
	if args.Delimiter != "" {
		input.Delimiter = aws.String(args.Delimiter)
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
		"objects":        objects,
		"commonPrefixes": prefixes,
		"isTruncated":    out.IsTruncated != nil && *out.IsTruncated,
	}
	if out.NextContinuationToken != nil {
		result["nextContinuationToken"] = aws.ToString(out.NextContinuationToken)
	}
	return result, nil
}

// --- Read Tools ---

func handleGetObjectInfo(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		Bucket string `json:"bucket"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)
	out, err := client.HeadObject(s3ctx(), &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(args.Key),
	})
	if err != nil {
		return nil, fmt.Errorf("head object: %w", err)
	}

	result := map[string]any{
		"key":         args.Key,
		"bucket":      bucket,
		"size":        aws.ToInt64(out.ContentLength),
		"contentType": aws.ToString(out.ContentType),
	}
	if out.LastModified != nil {
		result["lastModified"] = out.LastModified.Format(time.RFC3339)
	}
	if out.ETag != nil {
		result["etag"] = aws.ToString(out.ETag)
	}
	return result, nil
}

func handleDownloadObject(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		Bucket string `json:"bucket"`
		Key    string `json:"key"`
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

	contentType := aws.ToString(out.ContentType)
	size := aws.ToInt64(out.ContentLength)

	// Large files: save via file dialog
	if size > maxInlineSize {
		fileName := args.Key
		if idx := strings.LastIndex(args.Key, "/"); idx >= 0 {
			fileName = args.Key[idx+1:]
		}
		savePath, err := opskat.FileDialog("save", opskat.DialogOptions{
			Title:       "Save downloaded file",
			DefaultName: fileName,
		})
		if err != nil {
			return nil, fmt.Errorf("file dialog: %w", err)
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
		return map[string]any{
			"key":     args.Key,
			"size":    written,
			"savedTo": savePath,
		}, nil
	}

	// Small files: return content inline
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	result := map[string]any{
		"key":         args.Key,
		"contentType": contentType,
		"size":        len(data),
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

func handlePresignURL(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		Bucket  string `json:"bucket"`
		Key     string `json:"key"`
		Expires int    `json:"expires"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}
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

// isTextContent returns true if the content type is human-readable text.
func isTextContent(ct string) bool {
	return strings.HasPrefix(ct, "text/") ||
		ct == "application/json" ||
		ct == "application/xml" ||
		ct == "application/javascript"
}

// --- Write Tools ---

func handleUploadObject(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		Bucket  string `json:"bucket"`
		Key     string `json:"key"`
		Content string `json:"content"`
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
		data := []byte(args.Content)
		body = strings.NewReader(args.Content)
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

	input := &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(args.Key),
		Body:          body,
		ContentLength: aws.Int64(contentLength),
	}
	out, err := client.PutObject(s3ctx(), input)
	if err != nil {
		return nil, fmt.Errorf("put object: %w", err)
	}

	return map[string]any{
		"key":    args.Key,
		"bucket": bucket,
		"etag":   aws.ToString(out.ETag),
		"size":   contentLength,
	}, nil
}

func handleCopyObject(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		SourceBucket string `json:"sourceBucket"`
		SourceKey    string `json:"sourceKey"`
		DestBucket   string `json:"destBucket"`
		DestKey      string `json:"destKey"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	srcBucket := resolveBucket(args.SourceBucket, cfg)
	dstBucket := resolveBucket(args.DestBucket, cfg)

	_, err = client.CopyObject(s3ctx(), &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(args.DestKey),
		CopySource: aws.String(srcBucket + "/" + url.PathEscape(args.SourceKey)),
	})
	if err != nil {
		return nil, fmt.Errorf("copy object: %w", err)
	}

	return map[string]any{
		"sourceBucket": srcBucket,
		"sourceKey":    args.SourceKey,
		"destBucket":   dstBucket,
		"destKey":      args.DestKey,
	}, nil
}

func handleMoveObject(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		SourceBucket string `json:"sourceBucket"`
		SourceKey    string `json:"sourceKey"`
		DestBucket   string `json:"destBucket"`
		DestKey      string `json:"destKey"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	srcBucket := resolveBucket(args.SourceBucket, cfg)
	dstBucket := resolveBucket(args.DestBucket, cfg)

	// Step 1: Copy
	_, err = client.CopyObject(s3ctx(), &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(args.DestKey),
		CopySource: aws.String(srcBucket + "/" + url.PathEscape(args.SourceKey)),
	})
	if err != nil {
		return nil, fmt.Errorf("copy (move step 1): %w", err)
	}

	// Step 2: Delete source
	_, err = client.DeleteObject(s3ctx(), &s3.DeleteObjectInput{
		Bucket: aws.String(srcBucket),
		Key:    aws.String(args.SourceKey),
	})
	if err != nil {
		return nil, fmt.Errorf("delete source (move step 2): %w", err)
	}

	return map[string]any{
		"sourceBucket": srcBucket,
		"sourceKey":    args.SourceKey,
		"destBucket":   dstBucket,
		"destKey":      args.DestKey,
	}, nil
}

// --- Delete Tools ---

func handleDeleteObject(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		Bucket string `json:"bucket"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	client, cfg, err := getS3Client(assetIDFromArgs(ctx.Args))
	if err != nil {
		return nil, err
	}

	bucket := resolveBucket(args.Bucket, cfg)
	_, err = client.DeleteObject(s3ctx(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(args.Key),
	})
	if err != nil {
		return nil, fmt.Errorf("delete object: %w", err)
	}

	return map[string]any{
		"key":    args.Key,
		"bucket": bucket,
	}, nil
}

func handleDeleteObjects(ctx *opskat.ToolContext) (any, error) {
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

	objects := make([]types.ObjectIdentifier, len(args.Keys))
	for i, key := range args.Keys {
		objects[i] = types.ObjectIdentifier{Key: aws.String(key)}
	}

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
