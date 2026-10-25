#!/usr/bin/env python3
"""Assemble an Ubuntu GTK3/WebKitGTK runtime with an auditable file inventory.

Build on the oldest supported Ubuntu release. glibc and graphics-driver-facing
libraries stay on the host; browser, GTK, modules and other dependencies ship in
the archive. No installation, FUSE, system configuration or sandbox bypass.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile

from app_version import app_version

MULTIARCH = "x86_64-linux-gnu"
HOST_LIBRARIES = re.compile(r"^(?:ld-linux|lib(?:c|m|pthread|rt|dl|util|resolv|anl|nss_[^.]+)\.so|"
                            r"lib(?:GL|GLX|GLdispatch|EGL|OpenGL|GLESv[12]|gbm|drm[^.]*|wayland-[^.]+|vulkan|nvidia[^.]*|cuda)\.so)")


def digest(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def dependencies(path):
    result = subprocess.run(["ldd", str(path)], text=True, capture_output=True, check=True)
    if "not found" in result.stdout:
        raise ValueError(f"Missing dependencies for {path}: {result.stdout}")
    libraries = {}
    for line in result.stdout.splitlines():
        match = re.match(r"\s*(\S+) => (/\S+) \(", line)
        if match:
            libraries[match[1]] = Path(match[2])
    return libraries


def relocate_webkit(path, prefix):
    """Length-preserving path relocation, restricted to staged WebKit copies.

    WEBKIT_EXEC_PATH is developer-only upstream. Relocate compiled release
    helper/injected-bundle paths instead, preserving ELF section offsets.
    """
    content = path.read_bytes()
    replaced = []
    for original in (prefix, "/usr/bin/bwrap", "/usr/bin/xdg-dbus-proxy"):
        before = original.encode()
        after = b"././" + before[4:]
        if before in content:
            content = content.replace(before, after)
            replaced.append({"from": original, "to": after.decode()})
    if not any(item["from"] == prefix for item in replaced):
        raise ValueError("WebKit compiled helper directory not found; refusing an unrelocatable runtime")
    path.write_bytes(content)
    return replaced


class Runtime:
    def __init__(self, directory):
        self.directory = Path(directory)
        self.origins = {}
        self.external = set()

    def copy(self, source, relative=None):
        source = Path(source)
        if not source.is_file():
            raise ValueError(f"Required runtime file missing: {source}")
        relative = Path(relative) if relative else source.relative_to("/")
        destination = self.directory / relative
        if destination in self.origins:
            if source.resolve() != self.origins[destination].resolve():
                raise ValueError(f"Conflicting runtime file: {relative}")
            return destination
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, destination, follow_symlinks=True)
        destination.chmod(destination.stat().st_mode & 0o777)
        self.origins[destination] = source
        with source.open("rb") as stream:
            is_elf = stream.read(4) == b"\x7fELF"
        if is_elf:
            for name, library in dependencies(source).items():
                if HOST_LIBRARIES.match(name):
                    self.external.add(name)
                else:
                    self.copy(library, Path("usr/lib") / MULTIARCH / name)
        return destination

    def tree(self, source, pattern="*"):
        source = Path(source)
        if source.is_dir():
            for file in sorted(source.rglob(pattern)):
                if file.is_file() and file.name != "icon-theme.cache":
                    self.copy(file)

    def packages(self):
        packages = set()
        sources = sorted({str(source.resolve()) for source in self.origins.values()})
        for offset in range(0, len(sources), 100):
            owners = subprocess.check_output(["dpkg-query", "-S", *sources[offset:offset + 100]], text=True)
            # dpkg may print one owner per line (multiarch names contain ':').
            packages.update(line.rsplit(": ", 1)[0] for line in owners.splitlines())
        return sorted(packages)


def assemble_runtime(directory, binary=None, extra_binaries=()):
    runtime = Runtime(directory)
    library_dir = Path("/usr/lib") / MULTIARCH
    runtime.directory.mkdir(parents=True, exist_ok=True)
    if binary is not None:
        app = runtime.directory / "usr/bin/cryptodesk"
        app.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(binary, app)
        for name, library in dependencies(binary).items():
            if HOST_LIBRARIES.match(name):
                runtime.external.add(name)
            else:
                runtime.copy(library, Path("usr/lib") / MULTIARCH / name)
    else:
        for name in ("libgtk-3.so.0", "libwebkit2gtk-4.1.so.0"):
            runtime.copy(library_dir / name)
    # The downloader's private Chromium shares this process-local library path.
    # Include its host dependency closure alongside the desktop's libraries.
    for executable in extra_binaries:
        for name, library in dependencies(executable).items():
            if HOST_LIBRARIES.match(name):
                runtime.external.add(name)
            else:
                runtime.copy(library, Path("usr/lib") / MULTIARCH / name)
    if extra_binaries:
        # NSS loads its PKCS#11 modules dynamically; ldd alone cannot discover
        # these or the adjacent integrity records used by the soft-token module.
        for pattern in ("libnssckbi.so", "libnssdbm3.so", "libsoftokn3.*", "libfreebl*.so", "libfreebl*.chk"):
            for module in sorted(library_dir.glob(pattern)):
                runtime.copy(module)
    webkit_dir = library_dir / "webkit2gtk-4.1"
    for name in ("WebKitNetworkProcess", "WebKitWebProcess"):
        runtime.copy(webkit_dir / name)
    for name in ("WebKitGPUProcess", "injected-bundle/libwebkit2gtkinjectedbundle.so"):
        if (webkit_dir / name).is_file():
            runtime.copy(webkit_dir / name)
    for name in ("bwrap", "xdg-dbus-proxy"):
        runtime.copy(Path("/usr/bin") / name)
    for module in ("libdconfsettings.so", "libgiognutls.so", "libgiognomeproxy.so"):
        runtime.copy(library_dir / "gio/modules" / module)
    # GTK includes the simple input module. Avoid host input-method ABI plugins.
    runtime.copy(library_dir / "gdk-pixbuf-2.0/gdk-pixbuf-query-loaders")
    svg_loader = library_dir / "gdk-pixbuf-2.0/2.10.0/loaders/libpixbufloader-svg.so"
    if not svg_loader.exists():
        svg_loader = svg_loader.with_name("libpixbufloader_svg.so")
    if svg_loader.exists():
        runtime.copy(svg_loader)
    runtime.copy(library_dir / "gstreamer1.0/gstreamer-1.0/gst-plugin-scanner")
    for name in ("app", "coreelements", "typefindfunctions"):
        runtime.copy(library_dir / f"gstreamer-1.0/libgst{name}.so")
    runtime.tree("/usr/share/glib-2.0/schemas", "org.gtk.Settings.*.xml")
    runtime.tree("/usr/share/glib-2.0/schemas", "org.gnome.desktop.*.xml")
    runtime.tree("/usr/share/glib-2.0/schemas", "org.gnome.system.*.xml")
    runtime.tree("/usr/share/icons/Adwaita")
    # Newer GdkPixbuf uses out-of-process glycin loaders (Ubuntu26+).
    for loader in Path("/usr/libexec/glycin-loaders").glob("*/glycin-image-rs"):
        runtime.copy(loader)
    for config in Path("/usr/share/glycin-loaders").glob("*/conf.d/glycin-image-rs.conf"):
        runtime.copy(config)
    # Equal-length path substitution is confined to copies, never host libraries.
    relocated = relocate_webkit(runtime.directory / "usr/lib" / MULTIARCH / "libwebkit2gtk-4.1.so.0", str(webkit_dir))
    # Glycin's config stores absolute loader paths; its relocated copies are text.
    for config in (runtime.directory / "usr/share/glycin-loaders").rglob("*.conf"):
        config.write_text(config.read_text().replace("/usr/libexec/glycin-loaders", "./libexec/glycin-loaders"))
    loaders_dir = runtime.directory / "usr/lib" / MULTIARCH / "gdk-pixbuf-2.0/2.10.0"
    loaders_dir.mkdir(parents=True, exist_ok=True)
    loader_files = sorted((loaders_dir / "loaders").glob("*.so"))
    if loader_files:
        cache = subprocess.check_output([str(library_dir / "gdk-pixbuf-2.0/gdk-pixbuf-query-loaders"), *map(str, loader_files)], text=True)
        (loaders_dir / "loaders.cache").write_text(cache.replace(str(runtime.directory / "usr") + "/", "./"))
    else:
        (loaders_dir / "loaders.cache").write_text("")
    subprocess.run(["glib-compile-schemas", str(runtime.directory / "usr/share/glib-2.0/schemas")], check=True)
    packages = runtime.packages()
    licenses = runtime.directory / "licenses"
    licenses.mkdir()
    for package in packages:
        name = package.split(":")[0]
        copyright_file = Path("/usr/share/doc") / name / "copyright"
        if not copyright_file.is_file():
            raise ValueError(f"Missing redistribution notice: {package}")
        shutil.copy2(copyright_file, licenses / f"{package}.copyright")
    common = Path("/usr/share/common-licenses")
    for license_file in common.iterdir():
        if license_file.is_file():
            shutil.copy2(license_file, licenses / license_file.name)
    return runtime, packages, relocated


def screener_binaries(directory):
    from screener_bundle import browser_path
    directory = Path(directory)
    chromium = directory / browser_path("linux-amd64")
    return [directory / "node", chromium, chromium.with_name("chrome_crashpad_handler")]


def create(root):
    from portable_sources import collect_sources
    version = app_version(root)
    standard = root / "release" / f"cryptodesk-{version}-linux-amd64"
    manifest = json.loads((standard / "version.json").read_text())
    if manifest["version"] != version or manifest["target"] != "linux/amd64":
        raise ValueError("Build the matching Linux archive first")
    binary = standard / "cryptodesk"
    if binary.read_bytes() != (root / "cryptodesk").read_bytes():
        raise ValueError("Root executable differs from the Linux package; run ./build.sh")
    name = f"cryptodesk-{version}-linux-amd64-portable"
    output = root / (name + ".tar.gz")
    workspace = root / ".tools/portable-staging"
    workspace.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=workspace) as temporary:
        directory = Path(temporary) / name
        directory.mkdir()
        for filename in ("APP_README.md", "LICENSE", "cryptodesk.desktop", "cryptodesk.svg", "cryptodesk.png", "install-user.sh"):
            shutil.copy2(standard / filename, directory / filename)
        shutil.copytree(standard / "icons", directory / "icons")
        shutil.copytree(standard / "docs", directory / "docs")
        from screener_bundle import validate_directory
        validate_directory(standard / "screener-runtime", "linux-amd64")
        shutil.copytree(standard / "screener-runtime", directory / "screener-runtime")
        shutil.copy2(root / "build/linux/portable-launcher.sh", directory / "cryptodesk")
        (directory / "cryptodesk").chmod(0o755)
        runtime, packages, relocated = assemble_runtime(directory / "runtime", binary,
                                                       screener_binaries(directory / "screener-runtime"))
        opener = directory / "runtime/usr/bin/xdg-open"
        shutil.copy2(root / "build/linux/portable-xdg-open.sh", opener)
        opener.chmod(0o755)
        print(f"Bundled runtime from {len(packages)} Ubuntu packages; collecting exact corresponding sources", flush=True)
        sources = collect_sources(packages, directory / "runtime/sources", root / ".tools/portable-sources")
        modifications = directory / "runtime/sources/cryptodesk-bundling"
        modifications.mkdir()
        for source in (root / "scripts/linux_portable.py", root / "scripts/portable_sources.py", root / "scripts/app_version.py",
                       root / "build/linux/portable-launcher.sh", root / "build/linux/portable-xdg-open.sh",
                       root / "docs/LINUX_PORTABLE.md", root / "LICENSE"):
            shutil.copy2(source, modifications / source.name)
        manifest.update({"packageVariant": "portable", "runtime": {"glibc": subprocess.check_output(["getconf", "GNU_LIBC_VERSION"], text=True).strip(),
                         "buildOS": Path("/etc/os-release").read_text(), "hostLibraries": sorted(runtime.external), "relocatedWebKitPaths": relocated,
                         "sourcesManifest": "runtime/sources/portable-sources.json", "sourcePackages": len(sources.get("sources", []))}})
        (directory / "version.json").write_text(json.dumps(manifest, indent=2) + "\n")
        inventory = {str(path.relative_to(directory)): {"sha256": digest(path), "bytes": path.stat().st_size}
                     for path in sorted((directory / "runtime").rglob("*")) if path.is_file()}
        (directory / "runtime-manifest.json").write_text(json.dumps(inventory, indent=2) + "\n")
        (directory / "SHA256SUMS").write_text("".join(f"{digest(path)}  {path.relative_to(directory)}\n" for path in sorted(directory.rglob("*")) if path.is_file()))
        partial = output.with_suffix(output.suffix + ".tmp")
        try:
            with tarfile.open(partial, "w:gz", dereference=True) as archive:
                archive.add(directory, arcname=name)
            os.replace(partial, output)
        finally:
            partial.unlink(missing_ok=True)
    (root / "cryptodesk-Portable-SHA256SUMS").write_text(f"{digest(output)}  {output.name}\n")
    print(f"Portable runtime archive: {output}\nBytes: {output.stat().st_size:,}", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument("--list-packages", action="store_true", help="List installed runtime package names without an app build or source downloads")
    parser.add_argument("--screener-runtime", type=Path, help="Include this prepared downloader's dependency closure when listing packages")
    arguments = parser.parse_args()
    try:
        if arguments.list_packages:
            extra_binaries = ()
            if arguments.screener_runtime:
                from screener_bundle import validate_directory
                validate_directory(arguments.screener_runtime, "linux-amd64", allow_runtime_state=True)
                extra_binaries = screener_binaries(arguments.screener_runtime)
            with tempfile.TemporaryDirectory(prefix="cryptodesk-runtime-packages-") as temporary:
                _, packages, _ = assemble_runtime(Path(temporary), extra_binaries=extra_binaries)
                print("\n".join(packages))
        else:
            create(arguments.root.resolve())
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        parser.exit(1, f"Portable Linux build failed: {error}\n")


if __name__ == "__main__":
    main()
