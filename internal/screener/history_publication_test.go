package screener

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const historyFirstName = "audusd-d1-2006-01-01-2026-10-25.csv"
const historySecondName = "audusd-d1-2010-01-01-2026-10-25.csv"
const historyUnionName = "audusd-d1-2005-01-01-2026-10-27.csv"
const historyUnionCSV = "timestamp,open,high,low,close,volume,sma_2\n1104537600000,1,3,1,2,10,\n1262304000000,2,4,2,3,20,2.5\n1792972800000,3,5,3,4,30,3.5\n"

type historyFixture struct {
	directory string
	staging   string
	sources   []downloadSource
	originals map[string]os.FileInfo
	contents  map[string]string
}

func newHistoryFixture(t *testing.T) historyFixture {
	t.Helper()
	fixture := historyFixture{directory: t.TempDir(), originals: map[string]os.FileInfo{}, contents: map[string]string{
		historyFirstName:  "timestamp,open,high,low,close,volume\n1136073600000,1,3,1,2,10\n",
		historySecondName: "timestamp,open,high,low,close,volume,sma_2\n1262304000000,2,4,2,3,20,2.5\n",
	}}
	for _, name := range []string{historyFirstName, historySecondName} {
		input := filepath.Join(fixture.directory, name)
		if err := os.WriteFile(input, []byte(fixture.contents[name]), 0640); err != nil {
			t.Fatal(err)
		}
		info, checksum, err := historyFingerprint(input)
		if err != nil {
			t.Fatal(err)
		}
		metadata := fileMetadata{Version: 1, Filename: name, Bytes: info.Size(), Modified: info.ModTime().UnixNano(), Checksum: checksum}
		if err := writeFileMetadata(fixture.directory, name, metadata); err != nil {
			t.Fatal(err)
		}
		match := downloadFilename.FindStringSubmatch(name)
		fixture.sources = append(fixture.sources, downloadSource{Input: input, Name: name, From: match[3], To: match[4], Bytes: info.Size(), Modified: info.ModTime().UnixMilli(), Metadata: metadata})
		fixture.originals[input] = info
	}
	fixture.staging = filepath.Join(fixture.directory, ".worker-result")
	if err := writeSyncedExclusive(fixture.staging, []byte(historyUnionCSV)); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func publishHistoryFixture(t *testing.T, fixture historyFixture, name string) string {
	t.Helper()
	first, _ := time.Parse("2006-01-02", "2005-01-01")
	last, _ := time.Parse("2006-01-02", "2026-10-26")
	backup, err := publishHistory(fixture.directory, name, fixture.staging, fixture.sources, fixture.originals, fileMetadata{
		FirstTimestamp: first.UnixMilli(), LastTimestamp: last.UnixMilli(),
		Recipes: []EnrichmentRequest{{Indicator: "sma", Parameters: map[string]float64{"period": 2}, Source: "close"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(backup)
}

func assertHistoryPublished(t *testing.T, fixture historyFixture, name, transaction string) {
	t.Helper()
	names, err := csvNames(fixture.directory)
	if err != nil || !slices.Equal(names, []string{name}) {
		t.Fatalf("published names = %v, error = %v", names, err)
	}
	data, err := os.ReadFile(filepath.Join(fixture.directory, name))
	if err != nil || string(data) != historyUnionCSV {
		t.Fatalf("published union was changed: %q, %v", data, err)
	}
	info, err := os.Stat(filepath.Join(fixture.directory, name))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := readFileMetadata(fixture.directory, name)
	if err != nil || !metadataMatches(metadata, info) || len(metadata.Recipes) != 1 {
		t.Fatalf("published metadata = %+v, error = %v", metadata, err)
	}
	for i, source := range fixture.sources {
		backup, err := os.ReadFile(historySourceBackup(transaction, i))
		if err != nil || string(backup) != fixture.contents[source.Name] {
			t.Fatalf("source %s exact backup lost: %q, %v", source.Name, backup, err)
		}
		if source.Name != name {
			if _, err := os.Lstat(historyMetadataPath(fixture.directory, source.Name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("superseded metadata survived: %s, %v", source.Name, err)
			}
		}
	}
	if err := recoverHistoryPublications(fixture.directory); err != nil {
		t.Fatalf("completed publication recovery: %v", err)
	}
}

func TestHistoryPublicationKeepsOneUnionAndExactPrivateOriginals(t *testing.T) {
	for _, name := range []string{historyUnionName, historyFirstName} {
		t.Run(name, func(t *testing.T) {
			fixture := newHistoryFixture(t)
			transaction := publishHistoryFixture(t, fixture, name)
			assertHistoryPublished(t, fixture, name, transaction)
			if err := os.Remove(fixture.staging); err != nil {
				t.Fatal(err)
			}
			assertHistoryPublished(t, fixture, name, transaction)
		})
	}
}

// Reconstruct actual filesystem states at publication boundaries, then run
// startup recovery with the original worker scratch file already gone.
func rewindHistoryPublication(t *testing.T, fixture historyFixture, name, transaction, boundary string) {
	t.Helper()
	if err := os.Remove(filepath.Join(transaction, "complete")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.staging); err != nil {
		t.Fatal(err)
	}
	if boundary == "prepared" {
		if err := os.Remove(filepath.Join(fixture.directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	if boundary == "prepared" || boundary == "csv published" {
		if err := os.Remove(historyMetadataPath(fixture.directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	for i, source := range fixture.sources {
		if boundary == "cleanup complete" || (boundary == "partial cleanup" && i == 0) {
			continue
		}
		if boundary == "prepared" || source.Name != name {
			if err := os.Link(historySourceBackup(transaction, i), source.Input); err != nil {
				t.Fatal(err)
			}
		}
		if boundary == "prepared" || boundary == "csv published" || source.Name != name {
			if err := os.Link(historyMetadataBackup(transaction, i), historyMetadataPath(fixture.directory, source.Name)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestHistoryPublicationRecoveryFinishesEveryCommitBoundary(t *testing.T) {
	for _, name := range []string{historyUnionName, historyFirstName} {
		for _, boundary := range []string{"prepared", "csv published", "metadata published", "partial cleanup", "cleanup complete"} {
			t.Run(name+"/"+boundary, func(t *testing.T) {
				fixture := newHistoryFixture(t)
				transaction := publishHistoryFixture(t, fixture, name)
				rewindHistoryPublication(t, fixture, name, transaction, boundary)
				if err := recoverHistoryPublications(fixture.directory); err != nil {
					t.Fatal(err)
				}
				assertHistoryPublished(t, fixture, name, transaction)
			})
		}
	}
}

func TestHistoryPublicationRejectsUnplannedFilesAndChangedSources(t *testing.T) {
	for _, scenario := range []string{"destination collision", "new sibling", "replaced source", "tampered same inode", "metadata symlink", "backup folder symlink"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newHistoryFixture(t)
			foreign := filepath.Join(fixture.directory, "foreign")
			if err := os.WriteFile(foreign, []byte("external data"), 0600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "destination collision":
				if err := os.Link(foreign, filepath.Join(fixture.directory, historyUnionName)); err != nil {
					t.Fatal(err)
				}
			case "new sibling":
				if err := os.Link(foreign, filepath.Join(fixture.directory, "audusd-d1-2000-01-01-2026-01-01.csv")); err != nil {
					t.Fatal(err)
				}
			case "replaced source":
				if err := os.Remove(fixture.sources[0].Input); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fixture.sources[0].Input, []byte(fixture.contents[historyFirstName]), 0640); err != nil {
					t.Fatal(err)
				}
			case "tampered same inode":
				original := fixture.originals[fixture.sources[0].Input]
				changed := strings.Replace(fixture.contents[historyFirstName], ",2,10", ",9,10", 1)
				if err := os.WriteFile(fixture.sources[0].Input, []byte(changed), 0640); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(fixture.sources[0].Input, original.ModTime(), original.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "metadata symlink":
				path := historyMetadataPath(fixture.directory, historyFirstName)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(foreign, path); err != nil {
					t.Fatal(err)
				}
			case "backup folder symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(fixture.directory, historyPublicationFolder)); err != nil {
					t.Fatal(err)
				}
			}
			before := map[string][]byte{}
			for _, source := range fixture.sources {
				before[source.Name], _ = os.ReadFile(source.Input)
			}
			if _, err := publishHistory(fixture.directory, historyUnionName, fixture.staging, fixture.sources, fixture.originals, fileMetadata{}); err == nil {
				t.Fatal("unsafe publication succeeded")
			}
			for _, source := range fixture.sources {
				after, err := os.ReadFile(source.Input)
				if err != nil || string(after) != string(before[source.Name]) {
					t.Fatalf("failed publication modified %s: %v", source.Name, err)
				}
			}
			if after, _ := os.ReadFile(foreign); string(after) != "external data" {
				t.Fatal("external file changed")
			}
		})
	}
}

func TestHistoryPublicationRecoveryRefusesExternalChanges(t *testing.T) {
	for _, scenario := range []string{"source replaced", "source edited", "result edited", "metadata edited", "new sibling", "destination collision"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newHistoryFixture(t)
			transaction := publishHistoryFixture(t, fixture, historyUnionName)
			boundary := "metadata published"
			if scenario == "destination collision" {
				boundary = "prepared"
			}
			rewindHistoryPublication(t, fixture, historyUnionName, transaction, boundary)
			switch scenario {
			case "source replaced":
				if err := os.Remove(fixture.sources[0].Input); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fixture.sources[0].Input, []byte("external source"), 0600); err != nil {
					t.Fatal(err)
				}
			case "source edited":
				if err := os.WriteFile(fixture.sources[0].Input, []byte("external edit"), 0600); err != nil {
					t.Fatal(err)
				}
			case "result edited":
				if err := os.WriteFile(filepath.Join(fixture.directory, historyUnionName), []byte("external result"), 0600); err != nil {
					t.Fatal(err)
				}
			case "metadata edited":
				if err := os.WriteFile(historyMetadataPath(fixture.directory, historyUnionName), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "new sibling":
				if err := os.WriteFile(filepath.Join(fixture.directory, "audusd-d1-2001-01-01-2020-01-01.csv"), []byte("new source"), 0600); err != nil {
					t.Fatal(err)
				}
			case "destination collision":
				if err := os.WriteFile(filepath.Join(fixture.directory, historyUnionName), []byte("external collision"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := recoverHistoryPublications(fixture.directory); err == nil {
				t.Fatal("recovery accepted external changes")
			}
			for _, source := range fixture.sources {
				if _, err := os.Lstat(source.Input); err != nil {
					t.Fatalf("recovery removed original %s: %v", source.Name, err)
				}
			}
		})
	}
}

func TestHistoryPublicationNewAssetAndInterruptedCompletionMarker(t *testing.T) {
	directory := t.TempDir()
	staging := filepath.Join(directory, ".complete-csv")
	if err := writeSyncedExclusive(staging, []byte(historyUnionCSV)); err != nil {
		t.Fatal(err)
	}
	backup, err := publishHistory(directory, historyUnionName, staging, nil, nil, fileMetadata{})
	if err != nil || backup != "" {
		t.Fatalf("new history publication: backup %q, error %v", backup, err)
	}
	entries, err := os.ReadDir(filepath.Join(directory, historyPublicationFolder))
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	transaction := filepath.Join(directory, historyPublicationFolder, entries[0].Name())
	if err := os.Remove(filepath.Join(transaction, "complete")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transaction, ".complete.tmp"), []byte("com"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(staging); err != nil {
		t.Fatal(err)
	}
	if err := recoverHistoryPublications(directory); err != nil {
		t.Fatal(err)
	}
	metadata, err := readFileMetadata(directory, historyUnionName)
	digest := sha256.Sum256([]byte(historyUnionCSV))
	if err != nil || metadata.Checksum != hex.EncodeToString(digest[:]) {
		t.Fatalf("recovered metadata: %+v, %v", metadata, err)
	}
}

func TestCanonicalHistoryNameIncludesRequestedExistingAndActualBounds(t *testing.T) {
	first, _ := time.Parse(time.RFC3339, "2005-12-31T23:00:00Z")
	last, _ := time.Parse(time.RFC3339, "2026-10-26T00:00:00Z")
	for _, test := range []struct {
		name, from, to, expected string
		first, last              int64
	}{
		{"existing wider", "2015-01-01", "2020-01-01", "audusd-d1-2006-01-01-2026-10-25.csv", 0, 0},
		{"request wider", "2000-01-01", "2026-12-31", "audusd-d1-2000-01-01-2026-12-31.csv", first.UnixMilli(), last.UnixMilli()},
		{"actual wider", "2015-01-01", "2020-01-01", "audusd-d1-2005-12-31-2026-10-27.csv", first.UnixMilli(), last.UnixMilli()},
	} {
		t.Run(test.name, func(t *testing.T) {
			name, err := canonicalHistoryName("audusd", "d1", test.from, test.to, []downloadSource{{Name: historyFirstName}}, fileMetadata{FirstTimestamp: test.first, LastTimestamp: test.last})
			if err != nil || name != test.expected {
				t.Fatalf("name %q, want %q: %v", name, test.expected, err)
			}
		})
	}
	for _, invalid := range [][4]string{{"../audusd", "d1", "2006-01-01", "2026-01-01"}, {"audusd", "unknown", "2006-01-01", "2026-01-01"}, {"audusd", "d1", "2006-02-30", "2026-01-01"}, {"audusd", "d1", "2026-01-01", "2006-01-01"}} {
		if _, err := canonicalHistoryName(invalid[0], invalid[1], invalid[2], invalid[3], nil, fileMetadata{}); err == nil {
			t.Fatalf("invalid name accepted: %v", invalid)
		}
	}
}
