"""Check the pinned supported-language patch without changing any Go dependency."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import types
import unittest
from unittest.mock import call, patch

spec = importlib.util.spec_from_file_location('windows_webview_language', Path(__file__).with_name('windows_webview_language.py'))
language = importlib.util.module_from_spec(spec)
spec.loader.exec_module(language)

UPSTREAM = b'''//go:build windows && !native_webview2loader

package edge

import (
\t"unsafe"

\t"github.com/wailsapp/go-webview2/webviewloader"
)

func createCoreWebView2EnvironmentWithOptions(browserExecutableFolder, userDataFolder string, environmentCompletedHandle *iCoreWebView2CreateCoreWebView2EnvironmentCompletedHandler, additionalBrowserArgs string) error {
\te := &environmentCreatedHandler{environmentCompletedHandle}
\treturn webviewloader.CreateCoreWebView2EnvironmentWithOptions(
\t\te,
\t\twebviewloader.WithBrowserExecutableFolder(browserExecutableFolder),
\t\twebviewloader.WithUserDataFolder(userDataFolder),
\t\twebviewloader.WithAdditionalBrowserArguments(additionalBrowserArgs),
\t)
}

type environmentCreatedHandler struct {
\toriginalHandler *iCoreWebView2CreateCoreWebView2EnvironmentCompletedHandler
}

func (r *environmentCreatedHandler) EnvironmentCompleted(errorCode webviewloader.HRESULT, createdEnvironment *webviewloader.ICoreWebView2Environment) webviewloader.HRESULT {
\tenv := (*ICoreWebView2Environment)(unsafe.Pointer(createdEnvironment))
\tres := r.originalHandler.impl.EnvironmentCompleted(uintptr(errorCode), env)
\treturn webviewloader.HRESULT(res)
}
'''


class WindowsWebViewLanguageTests(unittest.TestCase):
    def module_fixture(self, root):
        module = root / 'read only module cache'
        source = module / language.SOURCE_FILE
        source.parent.mkdir(parents=True)
        source.write_bytes(UPSTREAM)
        source.chmod(0o444)
        (module / 'go.mod').write_text(f'module {language.MODULE}\n\ngo 1.20\n')
        app = root / 'application checkout'
        app.mkdir()
        (app / 'go.mod').write_text(f'module example.invalid/languageprobe\n\ngo 1.20\n\nrequire {language.MODULE} {language.VERSION}\n')
        (app / 'go.sum').write_bytes(b'')
        report = {'Path': language.MODULE, 'Version': language.VERSION, 'Dir': str(module)}
        return module, source, report, app / 'go.mod'

    def discovery(self, report, main_mod):
        def discover(arguments, **_kwargs):
            if arguments == ['go', 'list', '-m', '-json', language.MODULE]:
                return types.SimpleNamespace(stdout=json.dumps(report))
            self.assertEqual(arguments, ['go', 'env', 'GOMOD'])
            return types.SimpleNamespace(stdout=str(main_mod) + '\n')
        return discover

    def test_exact_public_language_option_is_the_only_source_change(self):
        self.assertEqual(hashlib.sha256(UPSTREAM).hexdigest(), language.SOURCE_SHA256)
        patched = language.patch_language_source(UPSTREAM)
        added = b'\t\twebviewloader.WithLanguage("en-US"),\n'
        self.assertEqual(patched.count(added), 1)
        self.assertEqual(patched.replace(added, b'', 1), UPSTREAM)
        self.assertNotIn(b'--lang', patched)

    def test_unknown_dependency_source_and_unsafe_language_fail_closed(self):
        with self.assertRaisesRegex(RuntimeError, 'SHA-256'):
            language.patch_language_source(UPSTREAM + b'// unknown changes\n')
        for value in ['', 'en_US', 'en-US"), bad()', 'en-US\n', None]:
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, 'language tag'):
                language.patch_language_source(UPSTREAM, value)
        malformed = UPSTREAM.replace(language.PATCH_SITE, language.PATCH_SITE * 2)
        with patch.object(language, 'SOURCE_SHA256', hashlib.sha256(malformed).hexdigest()):
            with self.assertRaisesRegex(RuntimeError, 'exactly one'):
                language.patch_language_source(malformed)

    def test_overlay_manifest_and_unchanged_cache(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            module, source, discovered, main_mod = self.module_fixture(root)
            before = source.stat()
            cache_before = {str(path.relative_to(module)): path.read_bytes() for path in module.rglob('*') if path.is_file()}
            with patch.object(language.subprocess, 'run', side_effect=self.discovery(discovered, main_mod)) as run:
                report = language.prepare_language_overlay(root / 'scratch with spaces')
            self.assertEqual(run.call_args_list, [call(['go', 'list', '-m', '-json', language.MODULE], check=True, capture_output=True, text=True, timeout=30), call(['go', 'env', 'GOMOD'], check=True, capture_output=True, text=True, timeout=30)])
            self.assertEqual(source.read_bytes(), UPSTREAM)
            self.assertEqual(source.stat().st_mode, before.st_mode)
            self.assertEqual(source.stat().st_mtime_ns, before.st_mtime_ns)
            self.assertEqual({str(path.relative_to(module)): path.read_bytes() for path in module.rglob('*') if path.is_file()}, cache_before)
            overlay = json.loads(Path(report['overlayPath']).read_text())
            self.assertEqual(set(overlay), {'Replace'})
            copied_source = Path(report['overlayPath']).parent / 'go-webview2-v1.0.22' / language.SOURCE_FILE
            self.assertEqual(list(overlay['Replace']), [str(copied_source)])
            self.assertEqual(copied_source.read_bytes(), UPSTREAM)
            patched = Path(overlay['Replace'][str(copied_source)]).read_bytes()
            self.assertEqual(patched, language.patch_language_source(UPSTREAM))
            self.assertEqual(report['manifest'], {'module': language.MODULE, 'version': 'v1.0.22', 'sourceFile': language.SOURCE_FILE, 'originalSHA256': language.SOURCE_SHA256, 'patchedSHA256': hashlib.sha256(patched).hexdigest(), 'api': 'webviewloader.WithLanguage', 'method': 'temporary-module-replace-and-source-overlay', 'language': 'en-US'})
            self.assertEqual(set(path.name for path in Path(report['overlayPath']).parent.iterdir()), {'overlay.json', 'create_env_go.go', 'go-webview2-v1.0.22', 'app.mod', 'app.sum'})
            self.assertEqual(main_mod.read_text(), f'module example.invalid/languageprobe\n\ngo 1.20\n\nrequire {language.MODULE} {language.VERSION}\n')
            self.assertTrue(Path(report['modfilePath']).read_bytes().startswith(main_mod.read_bytes()))
            self.assertIn('replace ' + language.MODULE + ' ' + language.VERSION, Path(report['modfilePath']).read_text())
            self.assertEqual(Path(report['modfilePath']).with_suffix('.sum').read_bytes(), main_mod.with_suffix('.sum').read_bytes())

    def test_module_version_replacement_existing_files_and_cache_destination_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            module, source, discovered, main_mod = self.module_fixture(root)
            for change in [{'Version': 'v1.0.23'}, {'Path': 'example.invalid/other'}, {'Replace': {'Dir': '/somewhere'}}, {'Dir': ''}]:
                with self.subTest(change=change), patch.object(language.subprocess, 'run', return_value=types.SimpleNamespace(stdout=json.dumps({**discovered, **change}))):
                    with self.assertRaises(RuntimeError):
                        language.prepare_language_overlay(root / 'unused')
            with patch.object(language.subprocess, 'run', side_effect=self.discovery(discovered, main_mod)):
                with self.assertRaisesRegex(RuntimeError, 'outside'):
                    language.prepare_language_overlay(module / 'do not write here')
                scratch = root / 'scratch'
                scratch.mkdir()
                existing = scratch / 'create_env_go.go'
                existing.write_bytes(b'keep my work')
                with self.assertRaisesRegex(RuntimeError, 'Refusing to replace'):
                    language.prepare_language_overlay(scratch)
                self.assertEqual(existing.read_bytes(), b'keep my work')
            self.assertEqual(source.read_bytes(), UPSTREAM)

    def test_go_flag_quoting_matches_go_rules(self):
        self.assertEqual(language.quote_go_flag('-overlay=/tmp/overlay.json'), '-overlay=/tmp/overlay.json')
        self.assertEqual(language.quote_go_flag('-overlay=/tmp/with spaces/overlay.json'), "'-overlay=/tmp/with spaces/overlay.json'")
        self.assertEqual(language.quote_go_flag("-overlay=/tmp/Someone's Files/overlay.json"), '"-overlay=/tmp/Someone\'s Files/overlay.json"')
        with self.assertRaisesRegex(ValueError, 'both quote types'):
            language.quote_go_flag('-overlay=/tmp/both\'"quotes/overlay.json')

    @unittest.skipUnless(shutil.which('go'), 'Go executable unavailable')
    def test_go_actually_accepts_the_generated_flag_when_overlay_path_has_spaces(self):
        with tempfile.TemporaryDirectory(prefix='language overlay with spaces ') as temporary:
            root = Path(temporary)
            _, _, discovered, main_mod = self.module_fixture(root)
            with patch.object(language.subprocess, 'run', side_effect=self.discovery(discovered, main_mod)):
                report = language.prepare_language_overlay(root / 'scratch with spaces')
            probe = root / 'probe'
            probe.mkdir()
            (probe / 'go.mod').write_text('module example.invalid/languageprobe\n\ngo 1.20\n')
            (probe / 'probe.go').write_text('package languageprobe\n')
            result = subprocess.run(['go', 'list', '-json', '.'], cwd=probe, env={**os.environ, 'GOFLAGS': '-mod=readonly ' + report['goFlag']}, check=True, capture_output=True, text=True, timeout=30)
            self.assertEqual(json.loads(result.stdout)['ImportPath'], 'example.invalid/languageprobe')


if __name__ == '__main__':
    unittest.main()
