package hyperliquid

import (
	"encoding/hex"
	"testing"
)

// Independently generated with the official Python SDK at commit
// 2fdb18f9517675ea03695a0962bd19eece9c83f0, using action_hash(action, None,
// nonce, expires_after) and sign_l1_action(wallet, action, None, nonce,
// expires_after, is_mainnet). Only the public test key from signing_test.py
// was used. The SDK was installed in an isolated temporary environment.
// https://github.com/hyperliquid-dex/hyperliquid-python-sdk/blob/2fdb18f9517675ea03695a0962bd19eece9c83f0/hyperliquid/utils/signing.py
func TestOfficialPythonSDKAgentTransferSigningVector(t *testing.T) {
	const nonce, expires = uint64(1794240000000), uint64(1794240060000)
	p := TransferPreview{AccountAddress: testAccount, SourceDex: "", DestinationDex: "xyz", Coin: "USDC", TokenID: transferUSDC, Amount: "12.34"}
	action := transferWire(p, nonce)
	if got := hex.EncodeToString(actionHash(action, nonce, expires)); got != "3679ecbdcf9515225e2cce563c36e0e37cb93521990fa85e5870701d92bcd677" {
		t.Fatal("official Python SDK action hash differs", got)
	}
	key, err := keyFromString(testPrivate)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		mainnet bool
		want    signature
	}{
		{true, signature{"0x144895054188149db36d2c5f73f7a1ee2d98bb963f6bace8cb87df76ecf80bf9", "0x25fa4d901f2ad729f8c3de37e2c1819450ac6c50917ab62afbbc6093a97a871b", 27}},
		{false, signature{"0xafe4f3069a8a613eb97a6a8c5d411b0202245b112b30b3589ffb5e3efabf7f30", "0x5fd49476a2126458f00ca30c0c38aac78746128b0a30314fcbb6eedcf309c8d9", 27}},
	} {
		if got := signAction(key, action, nonce, expires, v.mainnet); got != v.want {
			t.Fatalf("official Python SDK transfer signature differs (mainnet=%v): %+v", v.mainnet, got)
		}
	}
}
