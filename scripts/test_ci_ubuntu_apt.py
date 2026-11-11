import contextlib
import io
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import ci_ubuntu_apt as apt


class CIUbuntuAPTTests(unittest.TestCase):
    def test_runner_mirrorlist_and_deb822_preserve_suites_keys_and_other_repositories(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sources = root / "sources.list.d"
            sources.mkdir()
            ubuntu = sources / "ubuntu.sources"
            ubuntu.write_text("Types: deb\nURIs: mirror+file:/etc/apt/apt-mirrors.txt\nSuites: noble noble-updates noble-security\nComponents: main universe\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n")
            microsoft = sources / "microsoft.list"
            foreign = "deb [arch=amd64 signed-by=/keys/microsoft.gpg] https://packages.microsoft.com/ubuntu/24.04/prod noble main\n"
            microsoft.write_text(foreign)
            before = ubuntu.read_text()
            apt.configure(root)
            self.assertEqual(ubuntu.read_text(), before.replace("mirror+file:/etc/apt/apt-mirrors.txt", apt.ARCHIVE))
            self.assertEqual(microsoft.read_text(), foreign)
            # Repeated setup is harmless and retains signed repository options.
            apt.configure(root)
            self.assertEqual(microsoft.read_text(), foreign)
            parser = shutil.which("apt-config")
            if parser:
                result = subprocess.run([parser, "-c", str(root / "apt.conf.d/99cryptodesk-ci"), "dump"], text=True, capture_output=True, check=True)
                self.assertIn('APT::Update::Error-Mode "any";', result.stdout)
                self.assertIn('Acquire::https::Timeout "15";', result.stdout)

    def test_legacy_azure_and_security_sources_keep_architecture_and_signing(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "sources.list"
            source.write_text("deb [arch=amd64 signed-by=/keys/ubuntu.gpg] http://azure.archive.ubuntu.com/ubuntu noble main\ndeb http://security.ubuntu.com/ubuntu/ noble-security main\n")
            apt.configure(root)
            self.assertEqual(source.read_text(), "deb [arch=amd64 signed-by=/keys/ubuntu.gpg] https://archive.ubuntu.com/ubuntu noble main\ndeb https://security.ubuntu.com/ubuntu/ noble-security main\n")

    def test_normal_local_invocation_refuses_before_configuration(self):
        for environment in ({}, {"GITHUB_ACTIONS": "true", "RUNNER_OS": "Windows"}):
            with self.subTest(environment=environment), patch.dict("os.environ", environment, clear=True), patch.object(apt, "configure") as configure, contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(SystemExit):
                    apt.main()
                configure.assert_not_called()

    def test_unknown_runner_sources_fail_without_adding_apt_configuration(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "sources.list"
            original = "deb https://example.com/custom stable main\n"
            source.write_text(original)
            with self.assertRaisesRegex(RuntimeError, "No supported Ubuntu"):
                apt.configure(root)
            self.assertEqual(source.read_text(), original)
            self.assertFalse((root / "apt.conf.d/99cryptodesk-ci").exists())


if __name__ == "__main__":
    unittest.main()
