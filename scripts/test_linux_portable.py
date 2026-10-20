"""Offline checks for the portable runtime and its process-local launcher.

Synthetic ELF markers exercise dependency traversal and relocation without
loading native GUI libraries, executing ldd, downloading files or starting GUI.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import linux_portable as portable

ROOT = Path(__file__).resolve().parent.parent


def synthetic_elf(path, label):
    path.write_bytes(b"\x7fELF" + label.encode() + b"\x00")
    return path


class PortableRuntimeTests(unittest.TestCase):
    def test_ldd_resolves_real_libraries_and_refuses_missing_or_failed_scans(self):
        output = """\
        linux-vdso.so.1 (0x00007ffc11111000)
        libgtk-3.so.0 => /lib/x86_64-linux-gnu/libgtk-3.so.0 (0x00007f1111110000)
        libwebkit2gtk-4.1.so.0 => /usr/lib/x86_64-linux-gnu/libwebkit2gtk-4.1.so.0 (0x00007f2222220000)
        libc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x00007f3333330000)
        /lib64/ld-linux-x86-64.so.2 (0x00007f4444440000)
"""
        with patch.object(portable.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, output, "")) as run:
            self.assertEqual(portable.dependencies(Path("/package/application")), {
                "libgtk-3.so.0": Path("/lib/x86_64-linux-gnu/libgtk-3.so.0"),
                "libwebkit2gtk-4.1.so.0": Path("/usr/lib/x86_64-linux-gnu/libwebkit2gtk-4.1.so.0"),
                "libc.so.6": Path("/lib/x86_64-linux-gnu/libc.so.6"),
            })
            run.assert_called_once_with(["ldd", "/package/application"], text=True, capture_output=True, check=True)
        missing = output + "        libjavascriptcoregtk-4.1.so.0 => not found\n"
        with patch.object(portable.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, missing, "")):
            with self.assertRaisesRegex(ValueError, "Missing dependencies.*application"):
                portable.dependencies(Path("/package/application"))
        with patch.object(portable.subprocess, "run", side_effect=subprocess.CalledProcessError(1, ["ldd"])):
            with self.assertRaises(subprocess.CalledProcessError):
                portable.dependencies(Path("/package/application"))

    def test_runtime_bundles_gui_and_language_libraries_but_preserves_host_abi_and_drivers(self):
        host = ("ld-linux-x86-64.so.2", "libc.so.6", "libm.so.6", "libpthread.so.0", "libdl.so.2",
                "libresolv.so.2", "libnss_files.so.2", "libGL.so.1", "libEGL.so.1", "libGLX.so.0",
                "libGLdispatch.so.0", "libgbm.so.1", "libdrm_amdgpu.so.1", "libwayland-client.so.0",
                "libvulkan.so.1", "libnvidia-glcore.so.1", "libcuda.so.1")
        for name in host:
            with self.subTest(host=name):
                self.assertIsNotNone(portable.HOST_LIBRARIES.match(name))
        for name in ("libgtk-3.so.0", "libwebkit2gtk-4.1.so.0", "libjavascriptcoregtk-4.1.so.0",
                     "libglib-2.0.so.0", "libgio-2.0.so.0", "libstdc++.so.6", "libgcc_s.so.1", "libcrypto.so.3"):
            with self.subTest(bundled=name):
                self.assertIsNone(portable.HOST_LIBRARIES.match(name))
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            app = synthetic_elf(root / "application", "app")
            gtk = synthetic_elf(root / "libgtk-3.so.0", "gtk")
            webkit = synthetic_elf(root / "libwebkit2gtk-4.1.so.0", "webkit")
            glib = root / "libglib-2.0.so.0"
            glib.write_bytes(b"Synthetic terminal dependency\n")
            cpp = root / "libstdc++.so.6"
            cpp.write_bytes(b"Synthetic language runtime\n")
            # These deliberately do not exist: host dependencies must never be copied.
            external = {name: root / "host only" / name for name in host}
            graph = {app: {gtk.name: gtk, webkit.name: webkit, **external},
                     gtk: {glib.name: glib, "libc.so.6": external["libc.so.6"]},
                     webkit: {gtk.name: gtk, cpp.name: cpp}}
            runtime = portable.Runtime(root / "runtime")
            with patch.object(portable, "dependencies", side_effect=lambda path: graph[Path(path)]) as scan:
                runtime.copy(app, Path("usr/bin/cryptodesk"))
            self.assertEqual(scan.call_count, 3)
            self.assertEqual(runtime.external, set(host))
            for source in (gtk, webkit, glib, cpp):
                self.assertEqual((runtime.directory / "usr/lib" / portable.MULTIARCH / source.name).read_bytes(), source.read_bytes())
            self.assertEqual(len(runtime.origins), 5)
            self.assertFalse((runtime.directory / "host only").exists())

    def test_dependency_cycles_and_symlink_aliases_copy_once_and_conflicts_preserve_existing_file(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            first = synthetic_elf(root / "first-real.so", "first")
            first.chmod(0o751)
            alias = root / "libfirst.so.1"
            alias.symlink_to(first.name)
            second = synthetic_elf(root / "libsecond.so.1", "second")
            runtime = portable.Runtime(root / "runtime")
            relative = Path("usr/lib") / portable.MULTIARCH / alias.name
            graph = {first: {second.name: second}, second: {alias.name: first}}
            with patch.object(portable, "dependencies", side_effect=lambda path: graph[Path(path).resolve()]) as scan:
                result = runtime.copy(alias, relative)
                self.assertEqual(runtime.copy(first, relative), result)
            self.assertEqual(scan.call_count, 2)
            self.assertEqual(len(runtime.origins), 2)
            self.assertFalse(result.is_symlink())
            self.assertEqual(result.read_bytes(), first.read_bytes())
            self.assertEqual(result.stat().st_mode & 0o777, 0o751)
            conflicting = root / "other-source.so"
            conflicting.write_bytes(b"Distinct file that must not overwrite the accepted runtime\n")
            before = result.read_bytes()
            with self.assertRaisesRegex(ValueError, "Conflicting runtime file"):
                runtime.copy(conflicting, relative)
            self.assertEqual(result.read_bytes(), before)
            with self.assertRaisesRegex(ValueError, "Required runtime file missing"):
                runtime.copy(root / "missing.so")
            self.assertEqual(len(runtime.origins), 2)

    def test_webkit_relocation_preserves_offsets_and_refuses_a_missing_helper_prefix_without_writing(self):
        prefix = "/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1"
        replacements = {prefix.encode(): b"././/lib/x86_64-linux-gnu/webkit2gtk-4.1",
                        b"/usr/bin/bwrap": b"././/bin/bwrap",
                        b"/usr/bin/xdg-dbus-proxy": b"././/bin/xdg-dbus-proxy"}
        header = b"\x7fELF" + bytes(range(4, 64))
        marker = b"UNCHANGED_ELF_SECTION_OFFSET_SENTINEL"
        original = header + b"\x00".join([prefix.encode(), b"/usr/bin/bwrap", marker,
                                          prefix.encode(), b"/usr/bin/xdg-dbus-proxy", b"TAIL"])
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "host-webkit.so"
            source.write_bytes(original)
            staged = root / "staged-webkit.so"
            shutil.copy2(source, staged)
            report = portable.relocate_webkit(staged, prefix)
            relocated = staged.read_bytes()
            self.assertEqual(len(relocated), len(original))
            self.assertEqual(relocated[:64], header)
            self.assertEqual(relocated.index(marker), original.index(marker))
            self.assertEqual(relocated[-4:], b"TAIL")
            for before, after in replacements.items():
                self.assertEqual(len(before), len(after))
                self.assertNotIn(before, relocated)
                self.assertEqual(relocated.count(after), original.count(before))
                self.assertIn({"from": before.decode(), "to": after.decode()}, report)
            self.assertEqual(source.read_bytes(), original)
            # A helper-tool path alone must not allow publishing an unrelocatable WebKit.
            invalid = root / "unrecognized-webkit.so"
            invalid.write_bytes(header + b"/usr/bin/bwrap\x00" + marker)
            before = invalid.read_bytes()
            with self.assertRaisesRegex(ValueError, "helper directory not found"):
                portable.relocate_webkit(invalid, prefix)
            self.assertEqual(invalid.read_bytes(), before)

    def test_launcher_quotes_paths_and_arguments_keeps_relative_profiles_and_scopes_runtime_environment(self):
        with tempfile.TemporaryDirectory(prefix="cryptodesk portable test ") as temporary:
            root = Path(temporary)
            package = root / "unpacked portable with spaces"
            usr = package / "runtime/usr"
            (usr / "bin").mkdir(parents=True)
            launcher = package / "cryptodesk"
            shutil.copy2(ROOT / "build/linux/portable-launcher.sh", launcher)
            launcher.chmod(0o755)
            fake = usr / "bin/cryptodesk"
            fake.write_text(f"#!{sys.executable}\n" + """import json, os, sys
keys = ['LD_LIBRARY_PATH', 'GIO_MODULE_DIR', 'GIO_EXTRA_MODULES', 'GSETTINGS_SCHEMA_DIR',
        'GTK_PATH', 'GTK_IM_MODULE', 'GTK_MODULES', 'GTK3_MODULES', 'GDK_PIXBUF_MODULE_FILE',
        'GST_PLUGIN_SCANNER_1_0', 'GST_REGISTRY_1_0', 'GLYCIN_DATA_DIR', 'XDG_DATA_DIRS',
        'XDG_DATA_HOME', 'XDG_CACHE_HOME', 'TZ', 'HOME', 'PATH']
print(json.dumps({'args': sys.argv[1:], 'cwd': os.getcwd(), 'env': {key: os.environ.get(key) for key in keys}}))
sys.exit(int(os.environ.get('CRYPTO_TEST_EXIT', '0')))
""")
            fake.chmod(0o755)
            caller = root / "caller directory"
            caller.mkdir()
            environment = dict(os.environ, LD_LIBRARY_PATH="/explicit-host-runtime", GTK_MODULES="host-module",
                               GTK3_MODULES="host-gtk3-module", GTK_IM_MODULE="host-input-method",
                               GIO_MODULE_DIR="/explicit-host-gio", HOME=str(root / "home with spaces"),
                               XDG_DATA_HOME=str(root / "saved settings"), XDG_CACHE_HOME=str(root / "cache with spaces"),
                               XDG_DATA_DIRS="/explicit-host-data", TZ="Pacific/Auckland")
            parent_before = dict(os.environ)
            args = ["--data-dir", "profile with spaces", "--data-dir=another relative profile", "--data-dir=/absolute profile",
                    "--data-dir=", "--data-dir", "", "--literal", "value with spaces;$HOME"]
            result = subprocess.run([str(launcher), *args], cwd=caller, env=environment,
                                    capture_output=True, text=True, check=True, timeout=10)
            report = json.loads(result.stdout)
            self.assertEqual(report["args"], ["--data-dir", str(caller / "profile with spaces"),
                                               "--data-dir=" + str(caller / "another relative profile"),
                                               "--data-dir=/absolute profile", "--data-dir=", "--data-dir", "",
                                               "--literal", "value with spaces;$HOME"])
            self.assertEqual(report["cwd"], str(usr))
            actual = report["env"]
            library_dir = usr / "lib/x86_64-linux-gnu"
            self.assertEqual(actual["LD_LIBRARY_PATH"], str(library_dir))
            self.assertEqual(actual["GIO_MODULE_DIR"], str(library_dir / "gio/modules"))
            self.assertEqual(actual["GIO_EXTRA_MODULES"], actual["GIO_MODULE_DIR"])
            self.assertEqual(actual["GSETTINGS_SCHEMA_DIR"], str(usr / "share/glib-2.0/schemas"))
            self.assertEqual(actual["GTK_PATH"], str(library_dir / "gtk-3.0"))
            self.assertEqual(actual["GTK_IM_MODULE"], "simple")
            self.assertIsNone(actual["GTK_MODULES"])
            self.assertIsNone(actual["GTK3_MODULES"])
            self.assertEqual(actual["GDK_PIXBUF_MODULE_FILE"], str(library_dir / "gdk-pixbuf-2.0/2.10.0/loaders.cache"))
            self.assertEqual(actual["GST_PLUGIN_SCANNER_1_0"], str(library_dir / "gstreamer1.0/gstreamer-1.0/gst-plugin-scanner"))
            self.assertEqual(actual["GST_REGISTRY_1_0"], str(root / "cache with spaces/cryptodesk/gstreamer-registry.bin"))
            self.assertEqual(actual["GLYCIN_DATA_DIR"], str(usr / "share"))
            self.assertEqual(actual["XDG_DATA_DIRS"], str(usr / "share") + ":/explicit-host-data")
            self.assertTrue(actual["PATH"].startswith(str(usr / "bin") + ":"))
            for name in ("XDG_DATA_HOME", "XDG_CACHE_HOME", "TZ", "HOME"):
                self.assertEqual(actual[name], environment[name])
            self.assertEqual(os.environ, parent_before)
            self.assertEqual(environment["GTK_MODULES"], "host-module")
            environment.pop("XDG_CACHE_HOME")
            environment["CRYPTO_TEST_EXIT"] = "17"
            result = subprocess.run([str(launcher), "--data-dir"], cwd=caller, env=environment,
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 17)
            report = json.loads(result.stdout)
            self.assertEqual(report["args"], ["--data-dir"])
            self.assertEqual(report["env"]["GST_REGISTRY_1_0"], str(root / "home with spaces/.cache/cryptodesk/gstreamer-registry.bin"))


    def test_external_opener_restores_the_callers_environment_and_keeps_the_url_literal(self):
        with tempfile.TemporaryDirectory(prefix="cryptodesk external opener ") as temporary:
            root = Path(temporary)
            package = root / "portable package with spaces"
            usr = package / "runtime/usr"
            (usr / "bin").mkdir(parents=True)
            host_bin = root / "host tools with spaces"
            host_bin.mkdir()
            launcher = package / "cryptodesk"
            shutil.copy2(ROOT / "build/linux/portable-launcher.sh", launcher)
            wrapper = usr / "bin/xdg-open"
            shutil.copy2(ROOT / "build/linux/portable-xdg-open.sh", wrapper)
            app = usr / "bin/cryptodesk"
            app.write_text(f"#!{sys.executable}\n" + """import json, os, subprocess, sys
opened = subprocess.run(['xdg-open', sys.argv[1]], capture_output=True, text=True, check=True)
print(json.dumps({'appLibraryPath': os.environ.get('LD_LIBRARY_PATH'), 'appPath': os.environ['PATH'],
                  'opened': json.loads(opened.stdout)}))
""")
            opener = host_bin / "xdg-open"
            opener.write_text(f"#!{sys.executable}\n" + """import json, os, sys
keys = ['LD_LIBRARY_PATH', 'GIO_MODULE_DIR', 'PATH', 'GIO_EXTRA_MODULES', 'GTK_MODULES',
        'GTK3_MODULES', 'GTK_IM_MODULE', 'GSETTINGS_SCHEMA_DIR', 'GLYCIN_DATA_DIR',
        'XDG_DATA_DIRS', 'TZ', 'HOME']
print(json.dumps({'program': __file__, 'args': sys.argv[1:],
                  'env': {key: os.environ.get(key) for key in keys},
                  'markers': sorted(key for key in os.environ if key.startswith('CRYPTODESK_HOST_'))}))
""")
            for executable in (launcher, wrapper, app, opener):
                executable.chmod(0o755)
            # Explicitly absent values must stay absent; explicitly empty ones must stay empty.
            environment = {key: value for key, value in os.environ.items()
                           if not key.startswith("CRYPTODESK_HOST_")}
            for name in ("GIO_EXTRA_MODULES", "GTK3_MODULES", "GSETTINGS_SCHEMA_DIR", "GLYCIN_DATA_DIR", "XDG_DATA_DIRS"):
                environment.pop(name, None)
            environment.update(LD_LIBRARY_PATH="/host runtime with spaces", GIO_MODULE_DIR="/host gio with spaces",
                               PATH=str(host_bin) + os.pathsep + os.defpath, GTK_MODULES="", GTK_IM_MODULE="host-input-method",
                               TZ="Asia/Dubai", HOME=str(root / "home with spaces"))
            before = dict(os.environ)
            url = "https://www.tradingview.com/?a=one%20two&b=$HOME;echo%20fake#fragment"
            result = subprocess.run([str(launcher), url], cwd=root, env=environment,
                                    capture_output=True, text=True, check=True, timeout=10)
            report = json.loads(result.stdout)
            self.assertEqual(report["appLibraryPath"], str(usr / "lib/x86_64-linux-gnu"))
            self.assertTrue(report["appPath"].startswith(str(usr / "bin") + os.pathsep))
            opened = report["opened"]
            self.assertEqual(opened["program"], str(opener))
            self.assertEqual(opened["args"], [url])
            self.assertEqual(opened["markers"], [])
            for name in ("LD_LIBRARY_PATH", "GIO_MODULE_DIR", "PATH", "GTK_MODULES", "GTK_IM_MODULE", "TZ", "HOME"):
                self.assertEqual(opened["env"][name], environment[name])
            for name in ("GIO_EXTRA_MODULES", "GTK3_MODULES", "GSETTINGS_SCHEMA_DIR", "GLYCIN_DATA_DIR", "XDG_DATA_DIRS"):
                self.assertIsNone(opened["env"][name])
            self.assertEqual(os.environ, before)


if __name__ == "__main__":
    unittest.main()
