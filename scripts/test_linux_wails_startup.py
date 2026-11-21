"""Verify the narrowly pinned startup overlay and untouched caller build inputs."""
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
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('linux_wails_startup', Path(__file__).with_name('linux_wails_startup.py'))
startup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(startup)

UPSTREAM = b'package linux\nfunc (w *Window) Run(url string) {\n' + startup.PATCH_SITE + b'\tw.Center()\n}\n'


class LinuxWailsStartupTests(unittest.TestCase):
    def fixtures(self, root):
        module = root / 'read only module cache'
        source = module / startup.SOURCE_FILE
        source.parent.mkdir(parents=True)
        source.write_bytes(UPSTREAM)
        source.chmod(0o444)
        (module / 'go.mod').write_text(f'module {startup.MODULE}\n\ngo 1.22\n')
        app = root / 'application checkout'
        app.mkdir()
        mod = app / 'go.mod'
        mod.write_text(f'module example.invalid/startup\n\ngo 1.22\nrequire {startup.MODULE} {startup.VERSION}\n')
        mod.with_suffix('.sum').write_text('original sums\n')
        return module, source, app, mod

    def discovery(self, module, mod, version=None, replace=None):
        def invoke(command, **kwargs):
            if command == ['go', 'list', '-m', '-json', startup.MODULE]:
                document = {'Path': startup.MODULE, 'Version': version or startup.VERSION, 'Dir': str(module)}
                if replace:
                    document['Replace'] = replace
                return types.SimpleNamespace(stdout=json.dumps(document))
            self.assertEqual(command, ['go', 'env', 'GOMOD'])
            return types.SimpleNamespace(stdout=str(mod) + '\n')
        return invoke

    def test_patch_changes_only_the_hidden_maximized_branch_and_refuses_unknown_source(self):
        with patch.object(startup, 'SOURCE_SHA256', hashlib.sha256(UPSTREAM).hexdigest()):
            result = startup.patch_startup_source(UPSTREAM)
            self.assertEqual(result.replace(startup.INSERTION, b'', 1), UPSTREAM)
            self.assertIn(b'StartHidden && w.appoptions.WindowStartState == options.Maximised', result)
            branch = result.split(startup.PATCH_SITE)[0]
            self.assertIn(b'C.gtk_widget_show_all(w.vbox)', branch)
            self.assertIn(b'C.gtk_widget_realize(w.asGTKWidget())', branch)
            self.assertLess(branch.index(b'C.gtk_widget_realize(w.asGTKWidget())'), branch.index(b'C.gtk_widget_show_all(w.vbox)'))
            self.assertIn(b'C.gtk_window_maximize(w.asGTKWindow())', branch)
            self.assertIn(b'\t\treturn\n', branch)
            self.assertNotIn(b'C.gtk_widget_show_all(w.asGTKWidget())', branch)
            with self.assertRaisesRegex(RuntimeError, 'SHA-256'):
                startup.patch_startup_source(UPSTREAM + b'unknown changes')

    def test_custom_flags_modfile_sum_and_overlay_are_preserved_without_source_mutation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            module, source, app, mod = self.fixtures(root)
            custom_mod = app / 'custom app.mod'
            custom_mod.write_bytes(mod.read_bytes() + b'// selected custom module\n')
            custom_mod.with_suffix('.sum').write_text('selected custom sums\n')
            other_source, other_patch = app / 'other.go', app / 'other patched.go'
            overlay = app / 'caller overlay.json'
            overlay.write_text(json.dumps({'Replace': {str(other_source): str(other_patch)}}))
            flags = '-race "-gcflags=all=-N -l" ' + startup.quote_go_flag('-modfile=' + str(custom_mod)) + ' ' + startup.quote_go_flag('-overlay=' + str(overlay))
            environment = {'GOFLAGS': flags, 'UNCHANGED': 'value'}
            baseline = {path: path.read_bytes() for path in (source, mod, mod.with_suffix('.sum'), custom_mod, custom_mod.with_suffix('.sum'), overlay)}
            with patch.object(startup, 'SOURCE_SHA256', hashlib.sha256(UPSTREAM).hexdigest()), patch.object(startup.subprocess, 'run', side_effect=self.discovery(module, mod)):
                result = startup.prepare_startup_overlay(root / 'scratch', environment, app)
            updated_flags = startup.split_go_flags(result['goFlags'])
            self.assertEqual(updated_flags[:2], ['-race', '-gcflags=all=-N -l'])
            output_mod = Path(next(value.removeprefix('-modfile=') for value in updated_flags if value.startswith('-modfile=')))
            self.assertTrue(output_mod.read_bytes().startswith(custom_mod.read_bytes()))
            self.assertEqual(output_mod.with_suffix('.sum').read_text(), 'selected custom sums\n')
            output_overlay = Path(next(value.removeprefix('-overlay=') for value in updated_flags if value.startswith('-overlay=')))
            replacements = json.loads(output_overlay.read_text())['Replace']
            self.assertEqual(replacements[str(other_source)], str(other_patch))
            copied_source = root / 'scratch/wails-v2.17.0' / startup.SOURCE_FILE
            self.assertEqual(copied_source.read_bytes(), UPSTREAM)
            self.assertEqual(Path(replacements[str(copied_source)]).read_bytes().replace(startup.INSERTION, b'', 1), UPSTREAM)
            self.assertEqual(environment, {'GOFLAGS': flags, 'UNCHANGED': 'value'})
            self.assertEqual(baseline, {path: path.read_bytes() for path in baseline})
            self.assertNotIn(str(root), json.dumps(result['manifest']))

    def test_conflicting_existing_overlay_and_unpinned_replacement_are_not_overridden(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            module, source, app, mod = self.fixtures(root)
            overlay = app / 'overlay.json'
            overlay.write_text(json.dumps({'Replace': {str(source): str(app / 'user-window.go')}}))
            with patch.object(startup, 'SOURCE_SHA256', hashlib.sha256(UPSTREAM).hexdigest()), patch.object(startup.subprocess, 'run', side_effect=self.discovery(module, mod)):
                with self.assertRaisesRegex(RuntimeError, 'already changes'):
                    startup.prepare_startup_overlay(root / 'scratch', {'GOFLAGS': startup.quote_go_flag('-overlay=' + str(overlay))}, app)
            self.assertFalse((root / 'scratch').exists())
            with patch.object(startup.subprocess, 'run', side_effect=self.discovery(module, mod, replace={'Dir': str(module)})):
                with self.assertRaisesRegex(RuntimeError, 'unmodified pinned'):
                    startup.prepare_startup_overlay(root / 'scratch', {}, app)

    def test_build_view_does_not_replace_existing_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            module, source, app, mod = self.fixtures(root)
            scratch = root / 'scratch'
            scratch.mkdir()
            (scratch / 'window.go').write_text('user data')
            with patch.object(startup, 'SOURCE_SHA256', hashlib.sha256(UPSTREAM).hexdigest()), patch.object(startup.subprocess, 'run', side_effect=self.discovery(module, mod)):
                with self.assertRaisesRegex(RuntimeError, 'Refusing to replace'):
                    startup.prepare_startup_overlay(scratch, {}, app)
            self.assertEqual((scratch / 'window.go').read_text(), 'user data')

    def test_go_flag_quoting_preserves_spaces_and_does_not_expand_shell_text(self):
        fields = ['-race', '-gcflags=all=-N -l', '-modfile=/tmp/a b/$(`literal`).mod']
        self.assertEqual(startup.split_go_flags(' '.join(map(startup.quote_go_flag, fields))), fields)
        self.assertEqual(startup.split_go_flags("'-race'-mod=readonly"), ['-race', '-mod=readonly'])
        self.assertEqual(startup.split_go_flags('-tags=one\u00a0two'), ['-tags=one\u00a0two'])
        for value in ["'unterminated", '"unterminated']:
            with self.assertRaises(ValueError):
                startup.split_go_flags(value)

    def test_wails_compiler_applies_overlay_to_build_but_not_module_disabled_probes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            compiler = root / 'selected-go'
            compiler.write_text('#!' + sys.executable + '\nimport json,os,sys\nprint(json.dumps({"args":sys.argv[1:],"flags":os.environ.get("GOFLAGS")}))\n')
            compiler.chmod(0o755)
            command = startup.wails_compiler_command(['wails', 'build', '-compiler', str(compiler), '-s'], root, '-race -modfile=/temporary/app.mod -overlay=/temporary/overlay.json')
            self.assertEqual(command[:3], ['wails', 'build', '-s'])
            wrapper = Path(command[-1])
            original = '-tags=user-tag'
            for arguments, disabled, expected in [(['build', '.'], False, '-race -modfile=/temporary/app.mod -overlay=/temporary/overlay.json'), (['version'], True, original), (['env', 'GOVERSION'], True, original), (['mod', 'tidy'], False, original), (['build', '.'], True, original)]:
                environment = dict(os.environ, GOFLAGS=original, GO111MODULE='off' if disabled else 'on')
                report = json.loads(subprocess.run([str(wrapper), *arguments], env=environment, capture_output=True, text=True, check=True).stdout)
                self.assertEqual(report, {'args': arguments, 'flags': expected})

    @unittest.skipUnless(shutil.which('go'), 'Go is unavailable')
    def test_actual_selected_pinned_source_matches_reviewed_hash_offline(self):
        # Go list does not install anything; missing uncached source remains a
        # separate build prerequisite rather than fetching from this unit test.
        environment = dict(os.environ, GOPROXY='off', GOSUMDB='off', GOFLAGS='')
        try:
            result = subprocess.run(['go', 'list', '-m', '-json', startup.MODULE], cwd=Path(__file__).resolve().parent.parent, env=environment, capture_output=True, text=True, check=True, timeout=30)
            module = json.loads(result.stdout)
            source = Path(module['Dir']) / startup.SOURCE_FILE
            original = source.read_bytes()
        except (subprocess.SubprocessError, OSError, KeyError):
            self.skipTest('The pinned Wails source is not locally available')
        self.assertEqual(module['Version'], startup.VERSION)
        self.assertEqual(hashlib.sha256(original).hexdigest(), startup.SOURCE_SHA256)
        self.assertEqual(startup.patch_startup_source(original).replace(startup.INSERTION, b'', 1), original)


if __name__ == '__main__':
    unittest.main()
