"""Explicit offline bridge-package fixture; never used by production builds."""
import hashlib
import io
import json
from pathlib import Path
import zipfile

import jforex_bundle


def bridge_files(label=b"explicit offline bridge fixture"):
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w") as jar:
        jar.writestr("cryptodesk/jforex/Bridge.class", label)
    files = {"cryptodesk-jforex.jar": output.getvalue(),
             "SDK-ARTIFACTS.json": (jforex_bundle.SOURCE / jforex_bundle.ARTIFACTS).read_bytes(),
             "LICENSE": b"fixture license", "THIRD_PARTY_NOTICES.md": b"fixture notices"}
    manifest = dict(version=1, sdkVersion=jforex_bundle.SDK_VERSION, apiVersion=jforex_bundle.API_VERSION,
                    javaMinimum=17, mainClass=jforex_bundle.MAIN_CLASS,
                    compiler="offline fixture", sources={},
                    files={name: dict(bytes=len(data), sha256=hashlib.sha256(data).hexdigest()) for name, data in files.items()})
    files[jforex_bundle.MANIFEST] = json.dumps(manifest).encode()
    return files, manifest


def write_bridge(directory, label=b"explicit offline bridge fixture"):
    directory = Path(directory)
    directory.mkdir(parents=True)
    files, manifest = bridge_files(label)
    for name, data in files.items():
        (directory / name).write_bytes(data)
    return manifest
