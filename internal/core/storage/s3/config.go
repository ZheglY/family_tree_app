package s3storage

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

const (
	EncryptionNone   = ""
	EncryptionAES256 = "AES256"
	EncryptionKMS    = "aws:kms"
)

type Config struct {
	Endpoint        string        `envconfig:"ENDPOINT" default:"http://localhost:9000"`
	Region          string        `envconfig:"REGION" default:"ru-central1"`
	Bucket          string        `envconfig:"BUCKET" default:"family-tree-media"`
	AccessKeyID     string        `envconfig:"ACCESS_KEY_ID" default:"family-tree"`
	SecretAccessKey string        `envconfig:"SECRET_ACCESS_KEY" default:"family-tree-secret"`
	UsePathStyle    bool          `envconfig:"USE_PATH_STYLE" default:"true"`
	UploadURLTTL    time.Duration `envconfig:"UPLOAD_URL_TTL" default:"15m"`
	DownloadURLTTL  time.Duration `envconfig:"DOWNLOAD_URL_TTL" default:"5m"`
	RequestTimeout  time.Duration `envconfig:"REQUEST_TIMEOUT" default:"5s"`
	MaxUploadBytes  int64         `envconfig:"MAX_UPLOAD_BYTES" default:"26214400"`
	Encryption      string        `envconfig:"ENCRYPTION" default:""`
	KMSKeyID        string        `envconfig:"KMS_KEY_ID" default:""`
}

func LoadConfig() (Config, error) {
	var config Config
	if err := envconfig.Process("S3", &config); err != nil {
		return Config{}, fmt.Errorf("process S3 config: %w", err)
	}
	config.Endpoint = strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	config.Region = strings.TrimSpace(config.Region)
	config.Bucket = strings.TrimSpace(config.Bucket)
	config.AccessKeyID = strings.TrimSpace(config.AccessKeyID)
	config.SecretAccessKey = strings.TrimSpace(config.SecretAccessKey)
	config.Encryption = strings.TrimSpace(config.Encryption)
	config.KMSKeyID = strings.TrimSpace(config.KMSKeyID)
	parsedEndpoint, err := url.Parse(config.Endpoint)
	if err != nil || parsedEndpoint.Host == "" ||
		(parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https") {
		return Config{}, fmt.Errorf("S3_ENDPOINT must be an absolute HTTP(S) URL")
	}
	if config.Region == "" || config.Bucket == "" || config.AccessKeyID == "" ||
		config.SecretAccessKey == "" {
		return Config{}, fmt.Errorf("S3 region, bucket and credentials are required")
	}
	if config.UploadURLTTL <= 0 || config.UploadURLTTL > 24*time.Hour ||
		config.DownloadURLTTL <= 0 || config.DownloadURLTTL > 24*time.Hour {
		return Config{}, fmt.Errorf("S3 presigned URL TTL must be between zero and 24 hours")
	}
	if config.RequestTimeout <= 0 || config.RequestTimeout > time.Minute {
		return Config{}, fmt.Errorf("S3_REQUEST_TIMEOUT must be between zero and one minute")
	}
	if config.MaxUploadBytes <= 0 || config.MaxUploadBytes > 5*1024*1024*1024 {
		return Config{}, fmt.Errorf("S3_MAX_UPLOAD_BYTES must be between 1 and 5 GiB")
	}
	switch config.Encryption {
	case EncryptionNone, EncryptionAES256:
		if config.KMSKeyID != "" {
			return Config{}, fmt.Errorf("S3_KMS_KEY_ID requires aws:kms encryption")
		}
	case EncryptionKMS:
		if config.KMSKeyID == "" {
			return Config{}, fmt.Errorf("S3_KMS_KEY_ID is required for aws:kms encryption")
		}
	default:
		return Config{}, fmt.Errorf("S3_ENCRYPTION must be empty, AES256 or aws:kms")
	}
	return config, nil
}
