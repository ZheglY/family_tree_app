package gedcom

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/google/uuid"
)

var gedcomLinePattern = regexp.MustCompile(`^\d+(?: @[A-Z0-9_]+@)? [A-Z][A-Z0-9_]*(?: .*)?$`)

func TestRenderProducesDeterministicGEDCOM7Graph(t *testing.T) {
	t.Parallel()
	fatherID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	motherID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	childID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	guardianID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	deletedID := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	unionID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	deletedAt := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	value := manifest.Manifest{
		Export: manifest.ExportMetadata{CreatedAt: time.Date(2026, time.August, 22, 14, 5, 6, 0, time.UTC)},
		Tree:   manifest.Tree{Name: "Род Волконских", Locale: "ru-RU"},
		Persons: []manifest.Person{
			{ID: guardianID, Sex: "female", LifeStatus: "alive", PrivacyLevel: "tree_members"},
			{ID: childID, Sex: "male", LifeStatus: "alive", Biography: "Первая строка\n@вторая", Notes: "архив\u0001", PrivacyLevel: "tree_members"},
			{ID: deletedID, Sex: "unknown", DeletedAt: &deletedAt},
			{ID: motherID, Sex: "female", LifeStatus: "deceased", PrivacyLevel: "tree_members"},
			{ID: fatherID, Sex: "male", LifeStatus: "deceased", PrivacyLevel: "tree_members"},
		},
		PersonNames: []manifest.PersonName{
			{ID: uuid.MustParse("10000000-0000-0000-0000-000000000001"), PersonID: childID, Type: "alias", FullText: "Петя", IsPreferred: false},
			{ID: uuid.MustParse("10000000-0000-0000-0000-000000000002"), PersonID: childID, Type: "primary", GivenName: "Пётр", Patronymic: "Иванович", FamilyName: "Волконский", FullText: "Пётр Иванович Волконский", IsPreferred: true},
			{ID: uuid.MustParse("10000000-0000-0000-0000-000000000003"), PersonID: fatherID, Type: "primary", GivenName: "Иван", FamilyName: "Волконский", IsPreferred: true},
			{ID: uuid.MustParse("10000000-0000-0000-0000-000000000004"), PersonID: motherID, Type: "married", GivenName: "Анна", FamilyName: "Волконская", IsPreferred: true},
			{ID: uuid.MustParse("10000000-0000-0000-0000-000000000005"), PersonID: guardianID, Type: "primary", GivenName: "Мария", FamilyName: "Орлова", IsPreferred: true},
			{ID: uuid.MustParse("10000000-0000-0000-0000-000000000006"), PersonID: deletedID, Type: "primary", FullText: "Удалённая персона", IsPreferred: true},
		},
		Unions: []manifest.FamilyUnion{{
			ID: unionID, Type: "marriage", Note: "Венчание", EndReason: "Смерть супруга",
		}},
		UnionMembers: []manifest.UnionMember{
			{UnionID: unionID, PersonID: motherID, Role: "mother"},
			{UnionID: unionID, PersonID: fatherID, Role: "father"},
		},
		ParentChildRelations: []manifest.ParentChildRelation{
			{ID: uuid.MustParse("20000000-0000-0000-0000-000000000001"), ParentPersonID: fatherID, ChildPersonID: childID, RelationType: "biological", Confidence: "confirmed"},
			{ID: uuid.MustParse("20000000-0000-0000-0000-000000000002"), ParentPersonID: motherID, ChildPersonID: childID, RelationType: "biological", Confidence: "confirmed", Note: "метрическая запись"},
			{ID: uuid.MustParse("20000000-0000-0000-0000-000000000003"), ParentPersonID: guardianID, ChildPersonID: childID, RelationType: "guardian", Confidence: "probable"},
			{ID: uuid.MustParse("20000000-0000-0000-0000-000000000004"), ParentPersonID: deletedID, ChildPersonID: childID, RelationType: "foster", Confidence: "confirmed"},
		},
	}

	first, err := Render(value)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("GEDCOM output is not deterministic")
	}
	assertValidDatasetShape(t, first)
	text := string(first)
	for _, expected := range []string{
		"\ufeff0 HEAD\r\n1 GEDC\r\n2 VERS 7.0\r\n",
		"1 DATE 22 AUG 2026\r\n2 TIME 14:05:06Z\r\n",
		"1 NAME Пётр Иванович /Волконский/\r\n2 GIVN Пётр Иванович\r\n2 SURN Волконский\r\n",
		"1 NAME Петя\r\n2 TYPE AKA\r\n",
		"1 NOTE Biography:\r\n2 CONT Первая строка\r\n2 CONT @@вторая\r\n",
		"1 DEAT Y\r\n",
		"1 FAMC @F" + strings.ToUpper(strings.ReplaceAll(unionID.String(), "-", "")) + "@\r\n2 PEDI BIRTH\r\n2 STAT PROVEN\r\n",
		"2 PEDI OTHER\r\n3 PHRASE Guardian\r\n2 NOTE FamilyTree confidence: probable\r\n",
		"1 MARR Y\r\n1 NOTE Венчание\r\n1 NOTE Union end reason:\r\n2 CONT Смерть супруга\r\n",
		"2 PHRASE father\r\n",
		"2 PHRASE mother\r\n",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing GEDCOM fragment %q\n%s", expected, text)
		}
	}
	if strings.Contains(text, deletedID.String()) || strings.Contains(text, "Удалённая персона") ||
		strings.ContainsRune(text, '\u0001') {
		t.Fatalf("deleted or prohibited data leaked into GEDCOM:\n%s", text)
	}
	assertReciprocalFamilyPointers(t, first)
}

func TestRenderWithMediaCreatesGEDZIPMultimediaRecords(t *testing.T) {
	t.Parallel()
	personID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	mediaID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	deletedMediaID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	deletedAt := time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC)
	value := manifest.Manifest{
		Persons: []manifest.Person{{ID: personID, Sex: "unknown", PrimaryMediaID: &mediaID}},
		MediaAssets: []manifest.MediaAsset{
			{ID: mediaID, Kind: "photo", OriginalFilename: "portrait.jpg", MIMEType: "image/jpeg", Caption: "Портрет", Description: "Семейный архив"},
			{ID: deletedMediaID, Kind: "document", OriginalFilename: "deleted.pdf", MIMEType: "application/pdf", DeletedAt: &deletedAt},
		},
		PersonMedia: []manifest.PersonMediaAttachment{{
			PersonID: personID, MediaID: mediaID, Role: "profile", SortOrder: 1,
		}},
	}
	body, err := RenderWithMedia(value, []MediaFile{
		{MediaID: mediaID, Path: "media/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/original.jpg", MIMEType: "image/jpeg"},
		{MediaID: mediaID, VariantKind: "preview", Path: "media/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/variants/preview.jpg", MIMEType: "image/jpeg"},
		{MediaID: deletedMediaID, Path: "media/deleted/original.pdf", MIMEType: "application/pdf"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	mediaXRef := "O" + strings.ToUpper(strings.ReplaceAll(mediaID.String(), "-", ""))
	for _, expected := range []string{
		"1 OBJE @" + mediaXRef + "@\r\n2 TITL Портрет\r\n",
		"0 @" + mediaXRef + "@ OBJE\r\n1 RESN CONFIDENTIAL\r\n",
		"1 FILE media/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/original.jpg\r\n2 FORM image/jpeg\r\n3 MEDI PHOTO\r\n",
		"2 TITL Портрет\r\n2 TRAN media/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/variants/preview.jpg\r\n3 FORM image/jpeg\r\n",
		"1 NOTE Семейный архив\r\n",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing multimedia fragment %q\n%s", expected, text)
		}
	}
	if strings.Count(text, "1 OBJE @"+mediaXRef+"@") != 1 || strings.Contains(text, "media/deleted") {
		t.Fatalf("unexpected multimedia links:\n%s", text)
	}
	assertValidDatasetShape(t, body)
}

func TestRenderSplitsUnionWithMoreThanTwoMembers(t *testing.T) {
	t.Parallel()
	personIDs := []uuid.UUID{
		uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		uuid.MustParse("33333333-3333-3333-3333-333333333333"),
	}
	unionID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	value := manifest.Manifest{Unions: []manifest.FamilyUnion{{ID: unionID, Type: "partnership"}}}
	for _, personID := range personIDs {
		value.Persons = append(value.Persons, manifest.Person{ID: personID, Sex: "unknown"})
		value.UnionMembers = append(value.UnionMembers, manifest.UnionMember{UnionID: unionID, PersonID: personID})
	}
	body, err := Render(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	baseXRef := "F" + strings.ToUpper(strings.ReplaceAll(unionID.String(), "-", ""))
	if strings.Count(text, " FAM\r\n") != 2 || !strings.Contains(text, "0 @"+baseXRef+"@ FAM") ||
		!strings.Contains(text, "0 @"+baseXRef+"_2@ FAM") {
		t.Fatalf("multi-partner union was not split deterministically:\n%s", text)
	}
	assertReciprocalFamilyPointers(t, body)
}

func assertValidDatasetShape(t *testing.T, body []byte) {
	t.Helper()
	if !bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("GEDCOM 7 UTF-8 BOM is missing")
	}
	withoutBOM := body[3:]
	if bytes.Contains(bytes.ReplaceAll(withoutBOM, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("GEDCOM contains inconsistent line endings")
	}
	lines := strings.Split(strings.TrimSuffix(string(withoutBOM), "\r\n"), "\r\n")
	if len(lines) < 2 || lines[0] != "0 HEAD" || lines[len(lines)-1] != "0 TRLR" {
		t.Fatalf("invalid HEAD/TRLR envelope: %#v", lines)
	}
	for _, line := range lines {
		if !gedcomLinePattern.MatchString(line) {
			t.Fatalf("invalid GEDCOM line %q", line)
		}
	}
}

func assertReciprocalFamilyPointers(t *testing.T, body []byte) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(string(body), "\ufeff"), "\r\n"), "\r\n")
	recordType := ""
	recordXRef := ""
	individualFamilies := make(map[string]map[string]string)
	familyPeople := make(map[string]map[string]string)
	for _, line := range lines {
		parts := strings.Split(line, " ")
		if parts[0] == "0" {
			recordType, recordXRef = "", ""
			if len(parts) >= 3 && strings.HasPrefix(parts[1], "@") {
				recordXRef, recordType = parts[1], parts[2]
			}
			continue
		}
		if len(parts) != 3 || parts[0] != "1" || !strings.HasPrefix(parts[2], "@") {
			continue
		}
		if recordType == "INDI" && (parts[1] == "FAMS" || parts[1] == "FAMC") {
			if individualFamilies[recordXRef] == nil {
				individualFamilies[recordXRef] = make(map[string]string)
			}
			individualFamilies[recordXRef][parts[2]] = parts[1]
		}
		if recordType == "FAM" && (parts[1] == "HUSB" || parts[1] == "WIFE" || parts[1] == "CHIL") {
			if familyPeople[recordXRef] == nil {
				familyPeople[recordXRef] = make(map[string]string)
			}
			familyPeople[recordXRef][parts[2]] = parts[1]
		}
	}
	for familyXRef, people := range familyPeople {
		for personXRef, familyRole := range people {
			expected := "FAMS"
			if familyRole == "CHIL" {
				expected = "FAMC"
			}
			if individualFamilies[personXRef][familyXRef] != expected {
				t.Fatalf("missing reciprocal %s from %s to %s", expected, personXRef, familyXRef)
			}
		}
	}
}
