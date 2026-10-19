#!/usr/bin/env python3
"""Prepare the pinned Microsoft Fixed Version x64 WebView2 tree without installing it.

The build never resolves "latest". URLs, CAB hash, extracted inventory and the
fixed-runtime license are pinned in webview2-runtime.json. An explicit --cab or
a verified local cache permits builds without network access.
"""
import argparse
import hashlib
import json
import mmap
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import struct
import subprocess
import sys
import tempfile
import urllib.parse
import urllib.request

MANIFEST = Path(__file__).with_name('webview2-runtime.json')
PROVENANCE = 'WEBVIEW2-PROVENANCE.json'
MAX_EXTRACTED_BYTES = 3 * 1024 * 1024 * 1024
MAX_FILES = 10000


def digest_file(path):
    digest = hashlib.sha256()
    with Path(path).open('rb') as source:
        while block := source.read(1024 * 1024):
            digest.update(block)
    return digest.hexdigest()


def load_manifest(path=MANIFEST):
    pin = json.loads(Path(path).read_text(encoding='utf-8'))
    version = pin.get('version', '')
    if not re.fullmatch(r'\d+\.\d+\.\d+\.\d+', version) or pin.get('architecture') != 'x64':
        raise RuntimeError('The WebView2 pin must specify an exact x64 runtime version')
    name = f'Microsoft.WebView2.FixedVersionRuntime.{version}.x64'
    if pin.get('directory') != name or pin.get('filename') != name + '.cab':
        raise RuntimeError('The WebView2 pin has an inconsistent archive name')
    url = urllib.parse.urlparse(pin.get('url', ''))
    if url.scheme != 'https' or not (url.hostname or '').endswith('.microsoft.com'):
        raise RuntimeError('The WebView2 CAB URL must use an official Microsoft HTTPS host')
    if not re.fullmatch(r'[0-9a-f]{64}', pin.get('sha256', '')) or not isinstance(pin.get('size'), int) or not 0 < pin['size'] <= 1024 ** 3:
        raise RuntimeError('The WebView2 CAB pin must include its SHA-256 and bounded size')
    license_text = pin.get('license_text', '')
    if pin.get('license_filename') != 'WEBVIEW2-LICENSE.html' or 'MICROSOFT EDGE WEBVIEW2 RUNTIME (FIXED VERSION)' not in license_text or hashlib.sha256(license_text.encode('utf-8')).hexdigest() != pin.get('license_sha256'):
        raise RuntimeError('The pinned Microsoft Fixed Version license is missing or corrupt')
    if not re.fullmatch(r'[0-9a-f]{64}', pin.get('inventory_sha256') or ''):
        raise RuntimeError('The WebView2 extracted inventory pin is invalid')
    return pin


def download(url, destination, expected_size):
    request = urllib.request.Request(url, headers={'User-Agent': 'CryptoDesk-runtime-build/0.1'})
    size = 0
    with urllib.request.urlopen(request, timeout=60) as response, destination.open('wb') as output:
        final = urllib.parse.urlparse(response.geturl())
        if final.scheme != 'https' or not (final.hostname or '').endswith('.microsoft.com'):
            raise RuntimeError('The Microsoft runtime download redirected to an unexpected host')
        while block := response.read(1024 * 1024):
            size += len(block)
            if size > expected_size:
                raise RuntimeError('The WebView2 CAB exceeds its pinned download size')
            output.write(block)


def verify_archive(path, pin):
    path = Path(path)
    if path.is_symlink() or not path.is_file():
        raise RuntimeError(f'WebView2 CAB is not a regular file: {path}')
    if path.stat().st_size != pin['size'] or digest_file(path) != pin['sha256']:
        raise RuntimeError(f'WebView2 CAB SHA-256/size verification failed: {path}. Expected the pinned {pin["version"]} x64 CAB.')
    with path.open('rb') as source:
        if source.read(4) != b'MSCF':
            raise RuntimeError('The verified download is not a Microsoft Cabinet archive')


def ensure_archive(cache, pin, cab=None, fetch=download):
    cache = Path(cache)
    downloads = cache / 'downloads'
    downloads.mkdir(parents=True, exist_ok=True)
    target = downloads / pin['filename']
    if cab is not None:
        cab = Path(cab).absolute()
        verify_archive(cab, pin)
    if target.exists() or target.is_symlink():
        verify_archive(target, pin)
        return target
    with tempfile.TemporaryDirectory(prefix='.webview2-download-', dir=downloads) as temporary:
        staged = Path(temporary) / pin['filename']
        if cab is not None:
            shutil.copyfile(cab, staged)
        else:
            print(f'Downloading pinned WebView2 {pin["version"]} x64 ({pin["size"]:,} bytes)', file=sys.stderr)
            fetch(pin['url'], staged, pin['size'])
        verify_archive(staged, pin)
        staged.rename(target)
    return target


def find_extractor(explicit=None):
    executable = shutil.which(str(explicit)) if explicit else (shutil.which('7z') or shutil.which('7zz'))
    if not executable:
        raise RuntimeError('7-Zip is required to unpack the Microsoft CAB. Install the Ubuntu package "7zip" ("p7zip-full" on older Ubuntu), or pass --extractor /path/to/7z. No system packages were installed.')
    return executable


def safe_archive_path(value, directory):
    normalized = value.replace('\\', '/')
    parts = normalized.split('/')
    if not value or '\x00' in value or any(ord(character) < 32 for character in value) or normalized.startswith('/') or any(part in ('', '.', '..') for part in parts):
        raise RuntimeError(f'Unsafe path in WebView2 CAB: {value!r}')
    for part in parts:
        if ':' in part or part.endswith((' ', '.')) or re.fullmatch(r'(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\..*)?', part, re.IGNORECASE):
            raise RuntimeError(f'Unsafe Windows path in WebView2 CAB: {value!r}')
    if parts[0] != directory:
        raise RuntimeError(f'Unexpected top-level WebView2 CAB path: {value!r}')
    return PurePosixPath(*parts).as_posix()


def archive_inventory(archive, extractor, pin):
    result = subprocess.run([extractor, 'l', '-slt', '-ba', str(archive)], check=True, capture_output=True, text=True, encoding='utf-8', timeout=60)
    records = []
    for block in re.split(r'\r?\n\s*\r?\n', result.stdout.strip()):
        record = {}
        for line in block.splitlines():
            if ' = ' in line:
                key, value = line.split(' = ', 1)
                if key in record:
                    raise RuntimeError('Duplicate fields in WebView2 CAB listing')
                record[key] = value
        if record:
            records.append(record)
    if not records or len(records) > MAX_FILES:
        raise RuntimeError('The WebView2 CAB has an empty or excessive file listing')
    files, seen, total = {}, set(), 0
    for record in records:
        path = safe_archive_path(record.get('Path', ''), pin['directory'])
        folded = path.casefold()
        if folded in seen:
            raise RuntimeError(f'Duplicate Windows path in WebView2 CAB: {path}')
        seen.add(folded)
        attributes = record.get('Attributes', '')
        if any('link' in key.lower() for key in record) or re.search(r'(^|\s)l[rwxs-]', attributes) or record.get('Mode', '').startswith('l'):
            raise RuntimeError(f'Links are not permitted in the WebView2 CAB: {path}')
        is_directory = record.get('Folder') == '+' or attributes.startswith('D')
        if is_directory:
            continue
        if '/' not in path:
            raise RuntimeError('The WebView2 CAB root must be a directory')
        try:
            size = int(record['Size'])
        except (KeyError, ValueError) as error:
            raise RuntimeError('The WebView2 CAB has an invalid file size') from error
        if size < 0:
            raise RuntimeError('The WebView2 CAB has a negative file size')
        total += size
        if total > MAX_EXTRACTED_BYTES:
            raise RuntimeError('The WebView2 CAB exceeds the unpacked size limit')
        files[path] = size
    if not files:
        raise RuntimeError('The WebView2 CAB contains no runtime files')
    return files


def tree_inventory(root):
    root = Path(root)
    if root.is_symlink() or not root.is_dir():
        raise RuntimeError(f'WebView2 runtime is not a directory: {root}')
    files, total = {}, 0
    for directory, directories, names in os.walk(root, followlinks=False):
        for name in directories + names:
            path = Path(directory) / name
            info = path.lstat()
            if stat.S_ISLNK(info.st_mode) or not (stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode)):
                raise RuntimeError(f'Unexpected link or special file in WebView2 runtime: {path}')
            if stat.S_ISREG(info.st_mode):
                if info.st_nlink != 1:
                    raise RuntimeError(f'Unexpected hard link in WebView2 runtime: {path}')
                relative = path.relative_to(root).as_posix()
                files[relative] = {'size': info.st_size, 'sha256': digest_file(path)}
                total += info.st_size
                if len(files) > MAX_FILES or total > MAX_EXTRACTED_BYTES:
                    raise RuntimeError('The extracted WebView2 runtime exceeds its file/size limit')
    return files


def validate_pe(path, version):
    """Check x64 PE32+ and the Windows VS_FIXEDFILEINFO file version, without execution."""
    path = Path(path)
    if path.stat().st_size < 64:
        raise RuntimeError(f'WebView2 runtime is not a PE executable: {path}')
    with path.open('rb') as source, mmap.mmap(source.fileno(), 0, access=mmap.ACCESS_READ) as data:
        if data[:2] != b'MZ':
            raise RuntimeError(f'WebView2 runtime is not a PE executable: {path}')
        pe = struct.unpack_from('<I', data, 0x3c)[0]
        if pe + 26 > len(data) or data[pe:pe + 4] != b'PE\0\0' or struct.unpack_from('<H', data, pe + 4)[0] != 0x8664 or struct.unpack_from('<H', data, pe + 24)[0] != 0x20b:
            raise RuntimeError(f'WebView2 executable is not a Windows x64 PE32+ binary: {path}')
        signature = struct.pack('<II', 0xfeef04bd, 0x00010000)
        offset = 0
        while (offset := data.find(signature, offset)) >= 0:
            if offset + 16 <= len(data):
                major_minor, build_patch = struct.unpack_from('<II', data, offset + 8)
                actual = f'{major_minor >> 16}.{major_minor & 0xffff}.{build_patch >> 16}.{build_patch & 0xffff}'
                if actual == version:
                    return {'machine': 'AMD64', 'format': 'PE32+', 'version': actual}
            offset += len(signature)
    raise RuntimeError(f'WebView2 PE file version does not match pinned {version}: {path}')


def validate_runtime(root, pin):
    root = Path(root)
    files = tree_inventory(root)
    files.pop(PROVENANCE, None)
    required = ['msedgewebview2.exe', 'msedge.dll', 'icudtl.dat', 'resources.pak', 'v8_context_snapshot.bin', 'Locales/en-US.pak', f'{pin["version"]}.manifest', 'show_third_party_software_licenses.bat', 'WidevineCdm/LICENSE', pin['license_filename']]
    for name in required:
        if name not in files or not files[name]['size']:
            raise RuntimeError(f'WebView2 runtime is incomplete: missing {name}')
    if files[pin['license_filename']]['sha256'] != pin['license_sha256']:
        raise RuntimeError('The runtime Microsoft Fixed Version license does not match its pin')
    validate_pe(root / 'msedgewebview2.exe', pin['version'])
    validate_pe(root / 'msedge.dll', pin['version'])
    inventory = hashlib.sha256()
    for name, info in sorted(files.items()):
        inventory.update(f'{name}\0{info["size"]}\0{info["sha256"]}\n'.encode('utf-8'))
    tree_hash = inventory.hexdigest()
    if pin.get('inventory_sha256') and pin['inventory_sha256'] != tree_hash:
        raise RuntimeError(f'The extracted WebView2 file inventory does not match its pin: {root}')
    return {'version': pin['version'], 'architecture': pin['architecture'], 'archive': pin['filename'], 'archive_sha256': pin['sha256'], 'archive_bytes': pin['size'], 'file_count': len(files), 'uncompressed_bytes': sum(info['size'] for info in files.values()), 'inventory_sha256': tree_hash, 'source_url': pin['url'], 'license_url': pin['license_url'], 'distribution_url': pin['distribution_url'], 'release_notes_url': pin['release_notes_url']}


def extract_runtime(archive, cache, pin, extractor):
    base = Path(cache) / 'extracted'
    base.mkdir(parents=True, exist_ok=True)
    target = base / pin['directory']
    expected = archive_inventory(archive, extractor, pin)
    with tempfile.TemporaryDirectory(prefix='.webview2-extract-', dir=base) as temporary:
        stage = Path(temporary)
        print(f'Extracting all pinned WebView2 {pin["version"]} runtime files', file=sys.stderr)
        subprocess.run([extractor, 'x', '-y', '-bd', f'-o{stage}', str(archive)], check=True, capture_output=True, text=True, timeout=600)
        actual = tree_inventory(stage)
        if {name: info['size'] for name, info in actual.items()} != expected:
            raise RuntimeError('The extracted WebView2 files differ from the verified CAB listing')
        runtime = stage / pin['directory']
        license_path = runtime / pin['license_filename']
        if license_path.exists() or license_path.is_symlink():
            raise RuntimeError('The runtime archive unexpectedly contains the generated license filename')
        license_path.write_text(pin['license_text'], encoding='utf-8', newline='')
        report = validate_runtime(runtime, pin)
        (runtime / PROVENANCE).write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
        if target.exists() or target.is_symlink():
            raise RuntimeError(f'A WebView2 extraction appeared concurrently: {target}. Retry the build.')
        runtime.rename(target)
    return target


def check_destination(destination):
    destination = Path(destination)
    if destination.is_symlink() or (destination.exists() and (not destination.is_dir() or any(destination.iterdir()))):
        raise RuntimeError(f'Refusing to replace a nonempty or linked runtime destination: {destination}')


def prepare_runtime(cache, destination, cab=None, pin=None, extractor=None, fetch=download):
    pin = pin or load_manifest()
    cache, destination = Path(cache).absolute(), Path(destination).absolute()
    check_destination(destination)
    if cab is not None:
        verify_archive(cab, pin)
    runtime = cache / 'extracted' / pin['directory']
    if runtime.exists() or runtime.is_symlink():
        report = validate_runtime(runtime, pin)
    else:
        executable = find_extractor(extractor)
        archive = ensure_archive(cache, pin, cab, fetch)
        runtime = extract_runtime(archive, cache, pin, executable)
        report = validate_runtime(runtime, pin)
    destination.parent.mkdir(parents=True, exist_ok=True)
    print(f'Copying verified WebView2 {pin["version"]} x64 to {destination}', file=sys.stderr)
    with tempfile.TemporaryDirectory(prefix='.webview2-copy-', dir=destination.parent) as temporary:
        staged = Path(temporary) / 'runtime'
        shutil.copytree(runtime, staged)
        report = validate_runtime(staged, pin)
        (staged / PROVENANCE).write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
        check_destination(destination)
        if destination.exists():
            destination.rmdir()
        staged.rename(destination)
    return {**report, 'destination': str(destination)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cache-dir', type=Path, required=True)
    parser.add_argument('--destination', type=Path, required=True)
    parser.add_argument('--cab', type=Path, help='Use the exact pinned local CAB without downloading')
    parser.add_argument('--extractor', help='Path to an existing 7z/7zz executable')
    args = parser.parse_args()
    try:
        print(json.dumps(prepare_runtime(args.cache_dir, args.destination, args.cab, extractor=args.extractor), indent=2))
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(f'Cannot prepare the portable WebView2 runtime: {error}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
