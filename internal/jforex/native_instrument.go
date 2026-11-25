package jforex

// NativeInstrumentAvailable never starts a session. It checks the configured
// account's catalog, so a public calculated/history entry cannot enable trading.
func (m *Manager) NativeInstrumentAvailable(instrument, market string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.loadInstrumentCatalogLocked() != nil || m.catalog == nil {
		return false
	}
	for _, item := range m.catalog.Items {
		if item.NativeID == instrument && item.Market == market && item.Availability == "available" {
			return true
		}
	}
	return false
}
