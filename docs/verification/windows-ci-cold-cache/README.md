# Windows CI missing-module fix — 2026-10-20

The [reported Windows job](https://github.com/neodix42/charting/actions/runs/39394397933/job/118533456870) failed on commit `f4fb9ee3cbf71a9dd6304a588be1ae37a6bf312c` after installing the Linux Wails CLI:

```text
2026-10-20T08:35:32.4299510Z Windows build failed: The pinned github.com/wailsapp/go-webview2@v1.0.22 source is unavailable; download the project Go dependencies first
```

Its metadata lookup selected the pinned Windows-only module but returned no extracted source directory. The helper now explicitly runs `go mod download -json github.com/wailsapp/go-webview2@v1.0.22` when needed, validates its identity, and retains the exact source SHA-256 check before applying the temporary English-language overlay. Normal Go checksum verification remains enabled. Existing cached builds reuse their source without another download command. Download failures and timeouts are reported without preparing a partial overlay.

Verification:

- [cold-build.json](cold-build.json) and [build.log](build.log): a complete real `./win-build.sh` succeeds with a genuinely empty, isolated temporary `GOMODCACHE`. Before the build, actual `go list -m -json` reproduces missing `Dir`; afterward the pinned source exists and its hash matches. Original `go.mod`, `go.sum` and generated bindings remain byte-identical. The CLI executable and pinned Microsoft WebView2 runtime were reused from the existing project-local tools/cache; only the Go dependency cache was empty. Host: Ubuntu 26.04.1 x64.
- [deterministic.log](deterministic.log): `env PATH=/usr/bin:/bin scripts/test.sh` passes 76 Python tests, Go race/vet/bindings checks, frontend typecheck/lint, 68 frontend unit tests and production build. The new real-Go regression uses an isolated local `file://` proxy and separate checksum-seed/cache directories; default tests make no public network requests. Mocked cases cover missing module metadata, invalid downloads, offline failure and timeout. Only the synthetic local-proxy fixture disables the checksum service; production download verification is unchanged.
- [package.json](package.json): the rebuilt root Windows ZIP is valid AMD64 GUI PE32+, all 187 packaged file checksums pass, and four English locale packs remain. Root ZIP checksum passes, and release staging produces a byte-identical stable download alias.

The changed workflow has not been run on GitHub. Commit and push this fix to `main` to trigger a run using the updated helper; rerunning the original commit uses its old script. Native Windows GUI execution remains unverified on this Ubuntu host. No remote tags, releases, workflow reruns or repository writes were performed.

Reference: Go's official [module download command](https://go.dev/ref/mod#go-mod-download) explicitly downloads named module source into the cache and returns its directory and checksum information.
