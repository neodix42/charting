"""Offline regressions for reviewed SDK downloads and our release-only bridge JAR."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import zipfile

import jforex_bundle as bundle


def entry(data=b'explicit offline test artifact'):
    return dict(name='offline-test.jar', url='https://repo.maven.apache.org/maven2/offline-test.jar',
                bytes=len(data), sha256=hashlib.sha256(data).hexdigest())


def make_bundle(path):
    path.mkdir(parents=True)
    with tempfile.TemporaryDirectory() as temporary:
        classes = Path(temporary)
        (classes / 'cryptodesk/jforex').mkdir(parents=True)
        (classes / 'cryptodesk/jforex/Bridge.class').write_bytes(b'explicit fake class fixture')
        bundle.archive_jar(classes, path / bundle.JAR)
    (path / bundle.ARTIFACTS).write_bytes((bundle.SOURCE / bundle.ARTIFACTS).read_bytes())
    (path / 'LICENSE').write_text('explicit license fixture')
    (path / 'THIRD_PARTY_NOTICES.md').write_text('explicit notice fixture')
    manifest = dict(version=1, sdkVersion=bundle.SDK_VERSION, apiVersion=bundle.API_VERSION,
                    javaMinimum=17, mainClass=bundle.MAIN_CLASS,
                    files={item.name: dict(bytes=item.stat().st_size, sha256=bundle.sha256(item))
                           for item in path.iterdir()})
    (path / bundle.MANIFEST).write_text(json.dumps(manifest))
    bundle.validate_directory(path)
    return path


class JForexBundleTests(unittest.TestCase):
    def test_checked_in_manifest_pins_complete_sdk_without_sources(self):
        entries = bundle.artifacts()
        self.assertEqual(len(entries), 51)
        self.assertEqual(sum(item['bytes'] for item in entries), 42895078)
        self.assertTrue(all(item['url'].startswith('https://') for item in entries))
        self.assertFalse(any(item['name'].endswith('-sources.jar') for item in entries))

    def test_verified_cached_artifacts_need_no_network(self):
        data = b'explicit offline test artifact'
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'offline-test.jar').write_bytes(data)
            fetch = mock.Mock(side_effect=AssertionError('cached SDK must not use network'))
            bundle.materialize_sdk(root, [entry(data)], fetch)
            fetch.assert_not_called()

    def test_corrupt_cached_artifact_is_never_accepted_or_executed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'offline-test.jar').write_bytes(b'corrupt')
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                bundle.materialize_sdk(root, [entry()], mock.Mock())
            self.assertEqual((root / 'offline-test.jar').read_bytes(), b'corrupt')

    def test_partial_bad_download_does_not_publish_artifact(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            def fetch(url, destination, size):
                destination.write_bytes(b'bad')
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                bundle.materialize_sdk(root, [entry()], fetch)
            self.assertEqual(list(root.iterdir()), [])

    def test_successful_download_atomically_installs_exact_pin(self):
        data = b'explicit offline test artifact'
        with tempfile.TemporaryDirectory(prefix='cryptodesk jforex ') as temporary:
            root = Path(temporary)
            def fetch(url, destination, size):
                self.assertTrue(url.startswith('https://'))
                self.assertEqual(size, len(data))
                destination.write_bytes(data)
            bundle.materialize_sdk(root, [entry(data)], fetch)
            self.assertEqual((root / 'offline-test.jar').read_bytes(), data)
            self.assertEqual(len(list(root.iterdir())), 1)

    def test_artifact_manifest_rejects_unsafe_origin_and_names(self):
        original = json.loads((bundle.SOURCE / bundle.ARTIFACTS).read_text())
        for mutation in [dict(name='../SDK.jar'), dict(url='http://www.dukascopy.com/a.jar'),
                         dict(url='https://unreviewed.example/a.jar'), dict(sha256='0' * 63),
                         dict(bytes=0), dict(name='api-sources.jar')]:
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                manifest = json.loads(json.dumps(original))
                manifest['artifacts'][0].update(mutation)
                (Path(temporary) / bundle.ARTIFACTS).write_text(json.dumps(manifest))
                with self.assertRaisesRegex(ValueError, 'Unsafe'):
                    bundle.artifacts(Path(temporary))

    def test_jar_is_deterministic_and_has_no_vendor_classes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            classes = root / 'classes'; (classes / 'cryptodesk/jforex').mkdir(parents=True)
            (classes / 'cryptodesk/jforex/Bridge.class').write_bytes(b'explicit fake class fixture')
            first, second = root / 'first.jar', root / 'second.jar'
            bundle.archive_jar(classes, first); bundle.archive_jar(classes, second)
            self.assertEqual(first.read_bytes(), second.read_bytes())
            with zipfile.ZipFile(first) as archive:
                self.assertIn(bundle.MAIN_CLASS.encode(), archive.read('META-INF/MANIFEST.MF'))
                self.assertEqual(set(archive.namelist()), {'META-INF/MANIFEST.MF', 'cryptodesk/jforex/Bridge.class'})

    def test_copy_runtime_does_not_download_or_require_compiler(self):
        with tempfile.TemporaryDirectory(prefix='cryptodesk jforex copy ') as temporary:
            root = Path(temporary)
            source = make_bundle(root / 'source')
            with mock.patch.object(bundle, 'materialize_sdk', side_effect=AssertionError('copy must stay offline')):
                bundle.copy_runtime(source, root / 'destination')
            self.assertEqual((source / bundle.JAR).read_bytes(), (root / 'destination' / bundle.JAR).read_bytes())
            self.assertEqual(set(item.name for item in (root / 'destination').iterdir()), bundle.REQUIRED | {bundle.MANIFEST})

    def test_copy_never_erases_unrelated_destination(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = make_bundle(root / 'source')
            destination = root / 'unrelated'; destination.mkdir()
            (destination / 'user-file').write_text('preserve this')
            with self.assertRaises(FileNotFoundError):
                bundle.copy_runtime(source, destination)
            self.assertEqual((destination / 'user-file').read_text(), 'preserve this')

    def test_failed_promotion_restores_previous_validated_bundle(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            stage, destination = make_bundle(root / 'stage'), make_bundle(root / 'destination')
            original = Path.replace
            def replace(path, target):
                if path == stage:
                    raise OSError('explicit promotion failure')
                return original(path, target)
            with mock.patch.object(Path, 'replace', replace), self.assertRaisesRegex(OSError, 'promotion failure'):
                bundle.publish_directory(stage, destination)
            bundle.validate_directory(destination)
            self.assertFalse((root / 'previous-runtime').exists())

    def test_release_inventory_rejects_vendor_jars_and_content_changes(self):
        contents = {bundle.JAR: b'jar placeholder', bundle.ARTIFACTS: b'{}',
                    'LICENSE': b'license', 'THIRD_PARTY_NOTICES.md': b'notice'}
        manifest = dict(version=1, sdkVersion=bundle.SDK_VERSION, apiVersion=bundle.API_VERSION,
                        javaMinimum=17, mainClass=bundle.MAIN_CLASS,
                        files={name: dict(bytes=len(data), sha256=hashlib.sha256(data).hexdigest())
                               for name, data in contents.items()})
        files = set(contents) | {bundle.MANIFEST, 'lib/vendor.jar'}
        with self.assertRaisesRegex(ValueError, 'vendor SDK'):
            bundle.validate_inventory(manifest, files, contents.__getitem__)
        contents['LICENSE'] = b'changed license'
        with self.assertRaisesRegex(ValueError, 'checksum'):
            bundle.validate_inventory(manifest, set(contents) | {bundle.MANIFEST}, contents.__getitem__)


if __name__ == '__main__':
    unittest.main()
