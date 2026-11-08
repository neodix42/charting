package hyperliquid

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// Ordered maps are essential: Hyperliquid signs MessagePack in insertion order.
// Wire fields/order follow the official hyperliquid-python-sdk signing.py.
type field struct {
	key   string
	value any
}
type wire []field

func (w wire) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range w {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(f.key)
		v, err := json.Marshal(f.value)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
func pack(v any) []byte {
	var b bytes.Buffer
	var put func(any)
	str := func(s string) {
		n := len(s)
		if n < 32 {
			b.WriteByte(byte(0xa0 + n))
		} else if n < 256 {
			b.Write([]byte{0xd9, byte(n)})
		} else {
			b.WriteByte(0xda)
			_ = binary.Write(&b, binary.BigEndian, uint16(n))
		}
		b.WriteString(s)
	}
	put = func(v any) {
		switch x := v.(type) {
		case wire:
			if len(x) >= 16 {
				panic("bounded Hyperliquid map exceeded")
			}
			b.WriteByte(byte(0x80 + len(x)))
			for _, f := range x {
				str(f.key)
				put(f.value)
			}
		case []wire:
			if len(x) >= 16 {
				panic("bounded Hyperliquid order batch exceeded")
			}
			b.WriteByte(byte(0x90 + len(x)))
			for _, item := range x {
				put(item)
			}
		case string:
			str(x)
		case bool:
			if x {
				b.WriteByte(0xc3)
			} else {
				b.WriteByte(0xc2)
			}
		case int:
			put(uint64(x))
		case uint64:
			switch {
			case x < 128:
				b.WriteByte(byte(x))
			case x <= 255:
				b.Write([]byte{0xcc, byte(x)})
			case x <= 65535:
				b.WriteByte(0xcd)
				_ = binary.Write(&b, binary.BigEndian, uint16(x))
			case x <= 4294967295:
				b.WriteByte(0xce)
				_ = binary.Write(&b, binary.BigEndian, uint32(x))
			default:
				b.WriteByte(0xcf)
				_ = binary.Write(&b, binary.BigEndian, x)
			}
		default:
			panic(fmt.Sprintf("unexpected Hyperliquid signing type %T", v))
		}
	}
	put(v)
	return b.Bytes()
}
func keccak(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		_, _ = h.Write(p)
	}
	return h.Sum(nil)
}
func keyFromString(value string) (*secp.PrivateKey, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(raw) != 32 {
		return nil, errors.New("API wallet private key must contain exactly 64 hexadecimal digits")
	}
	var scalar secp.ModNScalar
	if scalar.SetByteSlice(raw) || scalar.IsZero() {
		return nil, errors.New("invalid API wallet private key")
	}
	return secp.NewPrivateKey(&scalar), nil
}
func addressFor(key *secp.PrivateKey) string {
	return "0x" + hex.EncodeToString(keccak(key.PubKey().SerializeUncompressed()[1:])[12:])
}

type signature struct {
	R string `json:"r"`
	S string `json:"s"`
	V int    `json:"v"`
}

func actionHash(action wire, nonce uint64, expires uint64) []byte {
	data := pack(action)
	data = binary.BigEndian.AppendUint64(data, nonce)
	data = append(data, 0)
	if expires != 0 {
		data = append(data, 0)
		data = binary.BigEndian.AppendUint64(data, expires)
	}
	return keccak(data)
}
func signAction(key *secp.PrivateKey, action wire, nonce, expires uint64, mainnet bool) signature {
	chain := make([]byte, 32)
	binary.BigEndian.PutUint64(chain[24:], 1337)
	domain := keccak(keccak([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")), keccak([]byte("Exchange")), keccak([]byte("1")), chain, make([]byte, 32))
	source := "a"
	if !mainnet {
		source = "b"
	}
	message := keccak(keccak([]byte("Agent(string source,bytes32 connectionId)")), keccak([]byte(source)), actionHash(action, nonce, expires))
	signed := ecdsa.SignCompact(key, keccak([]byte{0x19, 0x01}, domain, message), false)
	return signature{R: "0x" + strings.TrimLeft(hex.EncodeToString(signed[1:33]), "0"), S: "0x" + strings.TrimLeft(hex.EncodeToString(signed[33:]), "0"), V: int(signed[0])}
}
