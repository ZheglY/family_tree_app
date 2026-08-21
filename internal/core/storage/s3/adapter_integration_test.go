package s3storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/google/uuid"
)

func TestAdapterPresignedUploadHeadAndDownload(t *testing.T) {
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config := Config{
		Endpoint:        endpoint,
		Region:          envOrDefault("S3_TEST_REGION", "ru-central1"),
		Bucket:          envOrDefault("S3_TEST_BUCKET", "family-tree-media-test"),
		AccessKeyID:     envOrDefault("S3_TEST_ACCESS_KEY_ID", "family-tree"),
		SecretAccessKey: envOrDefault("S3_TEST_SECRET_ACCESS_KEY", "family-tree-secret"),
		UsePathStyle:    true,
		UploadURLTTL:    5 * time.Minute,
		DownloadURLTTL:  5 * time.Minute,
		MaxUploadBytes:  1024 * 1024,
	}
	adapter, err := New(ctx, config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := adapter.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket() error = %v", err)
	}
	payload := []byte("private family archive")
	digest := sha256.Sum256(payload)
	checksum := fmt.Sprintf("%x", digest)
	objectKey := "integration/" + uuid.NewString()
	defer func() {
		if err := adapter.DeleteObject(context.Background(), objectKey); err != nil {
			t.Errorf("DeleteObject() error = %v", err)
		}
	}()
	upload, err := adapter.PresignUpload(ctx, storage.UploadInput{
		ObjectKey:      objectKey,
		ContentType:    "application/pdf",
		SizeBytes:      int64(len(payload)),
		ChecksumSHA256: checksum,
	})
	if err != nil {
		t.Fatalf("PresignUpload() error = %v", err)
	}
	request, err := http.NewRequestWithContext(ctx, upload.Method, upload.URL, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header = upload.Headers.Clone()
	request.ContentLength = int64(len(payload))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("presigned upload request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("presigned upload status = %d", response.StatusCode)
	}
	info, err := adapter.HeadObject(ctx, objectKey)
	if err != nil {
		t.Fatalf("HeadObject() error = %v", err)
	}
	if info.SizeBytes != int64(len(payload)) || info.ContentType != "application/pdf" ||
		info.ChecksumSHA256 != checksum {
		t.Fatalf("object info = %#v", info)
	}
	download, err := adapter.PresignDownload(ctx, objectKey, "archive.pdf")
	if err != nil {
		t.Fatalf("PresignDownload() error = %v", err)
	}
	downloadRequest, err := http.NewRequestWithContext(ctx, download.Method, download.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	downloadRequest.Header = download.Headers.Clone()
	downloadResponse, err := http.DefaultClient.Do(downloadRequest)
	if err != nil {
		t.Fatalf("presigned download request: %v", err)
	}
	defer downloadResponse.Body.Close()
	downloaded, err := io.ReadAll(downloadResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if downloadResponse.StatusCode != http.StatusOK || !bytes.Equal(downloaded, payload) {
		t.Fatalf("download status = %d, payload = %q", downloadResponse.StatusCode, downloaded)
	}
	variantKey := objectKey + "/variant"
	defer func() {
		if err := adapter.DeleteObject(context.Background(), variantKey); err != nil {
			t.Errorf("DeleteObject(variant) error = %v", err)
		}
	}()
	variantBody := []byte("generated thumbnail")
	variantDigest := sha256.Sum256(variantBody)
	variantChecksum := fmt.Sprintf("%x", variantDigest)
	putInfo, err := adapter.PutObject(ctx, storage.PutInput{
		ObjectKey:      variantKey,
		ContentType:    "image/jpeg",
		ChecksumSHA256: variantChecksum,
		Body:           variantBody,
	})
	if err != nil {
		t.Fatalf("PutObject() error = %v", err)
	}
	if putInfo.SizeBytes != int64(len(variantBody)) || putInfo.ChecksumSHA256 != variantChecksum {
		t.Fatalf("put object info = %#v", putInfo)
	}
	if _, err := adapter.PutObjectIfAbsent(ctx, storage.PutInput{
		ObjectKey: variantKey, ContentType: "image/jpeg",
		ChecksumSHA256: variantChecksum, Body: []byte("must not overwrite"),
	}); !errors.Is(err, storage.ErrObjectAlreadyExists) {
		t.Fatalf("PutObjectIfAbsent(existing) error = %v", err)
	}
	directBody, directInfo, err := adapter.DownloadObject(ctx, variantKey, int64(len(variantBody)))
	if err != nil {
		t.Fatalf("DownloadObject() error = %v", err)
	}
	if !bytes.Equal(directBody, variantBody) || directInfo.ChecksumSHA256 != variantChecksum {
		t.Fatalf("direct download info = %#v, body = %q", directInfo, directBody)
	}
	view, err := adapter.PresignView(ctx, variantKey)
	if err != nil {
		t.Fatalf("PresignView() error = %v", err)
	}
	viewRequest, err := http.NewRequestWithContext(ctx, view.Method, view.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	viewRequest.Header = view.Headers.Clone()
	viewResponse, err := http.DefaultClient.Do(viewRequest)
	if err != nil {
		t.Fatalf("inline view request: %v", err)
	}
	viewResponse.Body.Close()
	if viewResponse.StatusCode != http.StatusOK || viewResponse.Header.Get("Content-Disposition") != "inline" {
		t.Fatalf("inline view status = %d, disposition = %q", viewResponse.StatusCode, viewResponse.Header.Get("Content-Disposition"))
	}
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
