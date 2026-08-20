package migrations

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

var migrationFilenamePattern = regexp.MustCompile(
	`^(\d+)_([a-z0-9_]+)\.(up|down)\.sql$`,
)

type Migration struct {
	Version  int64
	Name     string
	UpSQL    string
	DownSQL  string
	Checksum string
}

type migrationParts struct {
	version int64
	name    string
	upSQL   string
	downSQL string
}

func Load(files fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}

	partsByVersion := make(map[int64]*migrationParts)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationFilenamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			if filepath.Ext(entry.Name()) == ".sql" {
				return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
			}
			continue
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		body, err := fs.ReadFile(files, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		parts, exists := partsByVersion[version]
		if !exists {
			parts = &migrationParts{version: version, name: matches[2]}
			partsByVersion[version] = parts
		}
		if parts.name != matches[2] {
			return nil, fmt.Errorf("migration version %d has inconsistent names", version)
		}
		switch matches[3] {
		case "up":
			if parts.upSQL != "" {
				return nil, fmt.Errorf("migration version %d has duplicate up file", version)
			}
			parts.upSQL = string(body)
		case "down":
			if parts.downSQL != "" {
				return nil, fmt.Errorf("migration version %d has duplicate down file", version)
			}
			parts.downSQL = string(body)
		}
	}

	migrations := make([]Migration, 0, len(partsByVersion))
	for _, parts := range partsByVersion {
		if parts.upSQL == "" || parts.downSQL == "" {
			return nil, fmt.Errorf(
				"migration version %d must have both up and down files",
				parts.version,
			)
		}
		checksum := sha256.Sum256([]byte(parts.upSQL + "\x00" + parts.downSQL))
		migrations = append(migrations, Migration{
			Version:  parts.version,
			Name:     parts.name,
			UpSQL:    parts.upSQL,
			DownSQL:  parts.downSQL,
			Checksum: fmt.Sprintf("%x", checksum),
		})
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations, nil
}
