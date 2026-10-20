#!/usr/bin/env python3
import datetime, json, pathlib, platform, subprocess, sys
from app_version import app_version
from build_metadata import captured_source, source_provenance
root = pathlib.Path(__file__).resolve().parent.parent
if sys.argv[1:] == ['--capture-source']:
    print(json.dumps(source_provenance(root)))
    raise SystemExit(0)
def run(*cmd):
    return subprocess.check_output(cmd, cwd=root, text=True).strip()
manifest = {
    'app': 'CryptoDesk', 'version': app_version(root), 'target': 'linux/amd64',
    'builtAtUTC': datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'go': run('go', 'version'), 'node': run('node', '--version'),
    'npm': run('npm', '--version'), 'architecture': platform.machine(),
    'os': pathlib.Path('/etc/os-release').read_text(),
    'wails': 'v2.17.0', 'webkitBuildTag': 'webkit2_41',
    'frontend': json.loads((root / 'frontend/package.json').read_text())['dependencies'],
    'goModules': run('go', 'list', '-m', 'all').splitlines(),
    **captured_source(root),
}
pathlib.Path(sys.argv[1]).write_text(json.dumps(manifest, indent=2) + '\n')
