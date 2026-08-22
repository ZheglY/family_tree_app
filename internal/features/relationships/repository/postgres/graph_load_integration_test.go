package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	relationpostgres "github.com/ZheglY/family_tree_app/internal/features/relationships/repository/postgres"
	"github.com/ZheglY/family_tree_app/internal/features/relationships/service"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	loadTreePersonCount  = 10_000
	loadGraphConcurrency = 12
	loadGraphP95Budget   = 3 * time.Second
)

func TestLargeGraphReadLoadBudget(t *testing.T) {
	databaseURL := os.Getenv("FAMILY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("FAMILY_TEST_DATABASE_URL is not configured")
	}
	setupContext, cancelSetup := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelSetup()
	database, err := testdatabase.Open(setupContext, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}()
	runner, err := migrations.NewRunner(database.Pool, logger.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(setupContext); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	treeID, ownerID, centerID, reachableCount := seedLargeGraph(t, setupContext, database)
	repository := relationpostgres.New(database.Pool)
	filter := service.GraphFilter{
		ActorUserID:      ownerID,
		TreeID:           treeID,
		CenterPersonID:   centerID,
		AncestorsDepth:   0,
		DescendantsDepth: service.MaxGraphDepth,
		MaxNodes:         service.MaxGraphNodes,
	}
	warmupContext, cancelWarmup := context.WithTimeout(context.Background(), loadGraphP95Budget)
	warmup, err := repository.LoadGraphAccessible(warmupContext, filter)
	cancelWarmup()
	if err != nil {
		t.Fatalf("warm up graph read: %v", err)
	}
	if len(warmup.Persons) != reachableCount || len(warmup.Relations) != reachableCount-1 {
		t.Fatalf(
			"warm up graph has persons=%d relations=%d, want persons=%d relations=%d",
			len(warmup.Persons),
			len(warmup.Relations),
			reachableCount,
			reachableCount-1,
		)
	}

	durations := make([]time.Duration, loadGraphConcurrency)
	errorsByRequest := make([]error, loadGraphConcurrency)
	var waitGroup sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < loadGraphConcurrency; index++ {
		waitGroup.Add(1)
		go func(requestIndex int) {
			defer waitGroup.Done()
			<-start
			requestContext, cancelRequest := context.WithTimeout(context.Background(), 2*loadGraphP95Budget)
			defer cancelRequest()
			startedAt := time.Now()
			graph, loadError := repository.LoadGraphAccessible(requestContext, filter)
			durations[requestIndex] = time.Since(startedAt)
			if loadError == nil && (len(graph.Persons) != reachableCount || len(graph.Relations) != reachableCount-1) {
				loadError = fmt.Errorf("incomplete graph response")
			}
			errorsByRequest[requestIndex] = loadError
		}(index)
	}
	close(start)
	waitGroup.Wait()
	for index, requestError := range errorsByRequest {
		if requestError != nil {
			t.Fatalf("concurrent graph request %d failed after %s: %v", index, durations[index], requestError)
		}
	}
	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	p95Index := (len(durations)*95+99)/100 - 1
	if p95 := durations[p95Index]; p95 > loadGraphP95Budget {
		t.Fatalf("large graph read p95 = %s, budget = %s, samples = %v", p95, loadGraphP95Budget, durations)
	}
	t.Logf(
		"large graph: total_persons=%d reachable=%d concurrency=%d p50=%s p95=%s",
		loadTreePersonCount,
		reachableCount,
		loadGraphConcurrency,
		durations[len(durations)/2],
		durations[p95Index],
	)
}

func seedLargeGraph(
	t *testing.T,
	ctx context.Context,
	database *testdatabase.Database,
) (uuid.UUID, uuid.UUID, uuid.UUID, int) {
	t.Helper()
	treeID := uuid.New()
	ownerID := uuid.New()
	now := time.Now().UTC()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO family_trees (
			id, name, owner_user_id, locale, timezone, created_at, updated_at
		) VALUES ($1, 'Large graph load test', $2, 'ru-RU', 'Europe/Moscow', $3, $3)
	`, treeID, ownerID, now); err != nil {
		t.Fatalf("create load test tree: %v", err)
	}
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO tree_members (
			tree_id, user_id, role, status, created_at, accepted_at
		) VALUES ($1, $2, 'owner', 'active', $3, $3)
	`, treeID, ownerID, now); err != nil {
		t.Fatalf("create load test owner membership: %v", err)
	}

	personIDs := make([]uuid.UUID, 0, loadTreePersonCount)
	relations := make([][]any, 0, 400)
	centerID := uuid.New()
	personIDs = append(personIDs, centerID)
	parents := []uuid.UUID{centerID}
	for depth := 0; depth < 5; depth++ {
		nextLevel := make([]uuid.UUID, 0, len(parents)*3)
		for _, parentID := range parents {
			for childIndex := 0; childIndex < 3; childIndex++ {
				childID := uuid.New()
				personIDs = append(personIDs, childID)
				nextLevel = append(nextLevel, childID)
				relations = append(relations, []any{
					uuid.New(), treeID, parentID, childID, "biological", "confirmed", "",
					ownerID, now, now, nil, 1,
				})
			}
		}
		parents = nextLevel
	}
	reachableCount := len(personIDs)
	for len(personIDs) < loadTreePersonCount {
		personIDs = append(personIDs, uuid.New())
	}
	personRows := make([][]any, 0, len(personIDs))
	nameRows := make([][]any, 0, len(personIDs))
	for index, personID := range personIDs {
		fullName := fmt.Sprintf("Person %05d", index)
		personRows = append(personRows, []any{
			personID, treeID, "unknown", "unknown", "", "", nil, "tree_members",
			ownerID, ownerID, now, now, nil, 1,
		})
		nameRows = append(nameRows, []any{
			uuid.New(), personID, treeID, "primary", fullName, "", "", "", "",
			fullName, true, "en", now, now,
		})
	}
	copyRows(t, ctx, database, "persons", []string{
		"id", "tree_id", "sex", "life_status", "biography", "notes", "primary_media_id",
		"privacy_level", "created_by", "updated_by", "created_at", "updated_at", "deleted_at", "version",
	}, personRows)
	copyRows(t, ctx, database, "person_names", []string{
		"id", "person_id", "tree_id", "type", "given_name", "patronymic", "family_name",
		"prefix", "suffix", "full_text", "is_preferred", "language_code", "created_at", "updated_at",
	}, nameRows)
	copyRows(t, ctx, database, "parent_child_relations", []string{
		"id", "tree_id", "parent_person_id", "child_person_id", "relation_type", "confidence",
		"note", "created_by", "created_at", "updated_at", "deleted_at", "version",
	}, relations)
	return treeID, ownerID, centerID, reachableCount
}

func copyRows(
	t *testing.T,
	ctx context.Context,
	database *testdatabase.Database,
	table string,
	columns []string,
	rows [][]any,
) {
	t.Helper()
	copied, err := database.Pool.CopyFrom(ctx, pgx.Identifier{table}, columns, pgx.CopyFromRows(rows))
	if err != nil {
		t.Fatalf("copy %s: %v", table, err)
	}
	if copied != int64(len(rows)) {
		t.Fatalf("copy %s rows = %d, want %d", table, copied, len(rows))
	}
}
