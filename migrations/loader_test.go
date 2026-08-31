package migrations

import "testing"

func TestEmbeddedMigrationsAreComplete(t *testing.T) {
	t.Parallel()
	migrations, err := Load(Files)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(migrations) != 11 {
		t.Fatalf("migration count = %d, want 11", len(migrations))
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
	if migrations[5].Version != 6 || migrations[5].Name != "create_job_queue" {
		t.Fatalf("job queue migration = %#v", migrations[5])
	}
	if migrations[6].Version != 7 || migrations[6].Name != "create_export_jobs" {
		t.Fatalf("export jobs migration = %#v", migrations[6])
	}
	if migrations[7].Version != 8 || migrations[7].Name != "add_zip_exports" {
		t.Fatalf("ZIP export migration = %#v", migrations[7])
	}
	if migrations[8].Version != 9 || migrations[8].Name != "add_visual_exports" {
		t.Fatalf("visual export migration = %#v", migrations[8])
	}
	if migrations[9].Version != 10 || migrations[9].Name != "add_gedcom_exports" {
		t.Fatalf("GEDCOM export migration = %#v", migrations[9])
	}
	if migrations[10].Version != 11 || migrations[10].Name != "add_gedzip_exports" {
		t.Fatalf("GEDZIP export migration = %#v", migrations[10])
	}
}
