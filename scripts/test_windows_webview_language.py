"""Check the pinned supported-language patch without changing any Go dependency."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import call, patch
import zipfile

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

    def discovery(self, report, main_mod, downloaded=None):
        def discover(arguments, **_kwargs):
            if arguments == ['go', 'list', '-m', '-json', language.MODULE]:
                return types.SimpleNamespace(stdout=json.dumps(report))
            if arguments == ['go', 'mod', 'download', '-json', f'{language.MODULE}@{language.VERSION}']:
                return types.SimpleNamespace(stdout=json.dumps(downloaded if downloaded is not None else report))
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

    def test_cold_metadata_downloads_exact_pinned_module_before_overlay(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            module, source, downloaded, main_mod = self.module_fixture(root)
            cold = {key: value for key, value in downloaded.items() if key != 'Dir'}
            before = {path.name: path.read_bytes() for path in (main_mod, main_mod.with_suffix('.sum'))}
            with patch.object(language.subprocess, 'run', side_effect=self.discovery(cold, main_mod, downloaded)) as run:
                report = language.prepare_language_overlay(root / 'scratch')
            self.assertEqual(run.call_args_list, [
                call(['go', 'list', '-m', '-json', language.MODULE], check=True, capture_output=True, text=True, timeout=30),
                call(['go', 'mod', 'download', '-json', f'{language.MODULE}@{language.VERSION}'], check=True, capture_output=True, text=True, timeout=180),
                call(['go', 'env', 'GOMOD'], check=True, capture_output=True, text=True, timeout=30),
            ])
            self.assertEqual({path.name: path.read_bytes() for path in (main_mod, main_mod.with_suffix('.sum'))}, before)
            self.assertEqual(source.read_bytes(), UPSTREAM)
            self.assertEqual((Path(report['overlayPath']).parent / 'go-webview2-v1.0.22' / language.SOURCE_FILE).read_bytes(), UPSTREAM)
            self.assertEqual(report['manifest']['originalSHA256'], language.SOURCE_SHA256)

    def test_invalid_cold_download_reports_preserve_application_and_module_cache(self):
        changes = [{'Path': 'example.invalid/other'}, {'Version': 'v1.0.23'}, {'Replace': {'Dir': '/somewhere'}},
                   {'Error': 'Download failed'}, {'Dir': ''}]
        for change in changes:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                module, source, downloaded, main_mod = self.module_fixture(root)
                cold = {key: value for key, value in downloaded.items() if key != 'Dir'}
                before = {path.name: path.read_bytes() for path in (main_mod, main_mod.with_suffix('.sum'))}
                scratch = root / 'scratch'
                with patch.object(language.subprocess, 'run', side_effect=self.discovery(cold, main_mod, {**downloaded, **change})):
                    with self.assertRaises(RuntimeError):
                        language.prepare_language_overlay(scratch)
                self.assertFalse(scratch.exists())
                self.assertEqual(source.read_bytes(), UPSTREAM)
                self.assertEqual({path.name: path.read_bytes() for path in (main_mod, main_mod.with_suffix('.sum'))}, before)

    def test_failed_or_timed_out_cold_download_preserves_sources_and_reports_failure(self):
        command = ['go', 'mod', 'download', '-json', f'{language.MODULE}@{language.VERSION}']
        failures = [subprocess.CalledProcessError(1, command, output='{"Error":"Unavailable"}', stderr='download unavailable'),
                    subprocess.TimeoutExpired(command, timeout=180)]
        for failure in failures:
            with self.subTest(failure=type(failure).__name__), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                _, source, downloaded, main_mod = self.module_fixture(root)
                cold = {key: value for key, value in downloaded.items() if key != 'Dir'}
                before = {path.name: path.read_bytes() for path in (main_mod, main_mod.with_suffix('.sum'))}
                with patch.object(language.subprocess, 'run', side_effect=[types.SimpleNamespace(stdout=json.dumps(cold)), failure]):
                    with self.assertRaisesRegex(RuntimeError, 'download'):
                        language.prepare_language_overlay(root / 'scratch')
                self.assertFalse((root / 'scratch').exists())
                self.assertEqual(source.read_bytes(), UPSTREAM)
                self.assertEqual({path.name: path.read_bytes() for path in (main_mod, main_mod.with_suffix('.sum'))}, before)

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
    def test_real_go_cold_cache_downloads_from_local_proxy_without_changing_locks(self):
        with tempfile.TemporaryDirectory(prefix='webview cold cache fixture ') as temporary:
            root = Path(temporary)
            app = root / 'application'
            app.mkdir()
            proxy = root / 'proxy'
            versions = proxy / language.MODULE / '@v'
            versions.mkdir(parents=True)
            module_content = f'module {language.MODULE}\n\ngo 1.20\n'
            (versions / f'{language.VERSION}.mod').write_text(module_content)
            (versions / f'{language.VERSION}.info').write_text(json.dumps({'Version': language.VERSION, 'Time': '2024-01-01T00:00:00Z'}))
            (versions / 'list').write_text(language.VERSION + '\n')
            with zipfile.ZipFile(versions / f'{language.VERSION}.zip', 'w') as archive:
                prefix = f'{language.MODULE}@{language.VERSION}/'
                archive.writestr(prefix + 'go.mod', module_content)
                archive.writestr(prefix + language.SOURCE_FILE, UPSTREAM)
            main_mod = app / 'go.mod'
            main_mod.write_text(f'module example.invalid/coldprobe\n\ngo 1.20\n\nrequire {language.MODULE} {language.VERSION}\n')
            # Only this fixture disables the checksum service: its local proxy
            # intentionally contains a tiny synthetic module, not upstream's ZIP.
            base_env = dict(os.environ, GOPROXY=proxy.as_uri(), GOSUMDB='off', GONOSUMDB='', GONOPROXY='',
                            GOPRIVATE='', GOWORK='off', GOTOOLCHAIN='local', GOFLAGS='-mod=readonly',
                            GOPATH=str(root / 'gopath'), GOCACHE=str(root / 'build cache'))
            seed_env = dict(base_env, GOMODCACHE=str(root / 'checksum seed cache'))
            seeded = subprocess.run(['go', 'mod', 'download', '-json', f'{language.MODULE}@{language.VERSION}'],
                                    cwd=app, env=seed_env, check=True, capture_output=True, text=True, timeout=30)
            sums = json.loads(seeded.stdout)
            main_sum = app / 'go.sum'
            main_sum.write_text(f'{language.MODULE} {language.VERSION} {sums["Sum"]}\n'
                                f'{language.MODULE} {language.VERSION}/go.mod {sums["GoModSum"]}\n')
            before = {path.name: path.read_bytes() for path in (main_mod, main_sum)}
            cold_cache = root / 'empty module cache'
            cold_cache.mkdir()
            self.assertEqual(list(cold_cache.iterdir()), [])
            cold_env = dict(base_env, GOMODCACHE=str(cold_cache))
            metadata = subprocess.run(['go', 'list', '-m', '-json', language.MODULE], cwd=app, env=cold_env,
                                      check=True, capture_output=True, text=True, timeout=30)
            selected = json.loads(metadata.stdout)
            self.assertEqual(selected['Version'], language.VERSION)
            self.assertFalse(selected.get('Dir'), 'Metadata-only discovery must reproduce the missing-source CI state')
            self.assertFalse((cold_cache / f'{language.MODULE}@{language.VERSION}').exists())
            self.assertEqual({path.name: path.read_bytes() for path in (main_mod, main_sum)}, before)
            helper = ("import json,sys;from pathlib import Path;"
                      "sys.path.insert(0,sys.argv[1]);"
                      "from windows_webview_language import prepare_language_overlay;"
                      "print(json.dumps(prepare_language_overlay(Path(sys.argv[2]))))")
            result = subprocess.run([sys.executable, '-c', helper, str(Path(__file__).resolve().parent), str(root / 'scratch')],
                                    cwd=app, env=cold_env, check=True, capture_output=True, text=True, timeout=30)
            report = json.loads(result.stdout)
            original = cold_cache / f'{language.MODULE}@{language.VERSION}' / language.SOURCE_FILE
            self.assertEqual(original.read_bytes(), UPSTREAM)
            self.assertEqual(hashlib.sha256(original.read_bytes()).hexdigest(), language.SOURCE_SHA256)
            self.assertTrue((cold_cache / 'cache/download' / language.MODULE / '@v' / f'{language.VERSION}.zip').is_file())
            overlay = json.loads(Path(report['overlayPath']).read_text())
            patched = Path(next(iter(overlay['Replace'].values()))).read_bytes()
            self.assertEqual(patched, language.patch_language_source(UPSTREAM))
            self.assertEqual({path.name: path.read_bytes() for path in (main_mod, main_sum)}, before)

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
