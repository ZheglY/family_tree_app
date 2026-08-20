package migrations

import "testing"

func TestEmbeddedMigrationsAreComplete(t *testing.T) {
	t.Parallel()
	migrations, err := Load(Files)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(migrations) != 5 {
		t.Fatalf("migration count = %d, want 5", len(migrations))
	}
	migration := migrations[0]
	if migration.Version != 1 || migration.Name != "create_tree_schema" ||
		migration.UpSQL == "" || migration.DownSQL == "" || migration.Checksum == "" {
		t.Fatalf("migration = %#v", migration)
	}
	if migrations[1].Version != 2 || migrations[1].Name != "create_person_schema" {
		t.Fatalf("person migration = %#v", migrations[1])
	}
	if migrations[2].Version != 3 || migrations[2].Name != "create_parent_child_relations" {
		t.Fatalf("relation migration = %#v", migrations[2])
	}
	if migrations[3].Version != 4 || migrations[3].Name != "create_family_unions" {
		t.Fatalf("union migration = %#v", migrations[3])
	}
	if migrations[4].Version != 5 || migrations[4].Name != "create_media_schema" {
		t.Fatalf("media migration = %#v", migrations[4])
	}
}
