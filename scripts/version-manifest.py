#!/usr/bin/env python3
import datetime, json, pathlib, platform, subprocess, sys
root = pathlib.Path(__file__).resolve().parent.parent
def run(*cmd):
    return subprocess.check_output(cmd, cwd=root, text=True).strip()
manifest = {
    'app': 'CryptoDesk', 'version': '0.1.0',
    'builtAtUTC': datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'go': run('go', 'version'), 'node': run('node', '--version'),
    'npm': run('npm', '--version'), 'architecture': platform.machine(),
    'os': pathlib.Path('/etc/os-release').read_text(),
    'wails': 'v2.17.0', 'webkitBuildTag': 'webkit2_41',
    'frontend': json.loads((root / 'frontend/package.json').read_text())['dependencies'],
    'goModules': run('go', 'list', '-m', 'all').splitlines(),
    'gitRevision': run('git', 'rev-parse', 'HEAD'),
    'workingTreeModified': bool(run('git', 'status', '--porcelain')),
}
pathlib.Path(sys.argv[1]).write_text(json.dumps(manifest, indent=2) + '\n')
