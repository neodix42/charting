"""SDK cache transfers remain pinned, complete and separate from product archives."""
import hashlib
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

import jforex_cache as cache


class SDKCacheTests(unittest.TestCase):
    def entries(self):
        return [dict(name='sdk.jar', bytes=3, sha256=hashlib.sha256(b'sdk').hexdigest()),
                dict(name='dependency.jar', bytes=3, sha256=hashlib.sha256(b'dep').hexdigest())]

    def archive(self, root, records):
        path = root / 'transfer.tar.gz'
        with tarfile.open(path, 'w:gz') as output:
            for name, data, kind in records:
                member = tarfile.TarInfo(name)
                member.size, member.type = len(data), kind
                output.addfile(member, io.BytesIO(data))
        return path

    def test_round_trip_retains_local_files_and_restores_only_the_reviewed_inventory(self):
        with tempfile.TemporaryDirectory() as temporary, mock.patch.object(cache.sdk, 'artifacts', return_value=self.entries()):
            root = Path(temporary); original = root / 'local'; original.mkdir()
            for name, data in [('sdk.jar',b'sdk'), ('dependency.jar',b'dep'), ('older.jar',b'retained')]:
                (original / name).write_bytes(data)
            transfer = root / 'cache.tar.gz'
            cache.export_cache(original, transfer)
            cache.import_cache(transfer, root / 'restored', cache.sdk.sha256(transfer))
            self.assertEqual(sorted(path.name for path in (root / 'restored').iterdir()), ['dependency.jar','sdk.jar'])
            self.assertEqual((original / 'older.jar').read_bytes(), b'retained')
            self.assertEqual((root / 'restored/sdk.jar').read_bytes(), b'sdk')

    def test_bad_transfers_never_change_an_existing_cache(self):
        cases = [ [('sdk.jar',b'sdk',tarfile.REGTYPE)],
                  [('sdk.jar',b'bad',tarfile.REGTYPE),('dependency.jar',b'dep',tarfile.REGTYPE)],
                  [('../sdk.jar',b'sdk',tarfile.REGTYPE)],
                  [('sdk.jar',b'sdk',tarfile.SYMTYPE)],
                  [('sdk.jar',b'sdk',tarfile.REGTYPE),('sdk.jar',b'sdk',tarfile.REGTYPE)] ]
        for records in cases:
            with self.subTest(records=records), tempfile.TemporaryDirectory() as temporary, mock.patch.object(cache.sdk, 'artifacts', return_value=self.entries()):
                root = Path(temporary); target = root / 'cache'; target.mkdir(); (target / 'sdk.jar').write_bytes(b'sdk')
                path = self.archive(root, records)
                with self.assertRaises(ValueError):
                    cache.import_cache(path, target, cache.sdk.sha256(path))
                self.assertEqual(list(target.iterdir()), [target / 'sdk.jar'])
                self.assertEqual((target / 'sdk.jar').read_bytes(), b'sdk')

    def test_archive_checksum_and_existing_corruption_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary, mock.patch.object(cache.sdk, 'artifacts', return_value=self.entries()):
            root = Path(temporary)
            path = self.archive(root,[('sdk.jar',b'sdk',tarfile.REGTYPE),('dependency.jar',b'dep',tarfile.REGTYPE)])
            with self.assertRaisesRegex(ValueError,'transfer checksum'):
                cache.import_cache(path, root / 'cache', '0'*64)
            target = root / 'cache'; target.mkdir(); (target / 'sdk.jar').write_bytes(b'bad')
            with self.assertRaisesRegex(ValueError,'original preserved'):
                cache.import_cache(path, target, cache.sdk.sha256(path))
            self.assertEqual((target / 'sdk.jar').read_bytes(), b'bad')

    def test_export_requires_a_complete_verified_copy_and_never_fetches(self):
        with tempfile.TemporaryDirectory() as temporary, mock.patch.object(cache.sdk, 'artifacts', return_value=self.entries()), mock.patch.object(cache.sdk, 'download', side_effect=AssertionError('no download')):
            root = Path(temporary); (root / 'sdk.jar').write_bytes(b'sdk')
            with self.assertRaisesRegex(ValueError,'Verified local SDK artifact required'):
                cache.export_cache(root, root / 'cache.tar.gz')
            self.assertFalse((root / 'cache.tar.gz').exists())


if __name__ == '__main__':
    unittest.main()
