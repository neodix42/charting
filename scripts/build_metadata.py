"""Record checkout provenance before build tools temporarily regenerate sources."""
import json
import os
import re
import subprocess


def source_provenance(root):
    def git(*arguments):
        return subprocess.check_output(["git", *arguments], cwd=root, text=True).strip()
    return {"gitRevision": git("rev-parse", "HEAD"),
            "workingTreeModified": bool(git("status", "--porcelain"))}


def captured_source(root, environment=None):
    environment = os.environ if environment is None else environment
    record = environment.get("CRYPTODESK_SOURCE_PROVENANCE")
    if record is None:
        return source_provenance(root)
    result = json.loads(record)
    if set(result) != {"gitRevision", "workingTreeModified"} or not isinstance(result["workingTreeModified"], bool) or not re.fullmatch(r"[0-9a-f]{40}", result["gitRevision"]):
        raise ValueError("Invalid captured build source provenance")
    return result
