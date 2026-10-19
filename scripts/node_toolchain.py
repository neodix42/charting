#!/usr/bin/env python3
"""Resolve usable Node/npm, bootstrapping a verified project-local bundle if needed."""
import hashlib
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request

NODE_VERSION = '24.19.0'
# https://nodejs.org/dist/v24.19.0/SHASUMS256.txt
ARCHIVES = {
    'x86_64': ('x64', '14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647'),
    'aarch64': ('arm64', '01443c1e1a29e531ccad5a46fefa6df490d2189c49f7955904aecdbb0fe86fdc'),
}


def compatible(version):
    match = re.fullmatch(r'v(\d+)\.(\d+)\.(\d+)', version.strip())
    if not match:
        return False
    major, minor, _ = map(int, match.groups())
    # Intersection of Vite, ESLint and Vitest's installed engine requirements.
    return (major == 22 and minor >= 13) or major == 24 or major >= 26


def usable(path):
    node, npm = shutil.which('node', path=path), shutil.which('npm', path=path)
    if not node or not npm:
        return None
    env = dict(os.environ, PATH=path)
    try:
        version = subprocess.run([node, '--version'], env=env, check=True,
                                 capture_output=True, text=True, timeout=15).stdout
        if not compatible(version):
            return None
        npm_version = subprocess.run([npm, '--version'], env=env, check=True,
                                     capture_output=True, text=True, timeout=15).stdout
        if not re.fullmatch(r'\d+\.\d+\.\d+\s*', npm_version):
            return None
    except (OSError, subprocess.SubprocessError):
        return None
    return os.pathsep.join(dict.fromkeys([str(Path(node).absolute().parent), str(Path(npm).absolute().parent)]))


def download(url, destination):
    digest = hashlib.sha256()
    size = 0
    with urllib.request.urlopen(url, timeout=60) as response, destination.open('wb') as output:
        while block := response.read(1024 * 1024):
            size += len(block)
            if size > 100 * 1024 * 1024:
                raise RuntimeError('Node archive exceeds the 100 MiB download limit')
            digest.update(block)
            output.write(block)
    return digest.hexdigest()


def install(tools, archive_name, checksum, fetch=download):
    name = archive_name.removesuffix('.tar.xz')
    target = tools / name
    tools.mkdir(parents=True, exist_ok=True)
    # Temporary extraction keeps failed downloads out of the reusable toolchain.
    with tempfile.TemporaryDirectory(prefix='.node-download-', dir=tools) as temporary:
        stage = Path(temporary)
        archive = stage / archive_name
        url = f'https://nodejs.org/dist/v{NODE_VERSION}/{archive_name}'
        print(f'Node/npm missing or incompatible; downloading Node {NODE_VERSION} into {target}', file=sys.stderr)
        if fetch(url, archive) != checksum:
            raise RuntimeError('Node archive SHA-256 verification failed; nothing was installed')
        with tarfile.open(archive, 'r:xz') as bundle:
            bundle.extractall(stage, filter='data')
        extracted = stage / name
        selected = usable(str(extracted / 'bin') + os.pathsep + os.environ.get('PATH', ''))
        if not selected or not (extracted / 'bin/node').is_file() or not (extracted / 'bin/npm').is_file():
            raise RuntimeError('Downloaded Node/npm toolchain did not start successfully')
        if target.exists():
            raise RuntimeError(f'Existing local toolchain is unusable: {target}. Move it aside and retry.')
        extracted.rename(target)
    return target / 'bin'


def resolve(root, path):
    if platform.system() != 'Linux' or platform.machine() not in ARCHIVES:
        found = usable(path)
        if found:
            return found
        raise RuntimeError('Install compatible Node.js (including npm) and add both to PATH')
    arch, checksum = ARCHIVES[platform.machine()]
    archive = f'node-v{NODE_VERSION}-linux-{arch}.tar.xz'
    local_bin = root / '.tools' / archive.removesuffix('.tar.xz') / 'bin'
    if local_bin.is_dir():
        found = usable(str(local_bin) + os.pathsep + path)
        if found:
            return found
    found = usable(path)
    if found:
        return found
    return str(install(root / '.tools', archive, checksum))


if __name__ == '__main__':
    try:
        print(resolve(Path(sys.argv[1]), os.environ.get('PATH', '')))
    except Exception as error:
        print(f'Cannot prepare Node/npm: {error}\nInstall Node 24 LTS with npm on PATH, or allow the project-local download from nodejs.org.', file=sys.stderr)
        sys.exit(1)
