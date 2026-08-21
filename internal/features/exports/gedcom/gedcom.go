package gedcom

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/google/uuid"
)

const MIMEType = "text/vnd.familysearch.gedcom"

type family struct {
	XRef         string
	Partners     []uuid.UUID
	PartnerRoles map[uuid.UUID]string
	Children     []uuid.UUID
	Union        *manifest.FamilyUnion
	ChildLinks   map[uuid.UUID]familyChildLink
}

type familyChildLink struct {
	RelationType string
	Confidence   string
	Notes        []string
}

type personLinks struct {
	FamiliesAsPartner map[string]struct{}
	FamiliesAsChild   map[string]familyChildLink
}

type relationGroup struct {
	ChildID    uuid.UUID
	Type       string
	ParentIDs  []uuid.UUID
	Confidence string
	Notes      []string
}

type writer struct {
	buffer bytes.Buffer
}

// Render converts the active graph snapshot into a deterministic FamilySearch GEDCOM 7.0 dataset.
func Render(value manifest.Manifest) ([]byte, error) {
	persons := activePersons(value.Persons)
	families, links := buildUnionFamilies(value, persons)
	families, links = addParentChildFamilies(value, persons, families, links)

	result := &writer{}
	result.buffer.WriteString("\xEF\xBB\xBF")
	result.line(0, "", "HEAD", "")
	result.line(1, "", "GEDC", "")
	result.line(2, "", "VERS", "7.0")
	result.line(1, "", "SOUR", "FamilyTree")
	result.line(2, "", "NAME", "Family Tree")
	result.line(2, "", "VERS", "1.0")
	if !value.Export.CreatedAt.IsZero() {
		createdAt := value.Export.CreatedAt.UTC()
		result.line(1, "", "DATE", gedcomDate(createdAt.Day(), int(createdAt.Month()), createdAt.Year()))
		result.line(2, "", "TIME", createdAt.Format("15:04:05"))
	}
	if locale := cleanInline(value.Tree.Locale); locale != "" {
		result.line(1, "", "LANG", locale)
	}
	if treeName := cleanText(value.Tree.Name); treeName != "" {
		result.line(1, "", "NOTE", "Family tree: "+treeName)
	}

	personIDs := sortedPersonIDs(persons)
	names := namesByPerson(value.PersonNames, persons)
	for _, personID := range personIDs {
		writeIndividual(result, persons[personID], names[personID], links[personID])
	}
	sort.Slice(families, func(left int, right int) bool { return families[left].XRef < families[right].XRef })
	for _, item := range families {
		writeFamily(result, item, persons)
	}
	result.line(0, "", "TRLR", "")
	return result.buffer.Bytes(), nil
}

func activePersons(values []manifest.Person) map[uuid.UUID]manifest.Person {
	result := make(map[uuid.UUID]manifest.Person)
	for _, person := range values {
		if person.ID != uuid.Nil && person.DeletedAt == nil {
			result[person.ID] = person
		}
	}
	return result
}

func buildUnionFamilies(
	value manifest.Manifest,
	persons map[uuid.UUID]manifest.Person,
) ([]family, map[uuid.UUID]*personLinks) {
	links := make(map[uuid.UUID]*personLinks, len(persons))
	for personID := range persons {
		links[personID] = newPersonLinks()
	}
	activeUnions := make(map[uuid.UUID]manifest.FamilyUnion)
	for _, union := range value.Unions {
		if union.ID != uuid.Nil && union.DeletedAt == nil {
			activeUnions[union.ID] = union
		}
	}
	members := make(map[uuid.UUID]map[uuid.UUID]string)
	for _, member := range value.UnionMembers {
		if _, exists := activeUnions[member.UnionID]; !exists {
			continue
		}
		if _, exists := persons[member.PersonID]; !exists {
			continue
		}
		if members[member.UnionID] == nil {
			members[member.UnionID] = make(map[uuid.UUID]string)
		}
		members[member.UnionID][member.PersonID] = cleanInline(member.Role)
	}
	unionIDs := make([]uuid.UUID, 0, len(activeUnions))
	for unionID := range activeUnions {
		unionIDs = append(unionIDs, unionID)
	}
	sortUUIDs(unionIDs)
	result := make([]family, 0, len(unionIDs))
	for _, unionID := range unionIDs {
		personIDs := roleMapKeys(members[unionID])
		if len(personIDs) == 0 {
			continue
		}
		pairs := partnerPairs(personIDs)
		for index, pair := range pairs {
			union := activeUnions[unionID]
			xref := "F" + compactUUID(unionID)
			if index > 0 {
				xref += fmt.Sprintf("_%d", index+1)
			}
			item := family{
				XRef: xref, Partners: orderedPartners(pair, persons), Union: &union,
				PartnerRoles: make(map[uuid.UUID]string), ChildLinks: make(map[uuid.UUID]familyChildLink),
			}
			for _, personID := range pair {
				item.PartnerRoles[personID] = members[unionID][personID]
			}
			result = append(result, item)
			for _, personID := range pair {
				links[personID].FamiliesAsPartner[xref] = struct{}{}
			}
		}
	}
	return result, links
}

func addParentChildFamilies(
	value manifest.Manifest,
	persons map[uuid.UUID]manifest.Person,
	families []family,
	links map[uuid.UUID]*personLinks,
) ([]family, map[uuid.UUID]*personLinks) {
	groups := relationGroups(value.ParentChildRelations, persons)
	for _, group := range groups {
		for chunkIndex, parentIDs := range parentChunks(group.ParentIDs) {
			familyIndex := matchingFamily(families, parentIDs, group.ChildID)
			if familyIndex < 0 {
				xref := relationFamilyXRef(group.ChildID, group.Type, chunkIndex, parentIDs)
				families = append(families, family{
					XRef: xref, Partners: orderedPartners(parentIDs, persons),
					ChildLinks: make(map[uuid.UUID]familyChildLink),
				})
				familyIndex = len(families) - 1
				for _, parentID := range parentIDs {
					links[parentID].FamiliesAsPartner[xref] = struct{}{}
				}
			}
			item := &families[familyIndex]
			childLink := familyChildLink{
				RelationType: group.Type, Confidence: group.Confidence,
				Notes: append([]string(nil), group.Notes...),
			}
			item.Children = append(item.Children, group.ChildID)
			item.ChildLinks[group.ChildID] = childLink
			links[group.ChildID].FamiliesAsChild[item.XRef] = childLink
		}
	}
	return families, links
}

func relationGroups(
	values []manifest.ParentChildRelation,
	persons map[uuid.UUID]manifest.Person,
) []relationGroup {
	type key struct {
		childID  uuid.UUID
		typeName string
	}
	grouped := make(map[key][]manifest.ParentChildRelation)
	for _, relation := range values {
		if relation.DeletedAt != nil || relation.ParentPersonID == relation.ChildPersonID {
			continue
		}
		if _, exists := persons[relation.ParentPersonID]; !exists {
			continue
		}
		if _, exists := persons[relation.ChildPersonID]; !exists {
			continue
		}
		typeName := relation.RelationType
		if typeName == "" {
			typeName = "unknown"
		}
		grouped[key{childID: relation.ChildPersonID, typeName: typeName}] = append(
			grouped[key{childID: relation.ChildPersonID, typeName: typeName}], relation,
		)
	}
	keys := make([]key, 0, len(grouped))
	for item := range grouped {
		keys = append(keys, item)
	}
	sort.Slice(keys, func(left int, right int) bool {
		if keys[left].childID != keys[right].childID {
			return keys[left].childID.String() < keys[right].childID.String()
		}
		return keys[left].typeName < keys[right].typeName
	})
	result := make([]relationGroup, 0, len(keys))
	for _, item := range keys {
		relations := grouped[item]
		sort.Slice(relations, func(left int, right int) bool {
			if relations[left].ParentPersonID != relations[right].ParentPersonID {
				return relations[left].ParentPersonID.String() < relations[right].ParentPersonID.String()
			}
			return relations[left].ID.String() < relations[right].ID.String()
		})
		parents := make([]uuid.UUID, 0, len(relations))
		seenParents := make(map[uuid.UUID]struct{})
		confidence := ""
		confidenceConsistent := true
		notes := make([]string, 0)
		for _, relation := range relations {
			if _, exists := seenParents[relation.ParentPersonID]; !exists {
				parents = append(parents, relation.ParentPersonID)
				seenParents[relation.ParentPersonID] = struct{}{}
			}
			if confidence == "" {
				confidence = relation.Confidence
			} else if relation.Confidence != confidence {
				confidenceConsistent = false
			}
			if note := cleanText(relation.Note); note != "" {
				notes = append(notes, fmt.Sprintf("Parent %s: %s", relation.ParentPersonID, note))
			}
		}
		if !confidenceConsistent {
			confidence = ""
			for _, relation := range relations {
				if relation.Confidence != "" {
					notes = append(notes, fmt.Sprintf(
						"Parent %s confidence: %s", relation.ParentPersonID, relation.Confidence,
					))
				}
			}
		} else if confidence != "" && gedcomFamilyStatus(confidence) == "" {
			notes = append(notes, "FamilyTree confidence: "+confidence)
		}
		result = append(result, relationGroup{
			ChildID: item.childID, Type: item.typeName,
			ParentIDs: parents, Confidence: confidence, Notes: notes,
		})
	}
	return result
}

func writeIndividual(
	result *writer,
	person manifest.Person,
	names []manifest.PersonName,
	links *personLinks,
) {
	result.line(0, personXRef(person.ID), "INDI", "")
	if person.PrivacyLevel == "tree_members" {
		result.line(1, "", "RESN", "CONFIDENTIAL")
	}
	for _, name := range names {
		writeName(result, name)
	}
	if sex := gedcomSex(person.Sex); sex != "" {
		result.line(1, "", "SEX", sex)
	}
	if person.LifeStatus == "deceased" {
		result.line(1, "", "DEAT", "Y")
	}
	if biography := cleanText(person.Biography); biography != "" {
		result.line(1, "", "NOTE", "Biography:\n"+biography)
	}
	if notes := cleanText(person.Notes); notes != "" {
		result.line(1, "", "NOTE", "Notes:\n"+notes)
	}
	result.line(1, "", "UID", person.ID.String())
	if links == nil {
		return
	}
	familyXRefs := mapStringKeys(links.FamiliesAsPartner)
	for _, xref := range familyXRefs {
		result.pointerLine(1, "FAMS", xref)
	}
	childXRefs := make([]string, 0, len(links.FamiliesAsChild))
	for xref := range links.FamiliesAsChild {
		childXRefs = append(childXRefs, xref)
	}
	sort.Strings(childXRefs)
	for _, xref := range childXRefs {
		link := links.FamiliesAsChild[xref]
		result.pointerLine(1, "FAMC", xref)
		pedi, phrase := gedcomPedigree(link.RelationType)
		result.line(2, "", "PEDI", pedi)
		if phrase != "" {
			result.line(3, "", "PHRASE", phrase)
		}
		if status := gedcomFamilyStatus(link.Confidence); status != "" {
			result.line(2, "", "STAT", status)
		}
		for _, note := range link.Notes {
			result.line(2, "", "NOTE", note)
		}
	}
}

func writeName(result *writer, name manifest.PersonName) {
	payload := personalName(name)
	if payload == "" {
		return
	}
	result.line(1, "", "NAME", payload)
	if nameType, phrase := gedcomNameType(name.Type); nameType != "" {
		result.line(2, "", "TYPE", nameType)
		if phrase != "" {
			result.line(3, "", "PHRASE", phrase)
		}
	}
	if prefix := cleanNamePiece(name.Prefix); prefix != "" {
		result.line(2, "", "NPFX", prefix)
	}
	given := strings.TrimSpace(strings.Join(nonEmpty(cleanNamePiece(name.GivenName), cleanNamePiece(name.Patronymic)), " "))
	if given != "" {
		result.line(2, "", "GIVN", given)
	}
	if surname := cleanNamePiece(name.FamilyName); surname != "" {
		result.line(2, "", "SURN", surname)
	}
	if suffix := cleanNamePiece(name.Suffix); suffix != "" {
		result.line(2, "", "NSFX", suffix)
	}
}

func writeFamily(result *writer, item family, persons map[uuid.UUID]manifest.Person) {
	result.line(0, item.XRef, "FAM", "")
	if len(item.Partners) > 0 {
		tag := "HUSB"
		if len(item.Partners) == 1 && persons[item.Partners[0]].Sex == "female" {
			tag = "WIFE"
		}
		result.pointerLine(1, tag, personXRef(item.Partners[0]))
		if role := item.PartnerRoles[item.Partners[0]]; role != "" {
			result.line(2, "", "PHRASE", role)
		}
	}
	if len(item.Partners) > 1 {
		result.pointerLine(1, "WIFE", personXRef(item.Partners[1]))
		if role := item.PartnerRoles[item.Partners[1]]; role != "" {
			result.line(2, "", "PHRASE", role)
		}
	}
	children := append([]uuid.UUID(nil), item.Children...)
	sortUUIDs(children)
	for _, childID := range children {
		result.pointerLine(1, "CHIL", personXRef(childID))
	}
	if item.Union != nil {
		writeUnion(result, *item.Union)
		result.line(1, "", "REFN", item.Union.ID.String())
		result.line(2, "", "TYPE", "FamilyTree union ID")
	}
}

func writeUnion(result *writer, union manifest.FamilyUnion) {
	switch union.Type {
	case "marriage":
		result.line(1, "", "MARR", "Y")
	case "engagement":
		result.line(1, "", "ENGA", "Y")
	case "civil_union":
		result.line(1, "", "EVEN", "")
		result.line(2, "", "TYPE", "Civil union")
	case "partnership":
		result.line(1, "", "EVEN", "")
		result.line(2, "", "TYPE", "Partnership")
	default:
		result.line(1, "", "EVEN", "")
		result.line(2, "", "TYPE", "Family union")
	}
	if note := cleanText(union.Note); note != "" {
		result.line(1, "", "NOTE", note)
	}
	if reason := cleanText(union.EndReason); reason != "" {
		result.line(1, "", "NOTE", "Union end reason:\n"+reason)
	}
}

func namesByPerson(
	values []manifest.PersonName,
	persons map[uuid.UUID]manifest.Person,
) map[uuid.UUID][]manifest.PersonName {
	result := make(map[uuid.UUID][]manifest.PersonName)
	for _, name := range values {
		if _, exists := persons[name.PersonID]; exists {
			result[name.PersonID] = append(result[name.PersonID], name)
		}
	}
	for personID := range result {
		names := result[personID]
		sort.Slice(names, func(left int, right int) bool {
			if names[left].IsPreferred != names[right].IsPreferred {
				return names[left].IsPreferred
			}
			if names[left].Type != names[right].Type {
				return names[left].Type < names[right].Type
			}
			return names[left].ID.String() < names[right].ID.String()
		})
		result[personID] = names
	}
	return result
}

func personalName(name manifest.PersonName) string {
	prefix := cleanNamePiece(name.Prefix)
	given := cleanNamePiece(name.GivenName)
	patronymic := cleanNamePiece(name.Patronymic)
	surname := cleanNamePiece(name.FamilyName)
	suffix := cleanNamePiece(name.Suffix)
	if prefix == "" && given == "" && patronymic == "" && surname == "" && suffix == "" {
		return cleanNamePiece(name.FullText)
	}
	parts := nonEmpty(prefix, given, patronymic)
	if surname != "" {
		parts = append(parts, "/"+surname+"/")
	}
	if suffix != "" {
		parts = append(parts, suffix)
	}
	return strings.Join(parts, " ")
}

func partnerPairs(personIDs []uuid.UUID) [][]uuid.UUID {
	if len(personIDs) == 1 {
		return [][]uuid.UUID{{personIDs[0]}}
	}
	result := make([][]uuid.UUID, 0, len(personIDs)-1)
	for index := 1; index < len(personIDs); index++ {
		result = append(result, []uuid.UUID{personIDs[0], personIDs[index]})
	}
	return result
}

func parentChunks(personIDs []uuid.UUID) [][]uuid.UUID {
	result := make([][]uuid.UUID, 0, (len(personIDs)+1)/2)
	for index := 0; index < len(personIDs); index += 2 {
		end := min(index+2, len(personIDs))
		result = append(result, append([]uuid.UUID(nil), personIDs[index:end]...))
	}
	return result
}

func matchingFamily(values []family, parentIDs []uuid.UUID, childID uuid.UUID) int {
	for index := range values {
		item := values[index]
		if _, exists := item.ChildLinks[childID]; exists {
			continue
		}
		if len(parentIDs) == 1 && len(item.Partners) == 1 && containsUUID(item.Partners, parentIDs[0]) {
			return index
		}
		if len(parentIDs) == 2 && len(item.Partners) == 2 &&
			containsUUID(item.Partners, parentIDs[0]) && containsUUID(item.Partners, parentIDs[1]) {
			return index
		}
	}
	return -1
}

func orderedPartners(values []uuid.UUID, persons map[uuid.UUID]manifest.Person) []uuid.UUID {
	result := append([]uuid.UUID(nil), values...)
	sortUUIDs(result)
	if len(result) == 2 && persons[result[0]].Sex == "female" && persons[result[1]].Sex == "male" {
		result[0], result[1] = result[1], result[0]
	}
	return result
}

func relationFamilyXRef(childID uuid.UUID, relationType string, index int, parents []uuid.UUID) string {
	parts := []string{"REL", childID.String(), relationType, fmt.Sprintf("%d", index)}
	for _, parentID := range parents {
		parts = append(parts, parentID.String())
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return "FREL_" + strings.ToUpper(hex.EncodeToString(digest[:]))
}

func gedcomNameType(value string) (string, string) {
	switch value {
	case "birth":
		return "BIRTH", ""
	case "married":
		return "MARRIED", ""
	case "alias":
		return "AKA", ""
	case "transliteration":
		return "OTHER", "Transliteration"
	case "other":
		return "OTHER", "Other"
	default:
		return "", ""
	}
}

func gedcomSex(value string) string {
	switch value {
	case "male":
		return "M"
	case "female":
		return "F"
	case "unknown", "not_specified":
		return "U"
	default:
		return ""
	}
}

func gedcomPedigree(value string) (string, string) {
	switch value {
	case "biological":
		return "BIRTH", ""
	case "adoptive":
		return "ADOPTED", ""
	case "foster":
		return "FOSTER", ""
	case "guardian":
		return "OTHER", "Guardian"
	case "step":
		return "OTHER", "Step"
	default:
		return "OTHER", "Unknown"
	}
}

func gedcomFamilyStatus(value string) string {
	switch value {
	case "confirmed":
		return "PROVEN"
	case "disputed":
		return "CHALLENGED"
	default:
		return ""
	}
}

func gedcomDate(day int, month int, year int) string {
	months := [...]string{"", "JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"}
	if day < 1 || month < 1 || month >= len(months) || year < 1 {
		return ""
	}
	return fmt.Sprintf("%d %s %d", day, months[month], year)
}

func (value *writer) line(level int, xref string, tag string, payload string) {
	lines := strings.Split(cleanText(payload), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	value.buffer.WriteString(fmt.Sprintf("%d", level))
	if xref != "" {
		value.buffer.WriteByte(' ')
		value.buffer.WriteString(pointer(xref))
	}
	value.buffer.WriteByte(' ')
	value.buffer.WriteString(tag)
	if lines[0] != "" {
		value.buffer.WriteByte(' ')
		value.buffer.WriteString(escapeLineString(lines[0]))
	}
	value.buffer.WriteString("\r\n")
	for _, continuation := range lines[1:] {
		value.buffer.WriteString(fmt.Sprintf("%d CONT", level+1))
		if continuation != "" {
			value.buffer.WriteByte(' ')
			value.buffer.WriteString(escapeLineString(continuation))
		}
		value.buffer.WriteString("\r\n")
	}
}

func (value *writer) pointerLine(level int, tag string, xref string) {
	value.buffer.WriteString(fmt.Sprintf("%d %s %s\r\n", level, tag, pointer(xref)))
}

func cleanText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' {
			return character
		}
		if unicode.IsControl(character) || character == '\u007f' ||
			character == '\ufffe' || character == '\uffff' {
			return -1
		}
		return character
	}, value)
}

func cleanInline(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || character == '\u007f' ||
			character == '\ufffe' || character == '\uffff' {
			return ' '
		}
		return character
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func cleanNamePiece(value string) string {
	return strings.ReplaceAll(cleanInline(value), "/", " ")
}

func escapeLineString(value string) string {
	if strings.HasPrefix(value, "@") {
		return "@" + value
	}
	return value
}

func pointer(xref string) string { return "@" + xref + "@" }

func personXRef(personID uuid.UUID) string { return "I" + compactUUID(personID) }

func compactUUID(value uuid.UUID) string {
	return strings.ToUpper(strings.ReplaceAll(value.String(), "-", ""))
}

func sortedPersonIDs(persons map[uuid.UUID]manifest.Person) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(persons))
	for personID := range persons {
		result = append(result, personID)
	}
	sortUUIDs(result)
	return result
}

func roleMapKeys(values map[uuid.UUID]string) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sortUUIDs(result)
	return result
}

func mapStringKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortUUIDs(values []uuid.UUID) {
	sort.Slice(values, func(left int, right int) bool {
		return values[left].String() < values[right].String()
	})
}

func containsUUID(values []uuid.UUID, expected uuid.UUID) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func nonEmpty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func newPersonLinks() *personLinks {
	return &personLinks{
		FamiliesAsPartner: make(map[string]struct{}),
		FamiliesAsChild:   make(map[string]familyChildLink),
	}
}
