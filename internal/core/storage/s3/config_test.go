package s3storage

import "testing"

func TestLoadConfigUsesSafeDevelopmentDefaults(t *testing.T) {
	for _, name := range []string{
		"S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY_ID",
		"S3_SECRET_ACCESS_KEY", "S3_ENCRYPTION", "S3_KMS_KEY_ID",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("S3_ENDPOINT", "http://localhost:9000")
	t.Setenv("S3_REGION", "ru-central1")
	t.Setenv("S3_BUCKET", "family-tree-media")
	t.Setenv("S3_ACCESS_KEY_ID", "family-tree")
	t.Setenv("S3_SECRET_ACCESS_KEY", "family-tree-secret")
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.MaxUploadBytes != 25*1024*1024 || config.UploadURLTTL <= 0 {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadConfigRejectsKMSWithoutKey(t *testing.T) {
	t.Setenv("S3_ENCRYPTION", EncryptionKMS)
	t.Setenv("S3_KMS_KEY_ID", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() succeeded without a KMS key")
	}
}
