"""Offline regressions for the production pinned CAB/cache/extraction helper."""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import stat
import struct
import sys
import tempfile
import unittest
from unittest import mock
import zipfile

import webview2_runtime as runtime


def fake_pe(version='1.2.3.4', machine=0x8664):
    data = bytearray(256)
    data[:2] = b'MZ'
    struct.pack_into('<I', data, 0x3c, 64)
    data[64:68] = b'PE\0\0'
    struct.pack_into('<H', data, 68, machine)
    struct.pack_into('<H', data, 88, 0x20b)
    major, minor, build, patch = map(int, version.split('.'))
    struct.pack_into('<IIII', data, 128, 0xfeef04bd, 0x10000, major << 16 | minor, build << 16 | patch)
    return bytes(data)


def fixture(root, extra=None, omit=None, machine=0x8664):
    """The stub uses a tiny ZIP behind an MSCF marker; it never impersonates a real runtime."""
    directory = 'Microsoft.WebView2.FixedVersionRuntime.1.2.3.4.x64'
    files = {name: b'fixture data' for name in ['icudtl.dat', 'resources.pak', 'v8_context_snapshot.bin', 'Locales/en-US.pak', '1.2.3.4.manifest', 'show_third_party_software_licenses.bat', 'WidevineCdm/LICENSE', 'nested folder/extra.dat']}
    files.update({'msedgewebview2.exe': fake_pe(machine=machine), 'msedge.dll': fake_pe()})
    if omit:
        files.pop(omit)
    files.update(extra or {})
    output = io.BytesIO(b'MSCF')
    with zipfile.ZipFile(output, 'a') as archive:
        for name, value in files.items():
            archive.writestr(directory + '/' + name, value)
    archive = root / 'local runtime.cab'
    archive.write_bytes(output.getvalue())
    license_text = '<p>MICROSOFT EDGE WEBVIEW2 RUNTIME (FIXED VERSION)</p>'
    pin = {'version': '1.2.3.4', 'architecture': 'x64', 'filename': directory + '.cab', 'directory': directory, 'url': 'https://downloads.microsoft.com/fixture.cab', 'sha256': runtime.digest_file(archive), 'size': archive.stat().st_size, 'license_url': 'https://developer.microsoft.com/fixture-license', 'license_filename': 'WEBVIEW2-LICENSE.html', 'license_text': license_text, 'license_sha256': hashlib.sha256(license_text.encode()).hexdigest(), 'inventory_sha256': None, 'distribution_url': 'https://learn.microsoft.com/fixture', 'release_notes_url': 'https://learn.microsoft.com/fixture-release'}
    extractor = root / 'fixture 7z'
    extractor.write_text(f'#!{sys.executable}\n' + '''import os,pathlib,stat,sys,zipfile
args=sys.argv[1:]
with zipfile.ZipFile(args[-1]) as archive:
    if args[0]=='l':
        for entry in archive.infolist():
            print('Path = '+entry.filename)
            print('Size = '+str(entry.file_size))
            print('Attributes = A')
            if stat.S_ISLNK(entry.external_attr >> 16): print('Symbolic Link = outside')
            print()
    elif args[0]=='x':
        root=pathlib.Path(next(arg[2:] for arg in args if arg.startswith('-o')))
        for entry in archive.infolist():
            target=root/entry.filename
            target.parent.mkdir(parents=True,exist_ok=True)
            target.write_bytes(archive.read(entry))
        if os.environ.get('CRYPTO_TEST_EXTRACT_LINK'):
            (root/'injected-link').symlink_to('/tmp')
''')
    extractor.chmod(0o755)
    return archive, pin, extractor


class WebView2RuntimeTests(unittest.TestCase):
    def test_real_manifest_pins_official_fixed_x64_cab_inventory_and_license(self):
        pin = runtime.load_manifest()
        self.assertEqual(pin['architecture'], 'x64')
        self.assertTrue(pin['url'].startswith('https://msedge.sf.dl.delivery.mp.microsoft.com/'))
        self.assertEqual(len(pin['inventory_sha256']), 64)
        self.assertIn('FIXED VERSION', pin['license_text'])

    def test_offline_cab_preserves_every_file_and_flattens_only_official_top_folder(self):
        with tempfile.TemporaryDirectory(prefix='cryptodesk runtime path ') as temporary:
            root = Path(temporary)
            archive, pin, extractor = fixture(root)
            fetch = mock.Mock(side_effect=AssertionError('Offline build attempted network'))
            destination = root / 'portable package' / 'WebView2'
            report = runtime.prepare_runtime(root / 'cache', destination, archive, pin, extractor, fetch)
            self.assertTrue((destination / 'msedgewebview2.exe').is_file())
            self.assertEqual((destination / 'nested folder/extra.dat').read_bytes(), b'fixture data')
            self.assertEqual((destination / pin['license_filename']).read_text(), pin['license_text'])
            self.assertTrue((destination / runtime.PROVENANCE).is_file())
            self.assertEqual(report['file_count'], 11)
            self.assertFalse((destination / pin['directory']).exists())
            fetch.assert_not_called()

    def test_network_download_is_verified_and_cached(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, pin, extractor = fixture(root)
            def fetch(url, target, size):
                self.assertEqual((url, size), (pin['url'], pin['size']))
                shutil.copyfile(archive, target)
            download = mock.Mock(side_effect=fetch)
            runtime.prepare_runtime(root / 'cache', root / 'first', pin=pin, extractor=extractor, fetch=download)
            runtime.prepare_runtime(root / 'cache', root / 'second', pin=pin, extractor='missing-extractor', fetch=download)
            self.assertEqual(download.call_count, 1)
            self.assertEqual(runtime.digest_file(root / 'first/msedgewebview2.exe'), runtime.digest_file(root / 'second/msedgewebview2.exe'))

    def test_explicit_wrong_cab_is_rejected_even_when_verified_extraction_is_cached(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, pin, extractor = fixture(root)
            runtime.prepare_runtime(root / 'cache', root / 'first', archive, pin, extractor)
            archive.write_bytes(archive.read_bytes() + b'bad')
            with self.assertRaisesRegex(RuntimeError, 'SHA-256'):
                runtime.prepare_runtime(root / 'cache', root / 'second', archive, pin, extractor)
            self.assertFalse((root / 'second').exists())

    def test_corrupt_cached_cab_is_not_extracted_or_silently_redownloaded(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, pin, extractor = fixture(root)
            cache = root / 'cache'
            target = runtime.ensure_archive(cache, pin, archive)
            data = bytearray(target.read_bytes()); data[-1] ^= 1; target.write_bytes(data)
            fetch = mock.Mock()
            with self.assertRaisesRegex(RuntimeError, 'SHA-256'):
                runtime.prepare_runtime(cache, root / 'output', pin=pin, extractor=extractor, fetch=fetch)
            fetch.assert_not_called()
            self.assertFalse((cache / 'extracted').exists())

    def test_wrong_download_hash_never_enters_reusable_cache(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _, pin, extractor = fixture(root)
            def fetch(_url, target, _size):
                target.write_bytes(b'MSCFnot a matching archive')
            with self.assertRaisesRegex(RuntimeError, 'SHA-256'):
                runtime.prepare_runtime(root / 'cache', root / 'output', pin=pin, extractor=extractor, fetch=fetch)
            self.assertEqual(list((root / 'cache/downloads').iterdir()), [])
            self.assertFalse((root / 'output').exists())

    def test_path_validation_rejects_traversal_drives_ads_windows_aliases_and_other_roots(self):
        directory = 'runtime'
        for name in ['../escape', '/runtime/file', 'C:/runtime/file', 'runtime/../escape', r'runtime\..\escape', 'runtime/name:stream', 'runtime/CON.txt', 'runtime/trailing.', 'runtime/trailing ', 'runtime//name', 'runtime/line\nbreak', 'other/file']:
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                runtime.safe_archive_path(name, directory)
        self.assertEqual(runtime.safe_archive_path(r'runtime\Locales\en-US.pak', directory), 'runtime/Locales/en-US.pak')

    def test_verified_archive_traversal_is_rejected_before_extraction(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, pin, extractor = fixture(root, extra={'../escape': b'unsafe'})
            with self.assertRaisesRegex(RuntimeError, 'Unsafe path'):
                runtime.prepare_runtime(root / 'cache', root / 'output', archive, pin, extractor)
            self.assertFalse((root / 'cache/extracted/escape').exists())
            self.assertFalse((root / 'output').exists())

    def test_windows_case_collisions_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, pin, extractor = fixture(root, extra={'Locales/EN-US.pak': b'duplicate'})
            with self.assertRaisesRegex(RuntimeError, 'Duplicate Windows path'):
                runtime.prepare_runtime(root / 'cache', root / 'output', archive, pin, extractor)

    def test_archive_link_and_extractor_created_link_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, pin, extractor = fixture(root)
            with zipfile.ZipFile(archive, 'a') as bundle:
                link = zipfile.ZipInfo(pin['directory'] + '/bad-link')
                link.create_system, link.external_attr = 3, (stat.S_IFLNK | 0o777) << 16
                bundle.writestr(link, '../../escape')
            pin['size'], pin['sha256'] = archive.stat().st_size, runtime.digest_file(archive)
            with self.assertRaisesRegex(RuntimeError, 'Links are not permitted'):
                runtime.prepare_runtime(root / 'first-cache', root / 'first', archive, pin, extractor)
            archive, pin, extractor = fixture(root)
            with mock.patch.dict(os.environ, {'CRYPTO_TEST_EXTRACT_LINK': '1'}), self.assertRaisesRegex(RuntimeError, 'Unexpected link'):
                runtime.prepare_runtime(root / 'second-cache', root / 'second', archive, pin, extractor)
            self.assertFalse((root / 'second').exists())

    def test_incomplete_and_wrong_architecture_runtimes_are_not_published(self):
        for options, message in [({'omit': 'Locales/en-US.pak'}, 'incomplete'), ({'machine': 0x14c}, 'x64')]:
            with self.subTest(options=options), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                archive, pin, extractor = fixture(root, **options)
                with self.assertRaisesRegex(RuntimeError, message):
                    runtime.prepare_runtime(root / 'cache', root / 'output', archive, pin, extractor)
                self.assertFalse((root / 'output').exists())

    def test_cached_file_content_tampering_is_detected_even_at_the_same_size(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, pin, extractor = fixture(root)
            report = runtime.prepare_runtime(root / 'cache', root / 'first', archive, pin, extractor)
            pin['inventory_sha256'] = report['inventory_sha256']
            cached = root / 'cache/extracted' / pin['directory'] / 'nested folder/extra.dat'
            cached.write_bytes(b'corrupt data')
            with self.assertRaisesRegex(RuntimeError, 'inventory does not match'):
                runtime.prepare_runtime(root / 'cache', root / 'second', pin=pin, extractor=extractor)
            self.assertFalse((root / 'second').exists())

    def test_wrong_pe_version_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            executable = Path(temporary) / 'runtime.exe'
            executable.write_bytes(fake_pe('9.9.9.9'))
            with self.assertRaisesRegex(RuntimeError, 'version does not match'):
                runtime.validate_pe(executable, '1.2.3.4')

    def test_existing_destination_files_are_preserved_and_missing_extractor_is_actionable(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _, pin, _ = fixture(root)
            destination = root / 'existing'
            destination.mkdir(); (destination / 'user.txt').write_text('keep')
            with self.assertRaisesRegex(RuntimeError, 'Refusing to replace'):
                runtime.prepare_runtime(root / 'cache', destination, pin=pin)
            self.assertEqual((destination / 'user.txt').read_text(), 'keep')
            with mock.patch.object(runtime.shutil, 'which', return_value=None), self.assertRaisesRegex(RuntimeError, '7-Zip.*No system packages were installed'):
                runtime.prepare_runtime(root / 'cache', root / 'new', pin=pin)

    def test_real_license_text_and_runtime_version_pin_cannot_be_changed_without_hashes(self):
        with tempfile.TemporaryDirectory() as temporary:
            pin = runtime.load_manifest()
            pin['license_text'] += 'modified'
            path = Path(temporary) / 'pin.json'
            path.write_text(json.dumps(pin))
            with self.assertRaisesRegex(RuntimeError, 'license is missing or corrupt'):
                runtime.load_manifest(path)


if __name__ == '__main__':
    unittest.main()
