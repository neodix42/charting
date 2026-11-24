package screener

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type providerSessionKey struct{}

type providerSession struct {
	Directory  string              `json:"directory"`
	TokenCache string              `json:"tokenCache"`
	RateLimit  bool                `json:"rateLimit"`
	SDK        *providerSDKSession `json:"sdk,omitempty"`
	limiter    *providerLimiter
	history    DukascopyHistoryProvider
}

// Cache the supported upstream browser session in the machine-local profile,
// outside disposable CSV scratch directories. Batch gates never survive restart.
func withProviderSession(ctx context.Context, profile string) (context.Context, func(), error) {
	return withProviderSessionWithHistory(ctx, profile, nil)
}
func withProviderSessionWithHistory(ctx context.Context, profile string, provider DukascopyHistoryProvider) (context.Context, func(), error) {
	directory := filepath.Join(profile, "screener-provider")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ctx, nil, err
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() {
		return ctx, nil, fmt.Errorf("Screener provider session folder must be a real directory")
	}
	batch, err := os.MkdirTemp(directory, "batch-")
	if err != nil {
		return ctx, nil, err
	}
	limiter, err := newProviderLimiter(filepath.Join(directory, "rate-limit.json"))
	if err != nil {
		_ = os.RemoveAll(batch)
		return ctx, nil, err
	}
	state := providerSession{Directory: batch, TokenCache: filepath.Join(directory, "waf-token.json"), RateLimit: true, limiter: limiter}
	if provider != nil {
		source, configured := provider.HistoricalSource()
		if configured {
			if !validSDKSourceKey(source) {
				_ = os.RemoveAll(batch)
				return ctx, nil, fmt.Errorf("invalid Dukascopy SDK history source identity")
			}
			state.SDK, state.history = &providerSDKSession{SourceKey: source}, provider
		}
	}
	return context.WithValue(ctx, providerSessionKey{}, &state), func() { _ = os.RemoveAll(batch) }, nil
}

func sessionFromContext(ctx context.Context) *providerSession {
	state, _ := ctx.Value(providerSessionKey{}).(*providerSession)
	return state
}
