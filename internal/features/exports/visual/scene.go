package visual

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ZheglY/family_tree_app/internal/features/exports/domain"
	"github.com/ZheglY/family_tree_app/internal/features/exports/manifest"
	"github.com/google/uuid"
)

const (
	minimumSceneWidth = 1200.0
	headerHeight      = 148.0
	footerHeight      = 54.0
	horizontalMargin  = 76.0
	nodeWidth         = 232.0
	nodeHeight        = 98.0
	horizontalGap     = 54.0
	verticalGap       = 112.0
)

type point struct {
	X float64
	Y float64
}

type sceneNode struct {
	ID        uuid.UUID
	X         float64
	Y         float64
	NameLines []string
	Detail    string
}

type sceneEdge struct {
	Points []point
}

type scene struct {
	Width       float64
	Height      float64
	Title       string
	Subtitle    string
	Footer      string
	CreatedAt   time.Time
	EmptyText   string
	Nodes       []sceneNode
	ParentEdges []sceneEdge
	UnionEdges  []sceneEdge
}

func buildScene(value manifest.Manifest, maxNodes int, maxPixels int64) (scene, error) {
	if maxNodes < 1 || maxPixels < 1 {
		return scene{}, domain.ErrInvalidExport
	}
	activePersons := make(map[uuid.UUID]manifest.Person)
	for _, person := range value.Persons {
		if person.DeletedAt == nil {
			activePersons[person.ID] = person
		}
	}
	if len(activePersons) > maxNodes {
		return scene{}, domain.ErrExportVisualTooLarge
	}
	names := preferredNames(value.PersonNames, activePersons)
	children := make(map[uuid.UUID][]uuid.UUID, len(activePersons))
	parents := make(map[uuid.UUID][]uuid.UUID, len(activePersons))
	indegrees := make(map[uuid.UUID]int, len(activePersons))
	for personID := range activePersons {
		indegrees[personID] = 0
	}
	seenRelations := make(map[string]struct{})
	for _, relation := range value.ParentChildRelations {
		if relation.DeletedAt != nil {
			continue
		}
		if _, exists := activePersons[relation.ParentPersonID]; !exists {
			continue
		}
		if _, exists := activePersons[relation.ChildPersonID]; !exists {
			continue
		}
		key := relation.ParentPersonID.String() + "/" + relation.ChildPersonID.String()
		if _, exists := seenRelations[key]; exists {
			continue
		}
		seenRelations[key] = struct{}{}
		children[relation.ParentPersonID] = append(children[relation.ParentPersonID], relation.ChildPersonID)
		parents[relation.ChildPersonID] = append(parents[relation.ChildPersonID], relation.ParentPersonID)
		indegrees[relation.ChildPersonID]++
	}
	ranks, err := generationRanks(activePersons, children, indegrees)
	if err != nil {
		return scene{}, err
	}
	unionMembers := activeUnionMembers(value, activePersons)
	if err := alignUnionRanks(ranks, children, unionMembers, len(activePersons)); err != nil {
		return scene{}, err
	}
	layers := make(map[int][]uuid.UUID)
	maximumRank := 0
	for personID := range activePersons {
		rank := ranks[personID]
		layers[rank] = append(layers[rank], personID)
		if rank > maximumRank {
			maximumRank = rank
		}
	}
	for rank := range layers {
		sort.Slice(layers[rank], func(left int, right int) bool {
			leftName := strings.ToLower(names[layers[rank][left]])
			rightName := strings.ToLower(names[layers[rank][right]])
			if leftName != rightName {
				return leftName < rightName
			}
			return layers[rank][left].String() < layers[rank][right].String()
		})
	}
	maximumLayerSize := 1
	for _, personIDs := range layers {
		if len(personIDs) > maximumLayerSize {
			maximumLayerSize = len(personIDs)
		}
	}
	contentWidth := float64(maximumLayerSize)*nodeWidth + float64(maximumLayerSize-1)*horizontalGap
	width := max(minimumSceneWidth, contentWidth+2*horizontalMargin)
	layerCount := maximumRank + 1
	if len(activePersons) == 0 {
		layerCount = 1
	}
	height := headerHeight + float64(layerCount)*nodeHeight +
		float64(max(0, layerCount-1))*verticalGap + footerHeight
	if width > 32768 || height > 32768 || width*height > float64(maxPixels) {
		return scene{}, domain.ErrExportVisualTooLarge
	}
	result := scene{
		Width: width, Height: height,
		Title:     truncateText(strings.TrimSpace(value.Tree.Name), 48),
		Subtitle:  fmt.Sprintf("ФАМИЛЬНОЕ ДРЕВО | %d ПЕРСОН", len(activePersons)),
		Footer:    "Family Tree | " + value.Export.CreatedAt.UTC().Format("02.01.2006"),
		CreatedAt: value.Export.CreatedAt.UTC(),
		Nodes:     make([]sceneNode, 0, len(activePersons)),
	}
	if result.Title == "" {
		result.Title = "Семейное древо"
	}
	if len(activePersons) == 0 {
		result.EmptyText = "В дереве пока нет персон"
	}
	nodesByID := make(map[uuid.UUID]sceneNode, len(activePersons))
	for rank := 0; rank <= maximumRank; rank++ {
		personIDs := layers[rank]
		rowWidth := float64(len(personIDs))*nodeWidth + float64(max(0, len(personIDs)-1))*horizontalGap
		startX := (width - rowWidth) / 2
		for index, personID := range personIDs {
			person := activePersons[personID]
			node := sceneNode{
				ID:        personID,
				X:         startX + float64(index)*(nodeWidth+horizontalGap),
				Y:         headerHeight + float64(rank)*(nodeHeight+verticalGap),
				NameLines: wrapName(names[personID]),
				Detail:    personDetail(person),
			}
			result.Nodes = append(result.Nodes, node)
			nodesByID[personID] = node
		}
	}
	childIDs := make([]uuid.UUID, 0, len(parents))
	for childID := range parents {
		childIDs = append(childIDs, childID)
	}
	sort.Slice(childIDs, func(left int, right int) bool {
		return childIDs[left].String() < childIDs[right].String()
	})
	for _, childID := range childIDs {
		parentIDs := parents[childID]
		sort.Slice(parentIDs, func(left int, right int) bool {
			return parentIDs[left].String() < parentIDs[right].String()
		})
		childNode, childExists := nodesByID[childID]
		if !childExists {
			continue
		}
		for _, parentID := range parentIDs {
			parentNode, parentExists := nodesByID[parentID]
			if !parentExists {
				continue
			}
			start := point{X: parentNode.X + nodeWidth/2, Y: parentNode.Y + nodeHeight}
			end := point{X: childNode.X + nodeWidth/2, Y: childNode.Y}
			middleY := start.Y + (end.Y-start.Y)/2
			result.ParentEdges = append(result.ParentEdges, sceneEdge{Points: []point{
				start,
				{X: start.X, Y: middleY},
				{X: end.X, Y: middleY},
				end,
			}})
		}
	}
	result.UnionEdges = buildUnionEdges(unionMembers, nodesByID)
	return result, nil
}

func generationRanks(
	persons map[uuid.UUID]manifest.Person,
	children map[uuid.UUID][]uuid.UUID,
	indegrees map[uuid.UUID]int,
) (map[uuid.UUID]int, error) {
	queue := make([]uuid.UUID, 0, len(persons))
	for personID, indegree := range indegrees {
		if indegree == 0 {
			queue = append(queue, personID)
		}
	}
	sort.Slice(queue, func(left int, right int) bool { return queue[left].String() < queue[right].String() })
	ranks := make(map[uuid.UUID]int, len(persons))
	visited := 0
	for len(queue) > 0 {
		personID := queue[0]
		queue = queue[1:]
		visited++
		for _, childID := range children[personID] {
			if ranks[childID] < ranks[personID]+1 {
				ranks[childID] = ranks[personID] + 1
			}
			indegrees[childID]--
			if indegrees[childID] == 0 {
				queue = append(queue, childID)
			}
		}
	}
	if visited != len(persons) {
		return nil, fmt.Errorf("%w: parent-child graph contains a cycle", domain.ErrExportSourceInvalid)
	}
	return ranks, nil
}

func activeUnionMembers(
	value manifest.Manifest,
	persons map[uuid.UUID]manifest.Person,
) map[uuid.UUID][]uuid.UUID {
	activeUnions := make(map[uuid.UUID]bool)
	for _, union := range value.Unions {
		activeUnions[union.ID] = union.DeletedAt == nil
	}
	members := make(map[uuid.UUID][]uuid.UUID)
	for _, member := range value.UnionMembers {
		if !activeUnions[member.UnionID] {
			continue
		}
		if _, exists := persons[member.PersonID]; exists {
			members[member.UnionID] = append(members[member.UnionID], member.PersonID)
		}
	}
	return members
}

func alignUnionRanks(
	ranks map[uuid.UUID]int,
	children map[uuid.UUID][]uuid.UUID,
	unionMembers map[uuid.UUID][]uuid.UUID,
	personCount int,
) error {
	for iteration := 0; iteration <= personCount; iteration++ {
		changed := false
		for _, personIDs := range unionMembers {
			maximumRank := 0
			for _, personID := range personIDs {
				maximumRank = max(maximumRank, ranks[personID])
			}
			for _, personID := range personIDs {
				if ranks[personID] < maximumRank {
					ranks[personID] = maximumRank
					changed = true
				}
			}
		}
		for parentID, childIDs := range children {
			for _, childID := range childIDs {
				if ranks[childID] <= ranks[parentID] {
					ranks[childID] = ranks[parentID] + 1
					changed = true
				}
			}
		}
		if !changed {
			return nil
		}
	}
	return fmt.Errorf("%w: family unions conflict with ancestry generations", domain.ErrExportSourceInvalid)
}

func buildUnionEdges(
	members map[uuid.UUID][]uuid.UUID,
	nodes map[uuid.UUID]sceneNode,
) []sceneEdge {
	result := make([]sceneEdge, 0)
	unionIDs := make([]uuid.UUID, 0, len(members))
	for unionID := range members {
		unionIDs = append(unionIDs, unionID)
	}
	sort.Slice(unionIDs, func(left int, right int) bool {
		return unionIDs[left].String() < unionIDs[right].String()
	})
	for _, unionID := range unionIDs {
		personIDs := members[unionID]
		sort.Slice(personIDs, func(left int, right int) bool {
			leftNode, rightNode := nodes[personIDs[left]], nodes[personIDs[right]]
			if leftNode.Y != rightNode.Y {
				return leftNode.Y < rightNode.Y
			}
			return leftNode.X < rightNode.X
		})
		for index := 1; index < len(personIDs); index++ {
			left, right := nodes[personIDs[index-1]], nodes[personIDs[index]]
			start := point{X: left.X + nodeWidth, Y: left.Y + nodeHeight/2}
			end := point{X: right.X, Y: right.Y + nodeHeight/2}
			if start.X > end.X {
				start = point{X: left.X, Y: left.Y + nodeHeight/2}
				end = point{X: right.X + nodeWidth, Y: right.Y + nodeHeight/2}
			}
			middleX := start.X + (end.X-start.X)/2
			result = append(result, sceneEdge{Points: []point{
				start,
				{X: middleX, Y: start.Y},
				{X: middleX, Y: end.Y},
				end,
			}})
		}
	}
	return result
}

func preferredNames(
	values []manifest.PersonName,
	persons map[uuid.UUID]manifest.Person,
) map[uuid.UUID]string {
	result := make(map[uuid.UUID]string, len(persons))
	for _, name := range values {
		if !name.IsPreferred {
			continue
		}
		if _, exists := persons[name.PersonID]; !exists {
			continue
		}
		text := strings.TrimSpace(name.FullText)
		if text == "" {
			text = strings.TrimSpace(strings.Join([]string{
				name.Prefix, name.GivenName, name.Patronymic, name.FamilyName, name.Suffix,
			}, " "))
		}
		result[name.PersonID] = strings.Join(strings.Fields(text), " ")
	}
	for personID := range persons {
		if result[personID] == "" {
			result[personID] = "Без имени"
		}
	}
	return result
}

func wrapName(value string) []string {
	value = cleanDisplayText(value)
	words := strings.Fields(value)
	if len(words) == 0 {
		return []string{"Без имени"}
	}
	lines := []string{""}
	for _, word := range words {
		last := len(lines) - 1
		if lines[last] == "" {
			lines[last] = truncateText(word, 21)
			continue
		}
		candidate := strings.TrimSpace(lines[last] + " " + word)
		if utf8.RuneCountInString(candidate) <= 21 {
			lines[last] = candidate
			continue
		}
		if len(lines) == 2 {
			lines[1] = truncateText(lines[1]+" "+word, 21)
			continue
		}
		lines = append(lines, truncateText(word, 21))
	}
	return lines
}

func truncateText(value string, maximumRunes int) string {
	value = cleanDisplayText(value)
	if utf8.RuneCountInString(value) <= maximumRunes {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:max(1, maximumRunes-3)])) + "..."
}

func cleanDisplayText(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func personDetail(person manifest.Person) string {
	sex := map[string]string{
		"male": "Мужчина", "female": "Женщина", "unknown": "Пол не указан", "not_specified": "Пол не указан",
	}[person.Sex]
	if sex == "" {
		sex = "Пол не указан"
	}
	status := map[string]string{
		"alive": "Жив(а)", "deceased": "Ушёл(ла) из жизни", "unknown": "Статус жизни не указан",
	}[person.LifeStatus]
	if status == "" {
		status = "Статус жизни не указан"
	}
	return truncateText(sex+" | "+status, 32)
}
