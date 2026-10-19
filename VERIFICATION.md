# What is verified in this instruction bundle

The accompanying verifier checks packaged-file integrity, the starter/intermediate/capacity
example counts, key semantic validation failures, synthetic Decimal candle expectations and
local Markdown links. With jsonschema already installed, it also validates all three examples
against Draft 2020-12. Run it before Codex modifies the retained specification files.

```bash
python3 codex-cryptodesk/tools/verify_bundle.py
bash codex-cryptodesk/tools/preflight.sh
```

The verifier is read-only. HASHES.json describes the original bundle: editing STATUS.md or
another specification file during implementation legitimately changes its checksum. Do not
regenerate the original checksum file and present that as proof the original is unchanged.
For app testing follow TEST_PLAN.md, not this bundle tool.

The environment preflight reports existing tools and libraries and performs no installation.
It is not a pass/fail certification of Ubuntu compatibility. Preparing this archive does not
implement CryptoDesk, build a Go executable or test a connection to Binance, Bybit or Coinbase.
