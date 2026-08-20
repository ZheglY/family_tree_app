package s3storage

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type Adapter struct {
	client         *awss3.Client
	presigner      *awss3.PresignClient
	bucket         string
	uploadTTL      time.Duration
	downloadTTL    time.Duration
	requestTimeout time.Duration
	encryption     types.ServerSideEncryption
	kmsKeyID       string
	now            func() time.Time
}

func New(ctx context.Context, config Config) (*Adapter, error) {
	awsConfiguration, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(config.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			config.AccessKeyID,
			config.SecretAccessKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("load AWS SDK config: %w", err)
	}
	client := awss3.NewFromConfig(awsConfiguration, func(options *awss3.Options) {
		options.BaseEndpoint = aws.String(config.Endpoint)
		options.UsePathStyle = config.UsePathStyle
	})
	adapter := &Adapter{
		client:         client,
		presigner:      awss3.NewPresignClient(client),
		bucket:         config.Bucket,
		uploadTTL:      config.UploadURLTTL,
		downloadTTL:    config.DownloadURLTTL,
		requestTimeout: config.RequestTimeout,
		kmsKeyID:       config.KMSKeyID,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	if adapter.requestTimeout <= 0 {
		adapter.requestTimeout = 5 * time.Second
	}
	switch config.Encryption {
	case EncryptionAES256:
		adapter.encryption = types.ServerSideEncryptionAes256
	case EncryptionKMS:
		adapter.encryption = types.ServerSideEncryptionAwsKms
	}
	return adapter, nil
}

func (a *Adapter) EnsureBucket(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, a.requestTimeout)
	defer cancel()
	_, err := a.client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String(a.bucket)})
	if err == nil {
		return nil
	}
	var apiError smithy.APIError
	if !errors.As(err, &apiError) ||
		(apiError.ErrorCode() != "NotFound" && apiError.ErrorCode() != "NoSuchBucket") {
		return fmt.Errorf("head S3 bucket: %w", err)
	}
	if _, err := a.client.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(a.bucket),
	}); err != nil {
		return fmt.Errorf("create private S3 bucket: %w", err)
	}
	return nil
}

func (a *Adapter) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, a.requestTimeout)
	defer cancel()
	if _, err := a.client.HeadBucket(ctx, &awss3.HeadBucketInput{
		Bucket: aws.String(a.bucket),
	}); err != nil {
		return fmt.Errorf("ping S3 bucket: %w", err)
	}
	return nil
}

func (a *Adapter) PresignUpload(
	ctx context.Context,
	input storage.UploadInput,
) (storage.PresignedRequest, error) {
	parameters := &awss3.PutObjectInput{
		Bucket:        aws.String(a.bucket),
		Key:           aws.String(input.ObjectKey),
		ContentType:   aws.String(input.ContentType),
		ContentLength: aws.Int64(input.SizeBytes),
		Metadata: map[string]string{
			"sha256": input.ChecksumSHA256,
		},
	}
	if a.encryption != "" {
		parameters.ServerSideEncryption = a.encryption
	}
	if a.kmsKeyID != "" {
		parameters.SSEKMSKeyId = aws.String(a.kmsKeyID)
	}
	request, err := a.presigner.PresignPutObject(
		ctx,
		parameters,
		func(options *awss3.PresignOptions) { options.Expires = a.uploadTTL },
	)
	if err != nil {
		return storage.PresignedRequest{}, fmt.Errorf("presign S3 upload: %w", err)
	}
	return storage.PresignedRequest{
		URL:       request.URL,
		Method:    request.Method,
		Headers:   cloneClientHeaders(request.SignedHeader),
		ExpiresAt: a.now().Add(a.uploadTTL),
	}, nil
}

func (a *Adapter) HeadObject(ctx context.Context, objectKey string) (storage.ObjectInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, a.requestTimeout)
	defer cancel()
	result, err := a.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) &&
			(apiError.ErrorCode() == "NotFound" || apiError.ErrorCode() == "NoSuchKey") {
			return storage.ObjectInfo{}, storage.ErrObjectNotFound
		}
		return storage.ObjectInfo{}, fmt.Errorf("head S3 object: %w", err)
	}
	return storage.ObjectInfo{
		ContentType:    aws.ToString(result.ContentType),
		SizeBytes:      aws.ToInt64(result.ContentLength),
		ChecksumSHA256: result.Metadata["sha256"],
		ETag:           strings.Trim(aws.ToString(result.ETag), "\""),
	}, nil
}

func (a *Adapter) PresignDownload(
	ctx context.Context,
	objectKey string,
	filename string,
) (storage.PresignedRequest, error) {
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	request, err := a.presigner.PresignGetObject(
		ctx,
		&awss3.GetObjectInput{
			Bucket:                     aws.String(a.bucket),
			Key:                        aws.String(objectKey),
			ResponseContentDisposition: aws.String(disposition),
		},
		func(options *awss3.PresignOptions) { options.Expires = a.downloadTTL },
	)
	if err != nil {
		return storage.PresignedRequest{}, fmt.Errorf("presign S3 download: %w", err)
	}
	return storage.PresignedRequest{
		URL:       request.URL,
		Method:    request.Method,
		Headers:   cloneClientHeaders(request.SignedHeader),
		ExpiresAt: a.now().Add(a.downloadTTL),
	}, nil
}

func (a *Adapter) DeleteObject(ctx context.Context, objectKey string) error {
	ctx, cancel := context.WithTimeout(ctx, a.requestTimeout)
	defer cancel()
	if _, err := a.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(objectKey),
	}); err != nil {
		return fmt.Errorf("delete S3 object: %w", err)
	}
	return nil
}

func cloneClientHeaders(source http.Header) http.Header {
	destination := make(http.Header)
	for name, values := range source {
		if strings.EqualFold(name, "Host") || strings.EqualFold(name, "Content-Length") {
			continue
		}
		destination[name] = append([]string(nil), values...)
	}
	return destination
}
