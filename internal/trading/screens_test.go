package trading

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func screenFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 83, 29))
	for y := 0; y < 29; y++ {
		for x := 0; x < 83; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 2), uint8(y * 3), 37, 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		t.Fatal(err)
	}
	return output.Bytes(), "data:image/png;base64," + base64.StdEncoding.EncodeToString(output.Bytes())
}

func TestTradeScreensPreserveOriginalsAssociationAndNotesAcrossReopen(t *testing.T) {
	profile := t.TempDir()
	store := NewEntryConditions(profile)
	trade := Trade{ID: "dukascopy:" + strings.Repeat("a", 32), Provider: "dukascopy", Environment: "demo", Account: "account-scope", OrderID: "ORDER-42", Instrument: "EURUSD", Market: "forex", Direction: "long", OpenDate: 1700000000000}
	// Migration must not rewrite or discard the existing v1 note.
	legacy := `{"version":1,"trades":{"` + trade.ID + `":{"entryCondition":"Range breakout"}}}`
	if err := os.WriteFile(store.path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	original, url := screenFixture(t)
	first, err := store.AddScreen(trade, "../../first.png", url)
	if err != nil || first.TradeID != trade.ID || first.FileName != "first.png" || first.Width != 83 || first.Height != 29 || first.OrderID != "ORDER-42" || first.Instrument != "EURUSD" || first.Bytes != int64(len(original)) {
		t.Fatal(first, err)
	}
	second, err := store.AddScreen(trade, "second.png", url)
	if err != nil || second.ID == first.ID {
		t.Fatal(second, err)
	}
	if _, err = store.Set(trade.ID, ""); err != nil {
		t.Fatal(err)
	}
	store = NewEntryConditions(profile)
	metadata, err := store.MetadataSnapshot()
	if err != nil || len(metadata[trade.ID].Screens) != 2 || metadata[trade.ID].EntryCondition != "" {
		t.Fatal(metadata, err)
	}
	for _, screen := range metadata[trade.ID].Screens {
		p := filepath.Join(profile, "trade-screens", "dukascopy", strings.Repeat("a", 32), screen.ID+".png")
		saved, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(saved, original) {
			t.Fatal("image was resized, re-encoded or lost", err)
		}
		if got, err := store.ReadScreen(trade.ID, screen.ID); err != nil || got != url {
			t.Fatal("original image did not survive restart", err)
		}
		if _, err := store.ReadScreen("hyperliquid:"+strings.Repeat("b", 32), screen.ID); err == nil {
			t.Fatal("image crossed trade boundaries")
		}
	}
	if _, err = store.Set(trade.ID, "Range breakout"); err != nil {
		t.Fatal(err)
	}
	page, err := store.QueryScreens(TradeScreenQuery{Provider: "dukascopy", Instrument: "EURUSD", OrderID: "ORDER-42", Environment: "demo", EntryCondition: "BREAKOUT", Limit: 1})
	if err != nil || page.Total != 2 || len(page.Screens) != 1 {
		t.Fatal(page, err)
	}
	page, err = store.QueryScreens(TradeScreenQuery{TradeID: trade.ID, Offset: 1, Limit: 10, From: first.CreatedAt - 1, To: second.CreatedAt + 1})
	if err != nil || page.Total != 2 || len(page.Screens) != 1 {
		t.Fatal(page, err)
	}
	page, err = store.QueryScreens(TradeScreenQuery{Provider: "hyperliquid"})
	if err != nil || page.Total != 0 {
		t.Fatal(page, err)
	}
	var data tradeAnnotations
	if err := ReadJournal(store.path, &data); err != nil || data.Version != 2 {
		t.Fatal("v1 annotations were not migrated on edit", err)
	}
}

func TestTradeScreensRejectInvalidInputAndDetectChangedOriginal(t *testing.T) {
	store := NewEntryConditions(t.TempDir())
	trade := Trade{ID: "hyperliquid:" + strings.Repeat("c", 32)}
	_, url := screenFixture(t)
	for _, bad := range []string{"https://example.invalid/image.png", "data:image/png;base64,broken", "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("not an image"))} {
		if _, err := store.AddScreen(trade, "test.png", bad); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Fatal("rejected input created annotations")
	}
	screen, err := store.AddScreen(trade, "screen.png", url)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(filepath.Dir(store.path), "trade-screens", "hyperliquid", strings.Repeat("c", 32), screen.ID+".png")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err = os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadScreen(trade.ID, screen.ID); err == nil {
		t.Fatal("changed image passed its integrity check")
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(after, data) {
		t.Fatal("failed preview modified the file")
	}
}

func TestTradeScreenMetadataFailureRemovesOnlyUncommittedImage(t *testing.T) {
	store := NewEntryConditions(t.TempDir())
	data := tradeAnnotations{Version: 2, Trades: map[string]tradeAnnotation{}}
	empty, _ := json.Marshal(data)
	data.Trades["dukascopy:"+strings.Repeat("0", 32)] = tradeAnnotation{EntryCondition: strings.Repeat("x", 100)}
	one, _ := json.Marshal(data)
	// Fill with real notes, not whitespace: serialization must retain the
	// payload, leaving too little room for the new image's metadata.
	count := (2*1024*1024 - 100 - len(empty) + 1) / (len(one) - len(empty) + 1)
	for i := 1; i < count; i++ {
		data.Trades[fmt.Sprintf("dukascopy:%032x", i)] = tradeAnnotation{EntryCondition: strings.Repeat("x", 100)}
	}
	raw, err := json.Marshal(data)
	if err != nil || len(raw) > 2*1024*1024 || len(raw) < 2*1024*1024-300 {
		t.Fatal("fixture did not approach the metadata limit", len(raw), err)
	}
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MetadataSnapshot(); err != nil {
		t.Fatal("fixture must be readable before attaching an image", err)
	}
	_, url := screenFixture(t)
	tradeID := "hyperliquid:" + strings.Repeat("f", 32)
	if _, err := store.AddScreen(Trade{ID: tradeID}, "image.png", url); err == nil || !strings.Contains(err.Error(), "exceeds 2 MiB") {
		t.Fatal("metadata save should fail after writing the original image", err)
	}
	if after, _ := os.ReadFile(store.path); !bytes.Equal(raw, after) {
		t.Fatal("failed image save replaced settings")
	}
	directory, err := store.screenDirectory(tradeID, false)
	if err != nil {
		t.Fatal("image directory was never reached", err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatal("uncommitted original was left behind", entries, err)
	}
}
