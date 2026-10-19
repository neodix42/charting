"""Offline regressions for the production Node/npm resolver and verified installer."""
import hashlib
import io
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

import node_toolchain as toolchain


def make_tools(directory, node='v24.19.0', npm='11.17.0'):
    directory.mkdir(parents=True)
    for name, version in [('node', node), ('npm', npm)]:
        if version is None:
            continue
        path = directory / name
        path.write_text(f'#!/bin/sh\nprintf "%s\\n" "{version}"\n')
        path.chmod(0o755)


class NodeToolchainTests(unittest.TestCase):
    def test_missing_npm_does_not_accept_node_alone(self):
        with tempfile.TemporaryDirectory() as temporary:
            binary = Path(temporary) / 'bin'
            make_tools(binary, npm=None)
            self.assertIsNone(toolchain.usable(str(binary)))

    def test_existing_compatible_path_avoids_download(self):
        with tempfile.TemporaryDirectory(prefix='cryptodesk tool path ') as temporary:
            root = Path(temporary)
            binary = root / 'bin'
            make_tools(binary)
            with mock.patch.object(toolchain, 'install') as install:
                self.assertEqual(toolchain.resolve(root, str(binary)), str(binary))
                install.assert_not_called()

    def test_relative_path_is_normalized_for_wails_frontend_directory(self):
        original = Path.cwd()
        with tempfile.TemporaryDirectory() as temporary:
            try:
                os.chdir(temporary)
                make_tools(Path('bin'))
                self.assertEqual(toolchain.usable('bin'), str(Path(temporary) / 'bin'))
            finally:
                os.chdir(original)

    def test_cached_local_tools_work_without_network_or_path_npm(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            arch = toolchain.ARCHIVES[toolchain.platform.machine()][0]
            binary = root / '.tools' / f'node-v{toolchain.NODE_VERSION}-linux-{arch}' / 'bin'
            make_tools(binary)
            with mock.patch.object(toolchain, 'install') as install:
                self.assertEqual(toolchain.resolve(root, ''), str(binary))
                install.assert_not_called()

    def test_unsupported_node_and_broken_npm_are_rejected(self):
        for version, npm in [('v18.20.0', '10.0.0'), ('v22.12.0', '10.0.0'),
                             ('v25.0.0', '11.0.0'), ('v24.19.0', 'bad version')]:
            with self.subTest(node=version, npm=npm), tempfile.TemporaryDirectory() as temporary:
                binary = Path(temporary) / 'bin'
                make_tools(binary, version, npm)
                self.assertIsNone(toolchain.usable(str(binary)))

    def test_checksum_failure_never_installs_or_executes_archive(self):
        with tempfile.TemporaryDirectory() as temporary:
            tools = Path(temporary) / '.tools'

            def bad_download(url, destination):
                destination.write_bytes(b'not an archive')
                return hashlib.sha256(destination.read_bytes()).hexdigest()

            with self.assertRaisesRegex(RuntimeError, 'SHA-256'):
                toolchain.install(tools, 'node-test.tar.xz', '0' * 64, bad_download)
            self.assertEqual(list(tools.iterdir()), [])

    def test_verified_archive_installs_in_path_with_spaces(self):
        name = 'node-test'
        archive_bytes = io.BytesIO()
        with tarfile.open(fileobj=archive_bytes, mode='w:xz') as archive:
            for executable, version in [('node', 'v24.19.0'), ('npm', '11.17.0')]:
                data = f'#!/bin/sh\nprintf "%s\\n" "{version}"\n'.encode()
                item = tarfile.TarInfo(f'{name}/bin/{executable}')
                item.mode, item.size = 0o755, len(data)
                archive.addfile(item, io.BytesIO(data))
        data = archive_bytes.getvalue()

        def fetch(url, destination):
            self.assertTrue(url.startswith('https://nodejs.org/'))
            destination.write_bytes(data)
            return hashlib.sha256(data).hexdigest()

        with tempfile.TemporaryDirectory(prefix='cryptodesk install tools ') as temporary:
            tools = Path(temporary) / '.tools'
            binary = toolchain.install(tools, f'{name}.tar.xz', hashlib.sha256(data).hexdigest(), fetch)
            self.assertEqual(toolchain.usable(str(binary)), str(binary))
            self.assertEqual(list(tools.iterdir()), [binary.parent])


if __name__ == '__main__':
    unittest.main()
