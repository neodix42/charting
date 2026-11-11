#!/usr/bin/env python3
"""Configure Ubuntu sources only on disposable GitHub Actions Linux runners."""

import os
from pathlib import Path
import re

ARCHIVE = "https://archive.ubuntu.com/ubuntu"
NETWORK_CONFIG = '''APT::Update::Error-Mode "any";
Acquire::Retries "1";
Acquire::http::Timeout "15";
Acquire::https::Timeout "15";
'''


def configure(apt_directory: Path) -> None:
    paths = [apt_directory / "sources.list"]
    for pattern in ("*.list", "*.sources"):
        paths.extend(sorted((apt_directory / "sources.list.d").glob(pattern)))
    ubuntu_found = False
    for path in paths:
        if not path.is_file():
            continue
        original = path.read_text()
        updated = re.sub(r"mirror\+file:/etc/apt/apt-mirrors\.txt/?(?=\s|$)", ARCHIVE, original)
        updated = re.sub(r"https?://azure\.archive\.ubuntu\.com/ubuntu/?(?=\s|$)", ARCHIVE, updated)
        updated = re.sub(r"http://(archive|security)\.ubuntu\.com/ubuntu(?=/|\s|$)", r"https://\1.ubuntu.com/ubuntu", updated)
        ubuntu_found |= bool(re.search(r"https://(?:archive|security)\.ubuntu\.com/ubuntu(?=/|\s|$)", updated))
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
    configure(Path("/etc/apt"))
    print("CI Ubuntu sources use official HTTPS archives; APT update errors fail and network waits are bounded")


if __name__ == "__main__":
    main()
