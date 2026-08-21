package visual

import (
	"bytes"
	"encoding/xml"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/google/uuid"
)

func TestBuildSceneUsesGenerationsAndAlignsPartners(t *testing.T) {
	value, IDs := visualFixture()
	prepared, err := buildScene(value, 20, 64*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Nodes) != 5 || len(prepared.ParentEdges) != 4 || len(prepared.UnionEdges) != 1 {
		t.Fatalf("scene counts = nodes %d, parents %d, unions %d", len(prepared.Nodes), len(prepared.ParentEdges), len(prepared.UnionEdges))
	}
	nodes := make(map[uuid.UUID]sceneNode)
	for _, node := range prepared.Nodes {
		nodes[node.ID] = node
	}
	if nodes[IDs.parent].Y != nodes[IDs.partner].Y {
		t.Fatalf("partner generations differ: %.1f and %.1f", nodes[IDs.parent].Y, nodes[IDs.partner].Y)
	}
	if nodes[IDs.child].Y <= nodes[IDs.parent].Y || prepared.Title != "Род Волконских" {
		t.Fatalf("unexpected scene layout: %#v", prepared)
	}
	if _, exists := nodes[IDs.deleted]; exists {
		t.Fatal("soft-deleted person is present in visual scene")
	}
}

func TestRenderProducesSVGPNGAndPDF(t *testing.T) {
	value, _ := visualFixture()
	formats := []struct {
		format string
		mime   string
	}{
		{format: domain.FormatSVG, mime: svgMIMEType},
		{format: domain.FormatPNG, mime: pngMIMEType},
		{format: domain.FormatPDF, mime: pdfMIMEType},
	}
	for _, testCase := range formats {
		t.Run(testCase.format, func(t *testing.T) {
			body, mimeType, err := Render(value, testCase.format, 20, 64*1024*1024)
			if err != nil {
				t.Fatal(err)
			}
			if mimeType != testCase.mime || len(body) < 100 {
				t.Fatalf("rendered %s = mime %q, size %d", testCase.format, mimeType, len(body))
			}
			switch testCase.format {
			case domain.FormatSVG:
				var root struct {
					XMLName xml.Name `xml:"svg"`
				}
				if err := xml.Unmarshal(body, &root); err != nil {
					t.Fatalf("decode SVG: %v", err)
				}
				if !bytes.Contains(body, []byte("Род Волконских")) ||
					bytes.Contains(body, []byte(value.Tree.ID.String())) {
					t.Fatal("SVG title is missing or internal UUID was exposed")
				}
			case domain.FormatPNG:
				configuration, err := png.DecodeConfig(bytes.NewReader(body))
				if err != nil {
					t.Fatalf("decode PNG: %v", err)
				}
				if configuration.Width < int(minimumSceneWidth) || configuration.Height < 400 {
					t.Fatalf("PNG dimensions = %dx%d", configuration.Width, configuration.Height)
				}
			case domain.FormatPDF:
				if !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.Contains(body, []byte("%%EOF")) {
					t.Fatal("PDF structure markers are missing")
				}
			}
			second, _, err := Render(value, testCase.format, 20, 64*1024*1024)
			if err != nil || !bytes.Equal(body, second) {
				t.Fatalf("%s rendering is not deterministic", testCase.format)
			}
			writeVisualFixture(t, testCase.format, body)
		})
	}
}

func TestRenderRejectsOversizedVisualTree(t *testing.T) {
	value, _ := visualFixture()
	if _, _, err := Render(value, domain.FormatSVG, 2, 64*1024*1024); !errors.Is(err, domain.ErrExportVisualTooLarge) {
		t.Fatalf("node-limited Render() error = %v", err)
	}
	if _, _, err := Render(value, domain.FormatPNG, 20, 1024); !errors.Is(err, domain.ErrExportVisualTooLarge) {
		t.Fatalf("pixel-limited Render() error = %v", err)
	}
}

func TestSVGRendererEscapesUserText(t *testing.T) {
	value, _ := visualFixture()
	value.Tree.Name = `</text><script>alert("x")</script>`
	value.PersonNames[0].FullText = `<img src="external">`
	body, _, err := Render(value, domain.FormatSVG, 20, 64*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("<script>")) || bytes.Contains(body, []byte("<img")) {
		t.Fatal("SVG contains unescaped user markup")
	}
	var root struct {
		XMLName xml.Name `xml:"svg"`
	}
	if err := xml.Unmarshal(body, &root); err != nil {
		t.Fatalf("escaped SVG is invalid XML: %v", err)
	}
}

func TestSceneHandlesEmptyTreeAndLongNames(t *testing.T) {
	value, _ := visualFixture()
	value.Persons = nil
	value.PersonNames = nil
	value.ParentChildRelations = nil
	value.Unions = nil
	value.UnionMembers = nil
	prepared, err := buildScene(value, 20, 64*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.EmptyText == "" || len(prepared.Nodes) != 0 {
		t.Fatalf("empty scene = %#v", prepared)
	}
	lines := wrapName("Оченьдлинноефамильноеимябезпробелов")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "...") {
		t.Fatalf("wrapped long name = %#v", lines)
	}
	if cleaned := cleanDisplayText("Князь\x01  Волконский"); cleaned != "Князь Волконский" {
		t.Fatalf("cleanDisplayText() = %q", cleaned)
	}
}

type visualIDs struct {
	parent  uuid.UUID
	partner uuid.UUID
	child   uuid.UUID
	deleted uuid.UUID
}

func visualFixture() (manifest.Manifest, visualIDs) {
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	grandfather := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	grandmother := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	parent := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	partner := uuid.MustParse("00000000-0000-0000-0000-000000000004")
	child := uuid.MustParse("00000000-0000-0000-0000-000000000005")
	deleted := uuid.MustParse("00000000-0000-0000-0000-000000000006")
	owner := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	unionID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	deletedAt := now
	persons := []manifest.Person{
		{ID: grandfather, Sex: "male", LifeStatus: "deceased"},
		{ID: grandmother, Sex: "female", LifeStatus: "deceased"},
		{ID: parent, Sex: "female", LifeStatus: "alive"},
		{ID: partner, Sex: "male", LifeStatus: "alive"},
		{ID: child, Sex: "male", LifeStatus: "alive"},
		{ID: deleted, Sex: "unknown", LifeStatus: "unknown", DeletedAt: &deletedAt},
	}
	names := []string{
		"Иван Сергеевич Волконский",
		"Анна Петровна Волконская",
		"Елизавета Ивановна Волконская",
		"Александр Михайлович Оболенский",
		"Пётр Александрович Оболенский",
		"Удалённая персона",
	}
	personNames := make([]manifest.PersonName, 0, len(persons))
	for index, person := range persons {
		personNames = append(personNames, manifest.PersonName{
			ID:       uuid.MustParse("30000000-0000-0000-0000-00000000000" + string(rune('1'+index))),
			PersonID: person.ID, FullText: names[index], IsPreferred: true,
		})
	}
	return manifest.Manifest{
		Schema: manifest.Schema{Name: domain.ManifestSchemaName, Version: domain.ManifestSchemaVersion},
		Export: manifest.ExportMetadata{
			ID:          uuid.MustParse("40000000-0000-0000-0000-000000000001"),
			RequestedBy: owner, CreatedAt: now,
		},
		Tree: manifest.Tree{
			ID:   uuid.MustParse("50000000-0000-0000-0000-000000000001"),
			Name: "Род Волконских", OwnerUserID: owner,
		},
		Persons:     persons,
		PersonNames: personNames,
		ParentChildRelations: []manifest.ParentChildRelation{
			{ID: uuid.New(), ParentPersonID: grandfather, ChildPersonID: parent},
			{ID: uuid.New(), ParentPersonID: grandmother, ChildPersonID: parent},
			{ID: uuid.New(), ParentPersonID: parent, ChildPersonID: child},
			{ID: uuid.New(), ParentPersonID: partner, ChildPersonID: child},
		},
		Unions: []manifest.FamilyUnion{{ID: unionID}},
		UnionMembers: []manifest.UnionMember{
			{UnionID: unionID, PersonID: parent},
			{UnionID: unionID, PersonID: partner},
		},
	}, visualIDs{parent: parent, partner: partner, child: child, deleted: deleted}
}

func writeVisualFixture(t *testing.T, format string, body []byte) {
	t.Helper()
	directory := strings.TrimSpace(os.Getenv("VISUAL_TEST_OUTPUT_DIR"))
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "family-tree-sample."+format), body, 0o600); err != nil {
		t.Fatal(err)
	}
}
