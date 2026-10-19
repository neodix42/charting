#!/usr/bin/env bash
# Download/extract Ubuntu's native build headers into this repository. This never
# installs packages, invokes sudo, or changes system/package-manager configuration.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
export CRYPTODESK_NATIVE_ROOT="$repo_root/.tools/native"
python3 - <<'PY'
import os, pathlib, re, subprocess
sdk = pathlib.Path(os.environ['CRYPTODESK_NATIVE_ROOT'])
sdk.mkdir(parents=True, exist_ok=True)
archives = sdk / 'archives'
archives.mkdir(exist_ok=True)
simulation = subprocess.check_output(['apt-get', '--simulate', 'install', 'libgtk-3-dev', 'libwebkit2gtk-4.1-dev'], text=True)
packages = re.findall(r'^Inst (\S+) \((\S+)', simulation, re.MULTILINE)
# Always extract the primary SDKs even on machines where they are already installed.
for name in ['libgtk-3-dev', 'libwebkit2gtk-4.1-dev']:
    if not any(p == name for p, _ in packages):
        version = subprocess.check_output(['dpkg-query', '-W', '-f=${Version}', name], text=True)
        packages.append((name, version))
(sdk / 'package-versions.txt').write_text(''.join(f'{name}={version}\n' for name, version in packages))
print(f'Downloading {len(packages)} packages into {archives}', flush=True)
subprocess.run(['apt', 'download'] + [f'{name}={version}' for name, version in packages], cwd=archives, check=True)
for archive in sorted(archives.glob('*.deb')):
    subprocess.run(['dpkg-deb', '-x', str(archive), str(sdk)], check=True)
# Relocate only local pkg-config records. Existing system records remain untouched.
for record in sdk.rglob('*.pc'):
    content = record.read_text()
    content = re.sub(r'(?<![\w/])/usr(?=/|$)', str(sdk / 'usr'), content, flags=re.MULTILINE)
    record.write_text(content)
# Development linker names often target runtime libraries supplied by the OS.
# Resolve broken links to those installed libraries without copying/changing them.
for link in sdk.rglob('*'):
    if link.is_symlink() and link.name.endswith('.so') and not link.exists():
        target = os.readlink(link)
        local_target = pathlib.Path(os.path.abspath(link.parent / target)) if not target.startswith('/') else pathlib.Path(target)
        try:
            relative = local_target.relative_to(sdk)
        except ValueError:
            continue
        system_target = pathlib.Path('/') / relative
        if system_target.exists():
            link.unlink()
            link.symlink_to(system_target)
print('Local native SDK extracted; source scripts/native-env.sh before building.', flush=True)
PY
