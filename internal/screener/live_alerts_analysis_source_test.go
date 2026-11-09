package screener

import (
	"strings"
	"testing"
)

func TestLiveAlertInheritsAnalysisSourceOnceAndPersistsExplicitOverrides(t *testing.T) {
	m := sourceEnrichmentManager(t, false)
	request := defaultAnalysisRequest()
	request.DataSource, request.Period = "hyperliquid", "d1"
	saved, err := m.SaveAnalysisCondition("Hyperliquid saved condition", request)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	entry := LiveAlertDefinition{ID: strings.Repeat("a", 32), Name: "Inherited source", ConditionID: saved.ID, Period: "d1", Enabled: true}
	info, err := l.Configure(LiveAlertsConfig{Alerts: []LiveAlertDefinition{entry}})
	if err != nil || len(info.Config.Alerts) != 1 || info.Config.Alerts[0].DataSource != "hyperliquid" {
		t.Fatal("new alert did not inherit saved Analysis source", info.Config, err)
	}
	request.DataSource = "dukascopy"
	if _, err = m.UpdateAnalysisCondition(saved.ID, request); err != nil {
		t.Fatal(err)
	}
	info = l.Info()
	if info.Config.Alerts[0].DataSource != "hyperliquid" {
		t.Fatal("editing saved condition silently changed existing alert override", info.Config)
	}
	// Even an omitted source on an edited existing row retains its last explicit
	// choice, instead of following a changed reusable condition.
	info, err = l.Configure(LiveAlertsConfig{Alerts: []LiveAlertDefinition{entry}})
	if err != nil || info.Config.Alerts[0].DataSource != "hyperliquid" {
		t.Fatal("existing source was overwritten by saved condition edit", info.Config, err)
	}
	entry.DataSource = "dukascopy"
	legacy := entry
	legacy.ID, legacy.Name, legacy.DataSource = strings.Repeat("b", 32), "Retained legacy source", "bybit"
	info, err = l.Configure(LiveAlertsConfig{Alerts: []LiveAlertDefinition{entry, legacy}})
	if err != nil || info.Config.Alerts[0].DataSource != "dukascopy" || info.Config.Alerts[1].DataSource != "bybit" {
		t.Fatal("explicit or legacy choice was replaced", info.Config, err)
	}
	restored, err := NewLiveAlerts(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got := restored.Info().Config.Alerts; len(got) != 2 || got[0].DataSource != "dukascopy" || got[1].DataSource != "bybit" {
		t.Fatal("saved alert sources did not survive restart", got)
	}
}

func TestLegacyAlertSourceDefaultsUseAnalysisAndRespectExplicitDukascopy(t *testing.T) {
	id := strings.Repeat("a", 32)
	saved := []SavedAnalysisCondition{{ID: id, Name: "Source condition", Request: AnalysisRequest{DataSource: "hyperliquid", Period: "d1"}}}
	base := LiveAlertsConfig{ConditionIDs: []string{id}}
	inherited := migrateLiveAlertEntries(base, saved, nil, nil)
	if inherited.Alerts[0].DataSource != "hyperliquid" || base.ConditionSources != nil || base.Alerts != nil {
		t.Fatal("migration lost source or mutated caller", inherited, base)
	}
	base.ConditionSources = []LiveAlertConditionSource{{ConditionID: id, Source: "dukascopy"}}
	if got := migrateLiveAlertEntries(base, saved, nil, nil).Alerts[0].DataSource; got != "dukascopy" {
		t.Fatal("explicit Dukascopy override lost to saved Hyperliquid source", got)
	}
	saved[0].Request.DataSource = ""
	base.ConditionSources = nil
	if got := migrateLiveAlertEntries(base, saved, nil, nil).Alerts[0].DataSource; got != "dukascopy" {
		t.Fatal("legacy saved condition no longer defaults to Dukascopy", got)
	}
}

func TestDukascopyAlertNeverBorrowsHyperliquidAnalysisEvidence(t *testing.T) {
	rows := []SavedConditionProfitability{
		{Symbol: "eurusd", Timeframe: "d1", FileName: "hyperliquid_eurusd-d1-2026-01-01-2026-02-01.csv", Matches: 10, TakeProfitHits: 10, ProfitablePercent: 100},
		{Symbol: "eurusd", Timeframe: "d1", FileName: "eurusd-d1-2026-01-01-2026-02-01.csv", Matches: 10, TakeProfitHits: 8, ProfitablePercent: 80},
	}
	saved := SavedAnalysisCondition{Name: "Venue-specific evidence", Request: AnalysisRequest{DataSource: "hyperliquid", Period: "d1"}, Analysis: &SavedConditionAnalysis{Assets: rows}}
	if got := savedConditionEligiblePairs(saved); len(got) != 0 {
		t.Fatal("Hyperliquid condition exposed Dukascopy targets", got)
	}
	if stats, ok := SavedConditionProfitabilityFor(saved, "eurusd", "d1"); ok || stats != nil {
		t.Fatal("Hyperliquid condition exposed borrowed Dukascopy profitability", stats)
	}
	if reason := missingLiveConditionTargets(saved, "D1").Error(); !strings.Contains(reason, "Hyperliquid Analysis") || !strings.Contains(reason, "Dukascopy Analysis matches") {
		t.Fatal("source mismatch was not actionable", reason)
	}
	// A legacy request can contain mixed historical rows from before source
	// selection existed. Use only its real Dukascopy rows, not the stronger HL one.
	saved.Request.DataSource = ""
	pairs := savedConditionEligiblePairs(saved)
	if len(pairs) != 1 || pairs["eurusd/d1"].TakeProfitHits != 8 {
		t.Fatal("mixed legacy evidence borrowed a foreign venue's stronger ratio", pairs)
	}
	if stats, ok := SavedConditionProfitabilityFor(saved, "eurusd", "d1"); !ok || stats.TakeProfitHits != 8 {
		t.Fatal("single-target profitability borrowed a foreign venue", stats, ok)
	}
}
