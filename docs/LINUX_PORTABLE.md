# Bundled Linux runtime

Releases contain two distinct Linux x64 archives:

- `cryptodesk-X.Y.Z-linux-amd64.tar.gz`: small native binary; GTK3, WebKitGTK 4.1 and libsoup3 must be installed.
- `cryptodesk-X.Y.Z-linux-amd64-portable.tar.gz`: larger package with the GTK/WebKit runtime and matching helper processes, relevant GIO/GdkPixbuf/GStreamer modules, schemas, icons, dependency libraries, redistribution notices and exact corresponding source archives.

Download from the [latest release](https://github.com/neodix42/charting/releases/latest), verify its `SHA256SUMS`, extract the **whole directory**, and run its root `./cryptodesk` launcher. Keep the `runtime` directory beside it. FUSE and system installation are unnecessary. The ordinary optional user-local installer applies to the small native archive; copying only the bundled package's launcher will not install its runtime.

The launcher scopes library/module paths to this process and starts the real app under `runtime/usr/bin`. It restores the caller's original environment when opening an external browser. It preserves arguments containing spaces and resolves an explicitly relative `--data-dir` against the caller's directory. By default, the same normal per-user workspace is used; this package does not silently create a different workspace. For a profile beside the package, explicitly run `./cryptodesk --data-dir ./data`. Close other instances that use that profile first.

## Compatibility

The bundle removes the GTK/WebKit installation requirement. The host must still supply an x86_64 Linux desktop session (Wayland or X11), compatible glibc, graphics-driver interfaces, fonts, certificates and normal desktop services. glibc, the dynamic loader and graphics-driver-facing libraries are deliberately external. This avoids bundling a libc or GPU driver that conflicts with the host. The minimum glibc, build OS and exact external library names are recorded in `version.json`.

GitHub CI builds on Ubuntu 24.04. Local builds on Ubuntu 26.04 inherit that newer host's glibc requirement and are not advertised as Ubuntu 24.04 binaries. Other distributions, older Ubuntu releases, additional GPU stacks and display servers require their own runtime checks; see [STATUS.md](../STATUS.md) for actual verification. The package is not a universal Linux executable.

The bundled browser does not receive OS browser updates. A new app package is needed to update its WebKit runtime. No browser sandbox is disabled by the launcher or bundler.

## Build

```bash
./portable-build.sh
# Or immediately after a matching ./build.sh:
./portable-build.sh --skip-build
sha256sum -c cryptodesk-Portable-SHA256SUMS
```

Use an Ubuntu x64 build host with the normal native build prerequisites and Ubuntu runtime packages for GTK3, WebKitGTK 4.1, GLib networking, GSettings desktop schemas, Adwaita icons, librsvg, GStreamer base plugins, bubblewrap and xdg-dbus-proxy. The CI workflow provisions these on its disposable runner. Local scripts never install system packages or change system settings. The initial build needs the configured Ubuntu archives to provide the exact source versions of redistributed installed packages. Missing files, sources or checksums fail packaging rather than producing an incomplete release.

The source helper uses isolated APT configuration, lists, downloads and logs below `.tools/portable-sources`; host APT hooks are excluded. Source indexes are authenticated with the Ubuntu archive keyring. The complete `.dsc`, tar/diff source files, authenticated index records and SHA-256 inventory ship under `runtime/sources`; notices and license texts are under `runtime/licenses`. No source extraction or compilation occurs during collection. Runtime files can be replaced independently, and their exact hashes are listed in `runtime-manifest.json`. The portable archive contains regular files/directories only.

WebKit release builds use compiled helper paths: the upstream [`ProcessExecutablePathGLib.cpp`](https://github.com/WebKit/WebKit/blob/main/Source/WebKit/Shared/glib/ProcessExecutablePathGLib.cpp) restricts `WEBKIT_EXEC_PATH` to developer mode. The bundler therefore relocates only the staged library's exact WebKit helper and sandbox-tool path strings using equal-length replacements. Host libraries remain untouched. The launcher sets the corresponding working directory, so the bundled browser always uses its matching helper versions. Packaging refuses a WebKit library without the expected path. This follows the same need for bundled helpers handled by [Tauri's Linux bundler](https://github.com/tauri-apps/tauri/blob/dev/crates/tauri-bundler/src/bundle/linux/appimage/linuxdeploy.rs).

## Release assets

Future releases contain these three application archives, without unversioned copies:

```text
cryptodesk-X.Y.Z-linux-amd64.tar.gz
cryptodesk-X.Y.Z-linux-amd64-portable.tar.gz
cryptodesk-X.Y.Z-windows-amd64.zip
SHA256SUMS
release.json
```

The publisher checks version/source provenance, safe archive paths, executable architecture, required browser files and the entire portable runtime inventory before publication. Existing completed releases remain unchanged. Download links point to the latest release page so they continue to work as versioned filenames change.

To verify an actual package on a Linux desktop with unprivileged bubblewrap available:

```bash
python3 scripts/smoke-portable.py cryptodesk-X.Y.Z-linux-amd64-portable.tar.gz --evidence /absolute/evidence/directory
```

This separately tests real GUI/browser launches with host GTK/WebKit/modules hidden, isolated profiles, Ctrl+C, SQLite integrity, lock release, saved restart and natural process cleanup. Release CI runs it under Xvfb after packaging; local results and platform limitations are recorded in STATUS.md.

CI first runs `scripts/ci-portable-namespace.sh` as the ordinary runner user. On a GitHub-hosted Ubuntu 24.04 runner with an AppArmor namespace denial, it may add the pinned upstream ABI 4.0 Bubblewrap profile to that disposable runner's kernel, then repeat the unprivileged probe. It refuses existing policy conflicts, leaves sysctls unchanged and does not disable WebKit sandboxing. The policy grants setup to `/usr/bin/bwrap` and strips capabilities from its children; [Canonical describes this approach](https://discourse.ubuntu.com/t/understanding-apparmor-user-namespace-restriction/58007). Source, license and scope are retained in [scripts/apparmor/README.md](../scripts/apparmor/README.md). This CI helper is not a local policy installer.

If startup or shutdown fails, the smoke script exits unsuccessfully, records a failed report, and prints the process exit code and a bounded log tail. The workflow retains the **diagnostics-portable-linux-amd64** artifact even after an earlier setup/build failure. It includes the namespace probe and any application report/logs that were created. The diagnostic name is excluded from the publisher's application-archive selection.
