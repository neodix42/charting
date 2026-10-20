"""Build metadata uses one explicit, injection-safe application version."""
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from app_version import app_version, parse_version
from windows_build import set_snapshot_version


class AppVersionTests(unittest.TestCase):
    def test_version_file_and_environment_override(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "VERSION").write_text("1.2.3\n")
            self.assertEqual(app_version(root, {}), "1.2.3")
            self.assertEqual(app_version(root, {"CRYPTODESK_VERSION": "2.4.17"}), "2.4.17")
            with patch.dict(os.environ, {"CRYPTODESK_VERSION": "3.0.9"}):
                self.assertEqual(app_version(root), "3.0.9")

    def test_unsafe_noncanonical_or_unrepresentable_versions_fail(self):
        for value in [None, "", "v1.2.3", "01.2.3", "1.2", "1.2.3-beta", "1.2.3\n", "1.2.3;echo bad", "1.2.65536", "-1.2.3", "1. 2.3"]:
            with self.subTest(version=value), self.assertRaises(ValueError):
                parse_version(value)
        self.assertEqual(parse_version("0.65535.17"), (0, 65535, 17))

    def test_windows_resource_version_changes_only_the_snapshot(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            original = {"info": {"productVersion": "0.1.0", "companyName": "CryptoDesk"}, "outputfilename": "cryptodesk"}
            checkout = root / "wails.json"
            checkout.write_text(json.dumps(original))
            snapshot = root / "snapshot"
            snapshot.mkdir()
            (snapshot / "wails.json").write_bytes(checkout.read_bytes())
            set_snapshot_version(snapshot, "1.3.42")
            self.assertEqual(json.loads(checkout.read_text()), original)
            updated = json.loads((snapshot / "wails.json").read_text())
            self.assertEqual(updated["info"]["productVersion"], "1.3.42")
            self.assertEqual(updated["info"]["companyName"], "CryptoDesk")
            self.assertEqual(updated["outputfilename"], "cryptodesk")


if __name__ == "__main__":
    unittest.main()
