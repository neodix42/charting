package screener

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLiveAlertScanSentCounterStartsAtZeroBeforePreparationAndPersistsActualZero(t *testing.T) {
	for _, manual := range []bool{true, false} {
		mode := "scheduled"
		if manual {
			mode = "manual"
		}
		t.Run(mode, func(t *testing.T) {
			m, l, config, _ := liveAlertsFixture(t)
			id := config.ConditionIDs[0]
			count := int64(11)
			l.state.Sent = 11
			l.state.Checks = map[string]liveAlertCheckState{id: {LastChecked: 1704070800000, SentAlerts: 11, LastCheckSentAlerts: &count, CheckID: strings.Repeat("a", 32), AssetsScanned: 7, Status: "completed", Manual: true}}
			l.state.Watermarks = map[string]int64{id + "/eurusd/h1": 1704070800000}
			if err := writeState(l.path, l.state); err != nil {
				t.Fatal(err)
			}
			before := copyLiveCheckStates(l.state.Checks)
			claims := map[string]int64{}
			for key, value := range l.state.Watermarks {
				claims[key] = value
			}
			persisted, err := os.ReadFile(l.path)
			if err != nil {
				t.Fatal(err)
			}
			assertZero := func(phase string) {
				t.Helper()
				info := l.ProgressInfo()
				row := info.Checks[0]
				if row.SentAlerts != 0 || !row.SentAlertsKnown || row.LifetimeSentAlerts != 11 || info.Sent != 11 || row.LastChecked != before[id].LastChecked || row.AssetsScanned != 7 {
					t.Fatal("new scan retained prior sent count or rewrote history", phase, row, info.Sent)
				}
				if !reflect.DeepEqual(l.state.Checks, before) || !reflect.DeepEqual(l.state.Watermarks, claims) {
					t.Fatal("runtime counter reset changed saved attempt or delivery claims", phase)
				}
				if after, err := os.ReadFile(l.path); err != nil || !bytes.Equal(after, persisted) {
					t.Fatal("queue/preparation reset rewrote persistent state", phase, err)
				}
			}
			l.mu.Lock()
			l.queueCheckProgressLocked(config.ConditionIDs, manual)
			l.mu.Unlock()
			assertZero("queued")
			l.mu.Lock()
			l.prepareCheckProgressLocked(config.ConditionIDs, "Loading saved conditions")
			l.mu.Unlock()
			assertZero("preparing")
			l.mu.Lock()
			l.startCheckProgressLocked(id)
			l.mu.Unlock()
			assertZero("worker wait")
			l.mu.Lock()
			l.cancelCheckProgressLocked("Cancelled")
			l.mu.Unlock()
			assertZero("cancelled before attempt")
			run, err := l.beginCheckHistory(config, manual)
			if err != nil {
				t.Fatal(err)
			}
			if err = run.finish(nil); err != nil {
				t.Fatal(err)
			}
			row := l.Info().Checks[0]
			if row.SentAlerts != 0 || !row.SentAlertsKnown || row.AssetsScanned != 0 || row.LifetimeSentAlerts != 11 || l.Info().Sent != 11 || !reflect.DeepEqual(l.state.Watermarks, claims) {
				t.Fatal("zero-scanned attempt retained old sent count or erased totals/claims", row)
			}
			restored, err := NewLiveAlerts(m, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			row = restored.Info().Checks[0]
			if row.SentAlerts != 0 || !row.SentAlertsKnown || row.LifetimeSentAlerts != 11 || restored.Info().Sent != 11 || !reflect.DeepEqual(restored.state.Watermarks, claims) {
				t.Fatal("restart lost zero attempt or historical accounting", row)
			}
		})
	}
}
