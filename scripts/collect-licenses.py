#!/usr/bin/env python3
"""Collect installed module/package notices verbatim, with provenance paths."""
import json, pathlib, re, shutil, subprocess
root = pathlib.Path(__file__).resolve().parent.parent
out = root / 'docs/licenses'
out.mkdir(parents=True, exist_ok=True)
raw = subprocess.check_output(['go', 'list', '-m', '-json', 'all'], cwd=root, text=True)
decoder = json.JSONDecoder()
modules = []
while raw.strip():
    obj, end = decoder.raw_decode(raw.lstrip())
    modules.append(obj)
    raw = raw.lstrip()[end:]
index = []
for mod in modules:
    if mod.get('Main') or not mod.get('Dir'):
        continue
    directory = pathlib.Path(mod['Dir'])
    dest = out / ('go-' + re.sub(r'[^a-zA-Z0-9.-]', '_', mod['Path']) + '-' + mod['Version'])
    found = []
    for item in directory.iterdir():
        if item.is_file() and item.name.lower().startswith(('license','copying','notice','copyright','authors')):
            dest.mkdir(exist_ok=True)
            target = dest / item.name
            if target.exists(): target.chmod(0o644)
            shutil.copyfile(item, target)
            found.append(item.name)
    index.append({'package':mod['Path'],'version':mod['Version'],'files':found})
lock = json.loads((root/'frontend/package-lock.json').read_text())
for path, package in lock['packages'].items():
    if not path or package.get('dev'):
        continue
    directory = root/'frontend'/path
    dest = out / ('npm-' + path.replace('node_modules/','').replace('/','_') + '-' + package['version'])
    found = []
    for item in directory.iterdir():
        if item.is_file() and item.name.lower().startswith(('license','copying','notice','copyright')):
            dest.mkdir(exist_ok=True)
            target = dest / item.name
            if target.exists(): target.chmod(0o644)
            shutil.copyfile(item, target)
            found.append(item.name)
    retained = [item.name for item in dest.iterdir() if item.is_file()] if dest.is_dir() else []
    entry = {'package':path,'version':package['version'],'license':package.get('license'),'files':sorted(set(found+retained))}
    if path == 'node_modules/fancy-canvas':
        entry['licenseSource'] = 'https://github.com/tradingview/fancy-canvas/blob/master/LICENSE'
    if path == 'node_modules/lightweight-charts':
        entry['noticeSource'] = 'https://raw.githubusercontent.com/tradingview/lightweight-charts/v5.2.1/NOTICE'
    index.append(entry)
(out/'index.json').write_text(json.dumps(index,indent=2)+'\n')
print(f'Collected notices for {len(index)} modules/packages in {out}')
missing = [p['package'] for p in index if not p['files']]
if missing:
    print('Review modules with no root notice: '+', '.join(missing))
