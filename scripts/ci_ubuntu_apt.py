#!/usr/bin/env python3
"""Configure Ubuntu sources only on disposable GitHub Actions Linux runners."""

import os
from pathlib import Path
import re
from urllib.request import Request, urlopen

ARCHIVE = "https://archive.ubuntu.com/ubuntu"
ARCHIVES = (ARCHIVE, "https://mirrors.edge.kernel.org/ubuntu", "https://mirror.math.princeton.edu/pub/ubuntu")
SUITES = ("noble", "noble-updates", "noble-backports", "noble-security")
NETWORK_CONFIG = '''APT::Update::Error-Mode "any";
Acquire::Retries "1";
Acquire::http::Timeout "15";
Acquire::https::Timeout "15";
'''


def select_archive(opener=urlopen) -> str:
    errors = []
    for archive in ARCHIVES:
        try:
            for suite in SUITES:
                with opener(Request(f"{archive}/dists/{suite}/InRelease", method="HEAD"), timeout=8) as response:
                    if response.status != 200:
                        raise OSError(f"HTTP status {response.status}")
            return archive
        except OSError as error:
            errors.append(f"{archive}: {error}")
            print(f"CI Ubuntu mirror unavailable: {archive} ({type(error).__name__})")
    raise RuntimeError("No Ubuntu HTTPS archive is reachable: " + "; ".join(errors))


def configure(apt_directory: Path, archive: str = ARCHIVE) -> None:
    if archive not in ARCHIVES:
        raise ValueError("Only the fixed Ubuntu HTTPS archive list is supported")
    patterns = [r"mirror\+file:/etc/apt/apt-mirrors\.txt/?(?=\s|$)", r"https?://(?:azure\.archive|archive|security)\.ubuntu\.com/ubuntu/?(?=\s|$)"]
    patterns.extend(re.escape(base) + r"/?(?=\s|$)" for base in ARCHIVES)
    source_uri = re.compile("|".join(patterns))
    paths = [apt_directory / "sources.list"]
    for pattern in ("*.list", "*.sources"):
        paths.extend(sorted((apt_directory / "sources.list.d").glob(pattern)))
    ubuntu_found = False
    for path in paths:
        if not path.is_file():
            continue
        original = path.read_text()
        updated, count = source_uri.subn(archive, original)
        ubuntu_found |= count > 0
        if updated != original:
            path.write_text(updated)
    if not ubuntu_found:
        raise RuntimeError("No supported Ubuntu APT source found on the CI runner")
    config = apt_directory / "apt.conf.d" / "99cryptodesk-ci"
    config.parent.mkdir(parents=True, exist_ok=True)
    config.write_text(NETWORK_CONFIG)
    config.chmod(0o644)


def main() -> None:
    if os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("RUNNER_OS") != "Linux":
        raise SystemExit("This helper is only for GitHub Actions Linux runners; no local configuration was changed")
    archive = select_archive()
    configure(Path("/etc/apt"), archive)
    print(f"CI Ubuntu sources use {archive}; package signatures remain required, index errors fail and network waits are bounded")


if __name__ == "__main__":
    main()
