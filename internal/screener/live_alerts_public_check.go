package screener

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Public sources use the same saved evaluator and durable claims, with their
// own historical inputs. Historical profitability from another venue is never
// assigned to these assets.
func (l *LiveAlerts) checkPublicCycle(ctx context.Context, config LiveAlertsConfig, source string, asOf int64, scheduled bool) (cycleErr error) {
	conditions, err := l.selectedConditions(config)
	if err != nil {
		return err
	}
	allowedAssets := map[string]map[LiveAlertAssetRef]bool{}
	for _, saved := range conditions {
		allowedAssets[saved.ID] = liveAlertSelectedAssets(saved, source)
	}
	periods := map[string][]string{}
	selected := []SavedAnalysisCondition{}
	for _, saved := range conditions {
		for _, period := range savedLiveAlertPeriods(saved) {
			if !liveAlertsScopeAllows(config, saved.ID, period) || (scheduled && !liveAlertConditionDueAt(saved, period, time.UnixMilli(asOf))) {
				continue
			}
			periods[saved.ID] = append(periods[saved.ID], period)
		}
		if len(periods[saved.ID]) > 0 {
			selected = append(selected, saved)
		}
	}
	conditions = selected
	if len(conditions) == 0 {
		return nil
	}
	config.ConditionIDs = nil
	for _, saved := range conditions {
		config.ConditionIDs = append(config.ConditionIDs, saved.ID)
	}
	m := l.manager
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("screener is closed")
	}
	if m.active || m.pendingLocked() || m.enrichmentActive || m.refreshActive || m.analysisActiveLocked() || m.liveActive {
		m.mu.Unlock()
		return errLiveAlertsBusy
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.liveActive, m.liveCancel = true, cancel
	m.refreshActive, m.refreshCancel, m.refreshDone, m.refreshAsOf = true, cancel, done, asOf
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.liveActive, m.liveCancel = false, nil
		m.refreshActive, m.refreshCancel = false, nil
		close(done)
		m.mu.Unlock()
		m.wg.Done()
	}()
	run, err := l.beginCheckHistory(config, !scheduled)
	if err != nil {
		return err
	}
	defer func() { cycleErr = errors.Join(cycleErr, run.finish(cycleErr)) }()
	l.mu.Lock()
	l.updateCheckProgressLocked("Discovering "+source+" markets", 0, "")
	l.mu.Unlock()
	batch, prepareErr := l.preparePublicAlertData(ctx, source, conditions, periods, asOf)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(batch.Assets) == 0 {
		if prepareErr != nil {
			return prepareErr
		}
		return fmt.Errorf("%s returned no tradable spot or perpetual assets", source)
	}
	work, err := os.MkdirTemp(m.profile, ".screener-public-analysis-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	failures := []error{}
	if prepareErr != nil {
		failures = append(failures, prepareErr)
	}
	if failure := batch.Failures["catalog"]; failure != nil {
		failures = append(failures, failure)
		for _, saved := range conditions {
			run.failed(saved.ID, failure)
		}
	}
	sort.Slice(batch.Assets, func(i, j int) bool { return batch.Assets[i].Symbol < batch.Assets[j].Symbol })
	for index, asset := range batch.Assets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		l.mu.Lock()
		l.updateCheckProgressLocked("Checking latest closed public bars", 70+30*float64(index)/float64(max(1, len(batch.Assets))), source+" "+asset.Market+" "+asset.NativeID)
		l.mu.Unlock()
		a := &analysisState{directory: batch.Directory, work: work, series: map[string]*analysisSeries{}, cache: newAnalysisRowCache(), asOf: asOf}
		series := map[string]*analysisSeries{}
		indexFailures := map[string]error{}
		keys := []string{}
		for pair := range batch.Files {
			if strings.HasPrefix(pair, asset.Symbol+"/") {
				keys = append(keys, pair)
			}
		}
		sort.Strings(keys)
		for _, pair := range keys {
			name := batch.Files[pair]
			id := dataFileID(batch.Directory, name)
			s, e := indexAnalysisCSV(ctx, batch.Directory, name, filepath.Join(work, id+".rows"), func(float64) {})
			if e != nil {
				indexFailures[pair] = e
				continue
			}
			s.cache = a.cache
			s.metadata.Description = asset.Name
			s.metadata.Category = source + " " + asset.Market
			a.series[id] = s
			series[pair] = s
		}
		for _, saved := range conditions {
			if allowed := allowedAssets[saved.ID]; allowed != nil && !allowed[LiveAlertAssetRef{Market: asset.Market, Symbol: asset.NativeID}] {
				continue
			}
			for _, period := range periods[saved.ID] {
				pair := asset.Symbol + "/" + period
				run.expectPair(saved.ID, asset.Symbol, period)
				failure := batch.Failures[pair]
				if failure == nil {
					failure = indexFailures[pair]
				}
				s := series[pair]
				if failure == nil && s == nil {
					failure = fmt.Errorf("required %s %s history is unavailable", asset.NativeID, period)
				}
				if failure != nil {
					e := fmt.Errorf("%s %s %s, %s: %w", source, asset.NativeID, period, saved.Name, failure)
					run.skipped(saved.ID, asset.Symbol, e)
					failures = append(failures, e)
					continue
				}
				closed, e := s.closedCount(asOf)
				if e != nil {
					return e
				}
				if closed == 0 {
					failure = errLiveAlertsPendingBar
				} else {
					latest, e := s.row(closed - 1)
					if e != nil {
						return e
					}
					if scheduled {
						interval, _, _ := liveAlertCalendarPeriod(saved, period)
						expected := liveAlertPeriodStart(time.UnixMilli(asOf), period).Add(-interval).UnixMilli()
						if latest.timestamp != expected {
							failure = errLiveAlertsPendingBar
						}
					}
					if failure == nil {
						key := livePublicClaimKey(saved.ID, source, asset.Symbol, period)
						l.mu.Lock()
						previous, seen := l.state.Watermarks[key]
						l.mu.Unlock()
						if seen && latest.timestamp <= previous {
							run.processedPair(saved.ID, asset.Symbol, period)
							continue
						}
						a.request = copyAnalysisRequest(saved.Request)
						a.request.Period, a.request.Categories = "all", nil
						run.scanned(saved.ID, asset.Symbol)
						matched, e := evaluateLiveAlertLatest(ctx, a, s, closed)
						if e != nil {
							failure = e
						} else {
							var alert *LiveAlert
							if matched {
								value, e := liveAlertFromMatch(saved, s, closed-1, latest)
								if e != nil {
									failure = e
								} else {
									value.Symbol, value.AssetName, value.DataSource, value.Market, value.Profitability = asset.NativeID, asset.Name, source, asset.Market, nil
									alert = &value
								}
							}
							if failure == nil {
								claimed, e := l.claimCurrentPublicCondition(ctx, saved, source, key, latest.timestamp, alert)
								if e != nil {
									return e
								}
								if !claimed {
									failure = fmt.Errorf("%s changed during its check; obsolete public result was not claimed", saved.Name)
								} else {
									if alert != nil {
										if e = l.deliver(ctx, *alert); e != nil {
											run.failed(saved.ID, e)
											if !recoverableLiveAlertDelivery(ctx, e) {
												return joinLiveAlertErrors(append(failures, e)...)
											}
											failures = append(failures, e)
										}
									}
									run.processedPair(saved.ID, asset.Symbol, period)
								}
							}
						}
					}
				}
				if failure != nil {
					e := fmt.Errorf("%s %s %s, %s: %w", source, asset.NativeID, period, saved.Name, failure)
					run.skipped(saved.ID, asset.Symbol, e)
					failures = append(failures, e)
				}
			}
		}
		l.mu.Lock()
		if l.watermarksDirty {
			err = writeState(l.path, l.state)
			if err == nil {
				l.watermarksDirty = false
			}
		}
		l.mu.Unlock()
		if err != nil {
			return &liveAlertPersistenceError{fmt.Errorf("save processed public bars: %w", err)}
		}
	}
	return finishedLiveAlertScan(failures...)
}

func (l *LiveAlerts) claimCurrentPublicCondition(ctx context.Context, evaluated SavedAnalysisCondition, source, key string, timestamp int64, alert *LiveAlert) (bool, error) {
	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()
	saved, err := l.manager.readSavedConditions()
	if err != nil {
		return false, err
	}
	currentConditions, revision, valid := l.currentLiveAlertConditions(saved, evaluated)
	if !valid {
		return false, nil
	}
	for _, current := range currentConditions {
		if current.ID == evaluated.ID {
			if !sameAnalysisProfitabilityRequest(current.Request, evaluated.Request) {
				return false, nil
			}
			return true, l.claimSourceRevision(ctx, source, evaluated.ID, key, timestamp, alert, &revision)
		}
	}
	return false, nil
}
