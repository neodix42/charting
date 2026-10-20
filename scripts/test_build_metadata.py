"""Source flags describe input files, before Wails' temporary regeneration."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from build_metadata import captured_source, source_provenance


class SourceProvenanceTests(unittest.TestCase):
    def test_clean_capture_stays_clean_during_temporary_generated_file_changes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for arguments in [("init", "-q"), ("config", "user.name", "Fixture"), ("config", "user.email", "fixture@example.invalid")]:
                subprocess.run(["git", *arguments], cwd=root, check=True)
            source = root / "generated.js"
            source.write_text("Original checked-in wrapper")
            subprocess.run(["git", "add", "generated.js"], cwd=root, check=True)
            subprocess.run(["git", "commit", "-qm", "fixture"], cwd=root, check=True)
            before = source_provenance(root)
            self.assertFalse(before["workingTreeModified"])
            source.write_text("Wails temporary regenerated wrapper")
            self.assertTrue(source_provenance(root)["workingTreeModified"])
            self.assertEqual(captured_source(root, {"CRYPTODESK_SOURCE_PROVENANCE": json.dumps(before)}), before)
            self.assertTrue(captured_source(root, {})["workingTreeModified"])

    def test_invalid_capture_fails_instead_of_claiming_a_clean_release(self):
        for value in ["{}", '{"gitRevision":"wrong","workingTreeModified":false}', '{"gitRevision":"' + "a" * 40 + '","workingTreeModified":"false"}']:
            with self.subTest(value=value), self.assertRaises(ValueError):
                captured_source(Path("."), {"CRYPTODESK_SOURCE_PROVENANCE": value})


if __name__ == "__main__":
    unittest.main()
