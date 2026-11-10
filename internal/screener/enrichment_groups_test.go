package screener

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEnrichmentGroupDefaultsValidationAndCompatibility(t *testing.T) {
	m := &Manager{}
	for _, group := range []string{"Day / Week ranges", "Moving averages", "Time", "Price range", "Trend", "Volume", "Volatility", "Momentum"} {
		request, err := m.validateEnrichment(EnrichmentRequest{Group: group, Period: "h1", Source: "askprice"})
		if err != nil {
			t.Fatalf("%s: %v", group, err)
		}
		members, err := m.enrichmentMembers(request)
		if err != nil || len(members) == 0 {
			t.Fatalf("%s members: %v", group, err)
		}
		for _, member := range members {
			if member.Group != "" || member.Indicator == "" || len(member.Parameters) != len(indicatorDefinitions()[member.Indicator].Parameters) {
				t.Fatalf("noncanonical group member: %+v", member)
			}
		}
		if group == "Day / Week ranges" && (len(members) != 8 || request.Source != "auto") {
			t.Fatalf("range group: %+v", request)
		}
	}
	for _, request := range []EnrichmentRequest{{Group: "unknown"}, {Group: "Time", Indicator: "hour"}, {Group: "Moving averages", Parameters: map[string]float64{"period": 5}}, {Group: "Day / Week ranges", Period: "d1"}, {Group: "Day / Week ranges", Period: "tick"}, {Group: "Time", Source: "invalid"}} {
		if _, err := m.validateEnrichment(request); err == nil {
			t.Fatalf("invalid group accepted: %+v", request)
		}
	}
}

func TestRangeGroupEnrichesOnceAndPersistsIndividualRecipesWithRecoverableBackup(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "eurusd-h1-2024-01-01-2024-01-03.csv"
	text := "timestamp,open,high,low,close,volume\n1704067200000,10,12,8,10,1\n1704150000000,10,15,6,10,1\n1704153600000,8,13,3,8,1\n"
	path := writeDataFile(t, m, name, text)
	excluded := writeDataFile(t, m, "eurusd-d1-2024-01-01-2024-01-03.csv", text)
	id := dataFileID(m.directory, name)
	for repeat := 0; repeat < 2; repeat++ {
		before, _ := os.ReadFile(path)
		initial, err := m.StartEnrichment(EnrichmentRequest{Group: "Day / Week ranges"})
		if err != nil || initial.Total != 1 {
			t.Fatalf("batch: %+v %v", initial, err)
		}
		done := waitEnrichment(t, m, []string{id})
		if done.Completed != 1 || done.Failed != 0 || len(done.Jobs) != 1 || !reflect.DeepEqual(done.Jobs[0].Columns, calendarRangeIDs) {
			t.Fatalf("group result: %+v", done)
		}
		backup, err := os.ReadFile(done.Jobs[0].BackupPath)
		if err != nil || string(backup) != string(before) {
			t.Fatalf("exact original backup: %v", err)
		}
		data, _ := os.ReadFile(path)
		rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
		if err != nil || len(rows[0]) != 14 || rows[2][6] != "6" || rows[3][8] != "6" {
			t.Fatalf("range CSV: %s %v", data, err)
		}
		metadata, err := readFileMetadata(m.directory, name)
		if err != nil || len(metadata.Recipes) != 8 {
			t.Fatalf("per-member recipes: %+v %v", metadata, err)
		}
		for _, recipe := range metadata.Recipes {
			if recipe.Group != "" || recipe.Period != "" || recipe.DataSource != "" {
				t.Fatalf("batch scope leaked into recipe: %+v", recipe)
			}
		}
	}
	unchanged, _ := os.ReadFile(excluded)
	if string(unchanged) != text {
		t.Fatal("group touched daily CSV")
	}
	recovered := legacyRecipes(context.Background(), m.directory)[name]
	if len(recovered) != 8 {
		t.Fatalf("backup recipes were not recovered: %+v", recovered)
	}
	files, _ := os.ReadDir(filepath.Join(m.directory, ".cryptodesk-enrichment-backups"))
	if len(files) != 4 {
		t.Fatalf("expected one original and metadata per operation, got %d entries", len(files))
	}
}

func TestGroupFailureKeepsEntireFileAndPublishesNoPartialMembers(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	name := "eurusd-h1-2024-01-01-2024-01-02.csv"
	text := "timestamp,low\n1704067200000,8\n"
	path := writeDataFile(t, m, name, text)
	if _, err := m.StartEnrichment(EnrichmentRequest{Group: "Day / Week ranges", Period: "h1"}); err != nil {
		t.Fatal(err)
	}
	result := waitEnrichment(t, m, []string{dataFileID(m.directory, name)})
	got, _ := os.ReadFile(path)
	if result.Failed != 1 || result.Completed != 0 || string(got) != text {
		t.Fatalf("partial group publication: %+v %s", result, got)
	}
	if _, err := os.Stat(filepath.Join(m.directory, ".cryptodesk-enrichment-backups")); !os.IsNotExist(err) {
		t.Fatal("failed group created publication backups")
	}
}
