package hyperliquid

import (
	"encoding/hex"
	"testing"
)

// Exact publicly published vectors from the official SDK, not a parallel local
// implementation: hyperliquid-dex/hyperliquid-python-sdk/tests/signing_test.py.
func TestOfficialSDKSigningVectors(t *testing.T) {
	key, err := keyFromString("0x0123456789012345678901234567890123456789012345678901234567890123")
	if err != nil {
		t.Fatal(err)
	}
	action := wire{{"type", "dummy"}, {"num", uint64(100000000000)}}
	for _, v := range []struct {
		main bool
		want signature
	}{{true, signature{"0x53749d5b30552aeb2fca34b530185976545bb22d0b3ce6f62e31be961a59298", "0x755c40ba9bf05223521753995abb2f73ab3229be8ec921f350cb447e384d8ed8", 27}}, {false, signature{"0x542af61ef1f429707e3c76c5293c80d01f74ef853e34b76efffcb57e574f9510", "0x17b8b32f086e8cdede991f1e2c529f5dd5297cbe8128500e00cbaf766204a613", 28}}} {
		if got := signAction(key, action, 0, 0, v.main); got != v.want {
			t.Fatalf("official vector mismatch: mainnet=%v got=%+v", v.main, got)
		}
	}
	orders := []wire{orderWire(instrument{asset: 4}, true, "1670.1", "0.0147", false, wire{{"limit", wire{{"tif", "Ioc"}}}}, "")}
	hash := actionHash(wire{{"type", "order"}, {"orders", orders}, {"grouping", "na"}}, 1677777606040, 0)
	if got := hex.EncodeToString(hash); got != "0fcbeda5ae3c4950a548021552a4fea2226858c4453571bf3f24ba017eac2908" {
		t.Fatal("official MessagePack hash mismatch", got)
	}
	order := wire{{"type", "order"}, {"orders", []wire{orderWire(instrument{asset: 1}, true, "100", "100", false, wire{{"limit", wire{{"tif", "Gtc"}}}}, "")}}, {"grouping", "na"}}
	want := signature{"0xd65369825a9df5d80099e513cce430311d7d26ddf477f5b3a33d2806b100d78e", "0x2b54116ff64054968aa237c20ca9ff68000f977c93289157748a3162b6ea940e", 28}
	if got := signAction(key, order, 0, 0, true); got != want {
		t.Fatal("official order signature mismatch", got)
	}
}
