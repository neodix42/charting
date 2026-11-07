package screener

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Each public check owns this cache while Manager.mu protects reads. Verify
// the complete saved file's contents before EVERY claim; only skip repeated
// JSON decoding, profitability validation and evidence copies when unchanged.
// Public claims need rule identity/requests, never another venue's evidence.
type publicAlertRuleGuard struct {
	digest [32]byte
	valid  bool
	rules  []SavedAnalysisCondition
}

func (g *publicAlertRuleGuard) read(m *Manager) ([]SavedAnalysisCondition, error) {
	path := filepath.Join(m.profile, "screener-saved-conditions.json")
	data, err := publicAlertReadBounded(path, savedConditionsBytes)
	if errors.Is(err, os.ErrNotExist) {
		g.valid = false
		return nil, nil
	}
	if err != nil {
		g.valid = false
		return nil, err
	}
	digest := sha256.Sum256(data)
	if g.valid && digest == g.digest {
		return g.rules, nil
	}
	g.valid = false
	var saved savedAnalysisConditions
	if err = decodeState(data, &saved); err != nil {
		return nil, fmt.Errorf("read saved conditions (original state preserved): %w", err)
	}
	rules, err := validatedSavedConditions(saved)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		rules[i].Analysis = nil
	}
	g.digest, g.rules, g.valid = digest, rules, true
	return rules, nil
}

// Include the actual installed recipe/calculator code and dependency pins so
// an application/runtime update cannot reuse obsolete derived values.
func publicAlertRuntimeFingerprint(runtime workerRuntime) string {
	h := sha256.New()
	h.Write([]byte(publicAlertWorkerScript))
	root := filepath.Dir(runtime.worker)
	for _, name := range []string{"worker.cjs", "enrich-worker.cjs", "refresh-worker.cjs", "package-lock.json"} {
		data, err := publicAlertReadBounded(filepath.Join(root, name), 8<<20)
		if err != nil {
			return ""
		}
		h.Write(data)
	}
	data, err := publicAlertReadBounded(filepath.Join(root, "indicators.json"), 1<<20)
	if err != nil {
		data, err = publicAlertReadBounded(filepath.Join(root, "..", "internal", "screener", "indicators.json"), 1<<20)
	}
	if err != nil {
		return ""
	}
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func publicAlertEnrichmentKey(input []byte, plan publicAlertPlan, runtime string) string {
	if runtime == "" {
		return ""
	}
	h := sha256.New()
	h.Write(input)
	encoded, _ := json.Marshal(plan)
	h.Write(encoded)
	h.Write([]byte(runtime))
	return hex.EncodeToString(h.Sum(nil))
}

type publicAlertEnrichedState struct {
	Key      string `json:"key"`
	Checksum string `json:"checksum"`
}

func publicAlertReuseEnriched(metadata, output, key string) bool {
	if key == "" {
		return false
	}
	encoded, err := publicAlertReadBounded(metadata, 1<<20)
	var state publicAlertEnrichedState
	if err != nil || json.Unmarshal(encoded, &state) != nil || state.Key != key {
		return false
	}
	data, err := publicAlertReadBounded(output, 128<<20)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(data)
	return state.Checksum == hex.EncodeToString(digest[:])
}

func publicAlertSaveEnriched(metadata, key string, output []byte) error {
	if key == "" {
		return nil
	}
	digest := sha256.Sum256(output)
	encoded, _ := json.Marshal(publicAlertEnrichedState{Key: key, Checksum: hex.EncodeToString(digest[:])})
	return publicAlertAtomicWrite(metadata, append(encoded, '\n'))
}
