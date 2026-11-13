package screener

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

func (l *LiveAlerts) publicAlertStep(ctx context.Context, phase, asset string) {
	l.mu.Lock()
	l.updateCheckProgressContextLocked(ctx, phase, -1, asset)
	l.mu.Unlock()
}

type publicAlertPreparationStep struct{ phase, asset, displayName string }

// A row can have several assets at different stages. Show one active download
// instead of repeating every worker's status, removing finished workers so an
// old download cannot mask remaining enrichment or cache waits. Only completed
// series advance percentage.
type publicAlertPreparationProgress struct {
	mu               sync.Mutex
	live             *LiveAlerts
	ctx              context.Context
	total, completed int
	active           map[int]publicAlertPreparationStep
}

func newPublicAlertPreparationProgress(live *LiveAlerts, ctx context.Context, total int) *publicAlertPreparationProgress {
	return &publicAlertPreparationProgress{live: live, ctx: ctx, total: total, active: map[int]publicAlertPreparationStep{}}
}
func (p *publicAlertPreparationProgress) step(worker int, phase, asset string, displayNames ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	name := asset
	if len(displayNames) > 0 {
		name = displayNames[0]
	}
	p.active[worker] = publicAlertPreparationStep{phase: phase, asset: asset, displayName: name}
	p.publishLocked()
}

func publicAlertProgressName(asset publicAlertAsset) string {
	if name := strings.TrimSpace(asset.Name); name != "" {
		return name
	}
	return strings.TrimSpace(asset.Symbol)
}
func (p *publicAlertPreparationProgress) complete(worker int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completed++
	delete(p.active, worker)
	p.publishLocked()
}
func (p *publicAlertPreparationProgress) remove(worker int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.active, worker)
	p.publishLocked()
}
func (p *publicAlertPreparationProgress) publishLocked() {
	ids := make([]int, 0, len(p.active))
	for id := range p.active {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	// Keep the selected download stable while it is active. Worker IDs provide
	// a deterministic choice independent of goroutine report order.
	for _, id := range ids {
		step := p.active[id]
		if strings.HasPrefix(step.phase, "Downloading ") {
			p.publishRowLocked(strings.TrimSpace("Downloading "+step.displayName), "")
			return
		}
	}
	phases := []string{}
	counts := map[string]int{}
	assets := []string{}
	for _, id := range ids {
		step := p.active[id]
		if counts[step.phase] == 0 {
			phases = append(phases, step.phase)
		}
		counts[step.phase]++
		if step.asset != "" {
			assets = append(assets, step.asset+": "+step.phase)
		}
	}
	for index, phase := range phases {
		if counts[phase] > 1 {
			phases[index] = fmt.Sprintf("%s (%d)", phase, counts[phase])
		}
	}
	phase := strings.Join(phases, " · ")
	if phase == "" {
		phase = "Finishing public data preparation"
	}
	p.publishRowLocked(phase, strings.Join(assets, " · "))
}

func (p *publicAlertPreparationProgress) publishRowLocked(phase, asset string) {
	p.live.mu.Lock()
	p.live.updateCheckProgressContextLocked(p.ctx, phase, 70*float64(p.completed)/float64(max(1, p.total)), asset)
	p.live.mu.Unlock()
}
