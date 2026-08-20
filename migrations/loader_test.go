package migrations

import "testing"

func TestEmbeddedMigrationsAreComplete(t *testing.T) {
	t.Parallel()
	migrations, err := Load(Files)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(migrations) != 1 {
		t.Fatalf("migration count = %d, want 1", len(migrations))
	}
	migration := migrations[0]
	if migration.Version != 1 || migration.Name != "create_tree_schema" ||
		migration.UpSQL == "" || migration.DownSQL == "" || migration.Checksum == "" {
		t.Fatalf("migration = %#v", migration)
	}
}
