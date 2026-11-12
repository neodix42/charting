//go:build bindings

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cryptodesk/internal/hyperliquid"
	"cryptodesk/internal/jforex"
	"cryptodesk/internal/storage"
	"cryptodesk/internal/trading"
)

func nativeTradeScreenApp(t *testing.T) (*App, string, []string, []byte, string) {
	t.Helper()
	profile := t.TempDir()
	write := func(directory, id string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(profile, directory, id+".json")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	hyperID, dukaID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	write("hyperliquid-orders", hyperID, map[string]any{"preview": hyperliquid.Preview{ID: hyperID, AccountAddress: "0x" + strings.Repeat("1", 40), Network: "mainnet", ValuationTime: 1000, Request: hyperliquid.OrderRequest{Market: "perpetual", Instrument: "BTC", Side: "buy", Quantity: "2"}}, "result": hyperliquid.OrderResult{Status: "filled", FilledQuantity: "2", AveragePrice: "100", OrderIDs: []int64{101}}})
	write("dukascopy-trade-claims", dukaID, map[string]any{"id": dukaID, "createdAt": 2000, "environment": "demo", "status": "claimed", "request": jforex.TradeRequest{Instrument: "EUR/USD", Side: "sell", Quantity: 1000}})
	local, err := storage.Open(filepath.Join(profile, "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{store: local, hyperliquid: hyperliquid.NewStore(profile), jforex: jforex.New(profile)}
	t.Cleanup(func() { a.closeTrading(); a.jforex.Close(); local.Close() })
	pixels := image.NewNRGBA(image.Rect(0, 0, 37, 19))
	pixels.SetNRGBA(4, 7, color.NRGBA{R: 231, G: 19, B: 84, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, pixels); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	return a, profile, []string{"hyperliquid:" + hyperID, "dukascopy:" + dukaID}, data, "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

func TestTradeScreenNativeDropPreservesOriginalAndProviderAssociation(t *testing.T) {
	a, profile, ids, data, original := nativeTradeScreenApp(t)
	path := filepath.Join(t.TempDir(), "Entry screenshot ü.png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for index, id := range ids {
		provider, localID, _ := splitTradeID(id)
		directory := "hyperliquid-orders"
		if provider == "dukascopy" {
			directory = "dukascopy-trade-claims"
		}
		claimPath := filepath.Join(profile, directory, localID+".json")
		claimBefore, err := os.ReadFile(claimPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.SetTradeEntryCondition(id, "Existing entry note"); err != nil {
			t.Fatal(err)
		}
		screen, err := a.AddTradeScreenFile(id, path)
		if err != nil {
			t.Fatal(err)
		}
		if screen.TradeID != id || screen.Provider != provider || screen.Width != 37 || screen.Height != 19 || screen.Bytes != int64(len(data)) || screen.FileName != filepath.Base(path) || screen.MIMEType != "image/png" {
			t.Fatalf("native drop lost original metadata: %+v", screen)
		}
		if got, err := a.GetTradeScreen(id, screen.ID); err != nil || got != original {
			t.Fatalf("native drop changed original bytes: %v", err)
		}
		if _, err := a.GetTradeScreen(ids[1-index], screen.ID); err == nil {
			t.Fatal("native screen became accessible through another trade")
		}
		metadata, err := a.entryConditionStore().MetadataSnapshot()
		if err != nil || metadata[id].EntryCondition != "Existing entry note" || len(metadata[id].Screens) != 1 {
			t.Fatal("native import changed existing entry annotation", err)
		}
		claimAfter, err := os.ReadFile(claimPath)
		if err != nil || !bytes.Equal(claimBefore, claimAfter) {
			t.Fatal("native image import changed exchange order journal", err)
		}
	}
	page, err := a.QueryTradeScreens(trading.TradeScreenQuery{})
	if err != nil || page.Total != 2 {
		t.Fatal("native files were not independently associated", page, err)
	}
}

func TestTradeScreenNativeDropRejectsInvalidFilesWithoutChangingMetadata(t *testing.T) {
	a, profile, ids, _, _ := nativeTradeScreenApp(t)
	if _, err := a.SetTradeEntryCondition(ids[0], "Keep this note"); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(profile, "trades.json")
	before, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	empty, text, oversized := filepath.Join(dir, "empty.png"), filepath.Join(dir, "not-image.png"), filepath.Join(dir, "large.png")
	for path, data := range map[string][]byte{empty: {}, text: []byte("not an image")} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(trading.MaxTradeScreenBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	for name, path := range map[string]string{"relative": "screen.png", "missing": filepath.Join(dir, "missing.png"), "directory": dir, "empty": empty, "text": text, "oversized": oversized} {
		t.Run(name, func(t *testing.T) {
			if _, err := a.AddTradeScreenFile(ids[0], path); err == nil {
				t.Fatal("invalid native drop was accepted")
			}
			after, err := os.ReadFile(metadataPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed native drop changed existing metadata", err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(profile, "trade-screens")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed imports created an image directory", err)
	}
}

func TestTradeScreenNativePasteChecksOwnershipBeforeClipboardAndSavesOriginal(t *testing.T) {
	a, _, ids, data, original := nativeTradeScreenApp(t)
	called := false
	read := func(ctx context.Context) (string, error) {
		called = true
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
			t.Fatal("native clipboard read has no bounded deadline")
		}
		return original, nil
	}
	unknown := "hyperliquid:" + strings.Repeat("f", 32)
	if _, err := a.pasteTradeScreen(unknown, read); err == nil || called {
		t.Fatal("untracked trade read the native clipboard", err)
	}
	if _, err := a.AddTradeScreenFile(unknown, "relative.png"); err == nil || !strings.Contains(err.Error(), "locally recorded") {
		t.Fatal("file validation happened before trade ownership validation", err)
	}
	screen, err := a.pasteTradeScreen(ids[1], read)
	if err != nil || !called || screen.TradeID != ids[1] || screen.Width != 37 || screen.Height != 19 || screen.Bytes != int64(len(data)) || screen.FileName != "Screenshot.png" {
		t.Fatal("native paste lost original or trade identity", screen, err)
	}
	if got, err := a.GetTradeScreen(ids[1], screen.ID); err != nil || got != original {
		t.Fatal("native clipboard image was altered", err)
	}
}

func TestTradeScreenNativePasteErrorCancellationAndShutdownDoNotCommit(t *testing.T) {
	a, profile, ids, _, original := nativeTradeScreenApp(t)
	if _, err := a.SetTradeEntryCondition(ids[0], "Preserved"); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(profile, "trades.json")
	before, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("Clipboard image unavailable")
	if _, err := a.pasteTradeScreen(ids[0], func(context.Context) (string, error) { return "", failure }); !errors.Is(err, failure) {
		t.Fatal("native clipboard failure was not preserved", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	if _, err := a.pasteTradeScreen(ids[0], func(context.Context) (string, error) { cancel(); return original, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled clipboard read committed an image", err)
	}
	if after, err := os.ReadFile(metadataPath); err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed or canceled paste changed metadata", err)
	}
	a.closeTrading()
	called := false
	if _, err := a.pasteTradeScreen(ids[0], func(context.Context) (string, error) { called = true; return original, nil }); err == nil || called {
		t.Fatal("clipboard read began after shutdown", err)
	}
	if _, err := a.AddTradeScreenFile(ids[0], filepath.Join(profile, "image.png")); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatal("native file import began after shutdown", err)
	}
}
