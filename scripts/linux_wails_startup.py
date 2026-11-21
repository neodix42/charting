"""Honor hidden maximized startup through a pinned, temporary Wails build view.

Wails 2.17's Linux Run maps its top-level before queued Hide/Maximise calls.
This narrowly fixes StartHidden+Maximised, without modifying downloaded modules,
the application's go.mod/go.sum, or another Wails startup mode.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

MODULE = 'github.com/wailsapp/wails/v2'
VERSION = 'v2.17.0'
SOURCE_FILE = 'internal/frontend/desktop/linux/window.go'
SOURCE_SHA256 = '5e3627aff3717f19750501a8ce83a67bca8181d5b3fc5c25bd0b49b27a00988f'
PATCH_SITE = b'\tif w.appoptions.StartHidden {\n\t\tw.Hide()\n\t}\n\tC.gtk_widget_show_all(w.asGTKWidget())\n'
INSERTION = b'''\t// Keep a hidden, maximized startup window unmapped until WindowShow.
\t// Its children can load and paint while GTK requests maximization before map.
\tif w.appoptions.StartHidden && w.appoptions.WindowStartState == options.Maximised {
\t\tC.gtk_widget_realize(w.asGTKWidget())
\t\tC.gtk_widget_show_all(w.vbox)
\t\tC.gtk_window_maximize(w.asGTKWindow())
\t\treturn
\t}
'''


def patch_startup_source(source):
    if hashlib.sha256(source).hexdigest() != SOURCE_SHA256:
        raise RuntimeError(f'The pinned {MODULE}@{VERSION} {SOURCE_FILE} SHA-256 does not match; refusing to patch an unknown dependency')
    if source.count(PATCH_SITE) != 1:
        raise RuntimeError('The pinned Wails startup mapping site is not unique')
    return source.replace(PATCH_SITE, INSERTION + PATCH_SITE, 1)


def split_go_flags(value):
    """Match Go's quoted.Split: whole-field quotes, no shell interpolation."""
    fields, offset = [], 0
    while offset < len(value):
        while offset < len(value) and value[offset] in ' \t\r\n':
            offset += 1
        if offset == len(value):
            break
        quote = value[offset] if value[offset] in "'\"" else None
        start = offset + 1 if quote else offset
        if quote:
            end = value.find(quote, start)
            if end < 0:
                raise ValueError('GOFLAGS contains an unterminated quoted field')
            fields.append(value[start:end])
            offset = end + 1
        else:
            while offset < len(value) and value[offset] not in ' \t\r\n':
                offset += 1
            fields.append(value[start:offset])
    return fields


def quote_go_flag(value):
    if not any(char in value for char in " \t\r\n'\""):
        return value
    if "'" not in value:
        return "'" + value + "'"
    if '"' not in value:
        return '"' + value + '"'
    raise ValueError('A Go build flag contains both quote types and cannot be represented in GOFLAGS')


def prepare_startup_overlay(directory, environment=None, working_directory=None):
    environment = dict(os.environ if environment is None else environment)
    working_directory = Path(working_directory or Path.cwd()).resolve()
    flags = split_go_flags(environment.get('GOFLAGS', ''))
    supplied_modfile = next((flag.split('=', 1)[1] for flag in reversed(flags) if flag.startswith('-modfile=')), None)
    supplied_overlay = next((flag.split('=', 1)[1] for flag in reversed(flags) if flag.startswith('-overlay=')), None)
    for name in ('-modfile', '-overlay'):
        if name in flags:
            raise ValueError(f'Use {name}=PATH in GOFLAGS so the startup build view can preserve it')
    kept_flags = [flag for flag in flags if not flag.startswith(('-modfile=', '-overlay='))]
    def go(*arguments):
        return subprocess.run(['go', *arguments], cwd=working_directory, env=environment,
                              check=True, capture_output=True, text=True, timeout=180).stdout
    module = json.loads(go('list', '-m', '-json', MODULE))
    if module.get('Path') != MODULE or module.get('Version') != VERSION or module.get('Replace'):
        raise RuntimeError(f'The Linux startup overlay requires the unmodified pinned {MODULE}@{VERSION}')
    if not module.get('Dir'):
        module = json.loads(go('mod', 'download', '-json', f'{MODULE}@{VERSION}'))
        if module.get('Path') != MODULE or module.get('Version') != VERSION or module.get('Replace') or module.get('Error') or not module.get('Dir'):
            raise RuntimeError('Go did not provide the pinned Wails source for the startup overlay')
    module_root = Path(module['Dir']).resolve()
    original = module_root / SOURCE_FILE
    patched = patch_startup_source(original.read_bytes())
    modfile = Path(supplied_modfile) if supplied_modfile else Path(go('env', 'GOMOD').strip())
    if not modfile.is_absolute():
        modfile = working_directory / modfile
    if not modfile.is_file():
        raise RuntimeError('The Linux startup overlay must be prepared from an application Go module')
    directory = Path(directory).resolve()
    if directory.is_relative_to(module_root) or directory.is_relative_to(Path(__file__).resolve().parent.parent):
        raise RuntimeError('The startup build view must use a temporary directory outside the checkout and module cache')
    module_copy = directory / 'wails-v2.17.0'
    overlay_path, patched_path = directory / 'overlay.json', directory / 'window.go'
    output_modfile = directory / 'app.mod'
    output_sumfile = directory / 'app.sum'
    for path in (module_copy, overlay_path, patched_path, output_modfile, output_sumfile):
        if path.exists() or path.is_symlink():
            raise RuntimeError(f'Refusing to replace an existing startup overlay file: {path}')
    replacements = {}
    if supplied_overlay is not None:
        overlay_input = Path(supplied_overlay)
        if not overlay_input.is_absolute():
            overlay_input = working_directory / overlay_input
        document = json.loads(overlay_input.read_text())
        if set(document) != {'Replace'} or not isinstance(document['Replace'], dict):
            raise RuntimeError('The existing Go overlay must contain a Replace object')
        for source, replacement in document['Replace'].items():
            if not isinstance(source, str) or not isinstance(replacement, str):
                raise RuntimeError('Existing Go overlay replacements must be path strings')
            source = Path(source)
            if not source.is_absolute():
                source = working_directory / source
            source = source.resolve()
            if source == original:
                raise RuntimeError('The existing Go overlay already changes the pinned Wails startup file; refusing to override it')
            if source.is_relative_to(module_root):
                source = module_copy / source.relative_to(module_root)
            if replacement:
                destination = Path(replacement)
                if not destination.is_absolute():
                    destination = working_directory / destination
                replacement = str(destination.absolute())
            replacements[str(source)] = replacement
    directory.mkdir(parents=True, exist_ok=True)
    shutil.copytree(module_root, module_copy)
    replacement = '\nreplace ' + MODULE + ' ' + VERSION + ' => ' + json.dumps(str(module_copy)) + '\n'
    output_modfile.write_bytes(modfile.read_bytes() + replacement.encode('utf-8'))
    input_sumfile = modfile.with_suffix('.sum')
    if input_sumfile.is_file():
        output_sumfile.write_bytes(input_sumfile.read_bytes())
    patched_path.write_bytes(patched)
    replacements[str(module_copy / SOURCE_FILE)] = str(patched_path)
    overlay_path.write_text(json.dumps({'Replace': replacements}, indent=2) + '\n')
    go_flags = ' '.join(quote_go_flag(flag) for flag in [*kept_flags, '-modfile=' + str(output_modfile), '-overlay=' + str(overlay_path)])
    return {'goFlags': go_flags, 'manifest': {'module': MODULE, 'version': VERSION,
            'sourceFile': SOURCE_FILE, 'originalSHA256': SOURCE_SHA256,
            'patchedSHA256': hashlib.sha256(patched).hexdigest(),
            'scope': 'Linux StartHidden && Maximised',
            'method': 'temporary-module-copy-modfile-and-source-overlay'}}


def prepare_wails_compiler(directory, go_flags, compiler='go'):
    """Wails disables modules for discovery; apply our module view only to build.

    The normal CLI environment must not receive -modfile globally. Its supported
    compiler hook scopes the extra flags to the actual application compilation.
    """
    go = shutil.which(compiler)
    if not go:
        raise RuntimeError(f'The Go compiler is unavailable: {compiler}')
    path = Path(directory) / 'go-linux-startup'
    path.write_text('#!/usr/bin/env python3\n'
                    'import os, subprocess, sys\n'
                    'environment = os.environ.copy()\n'
                    "if sys.argv[1:2] == ['build'] and environment.get('GO111MODULE') != 'off':\n"
                    f"    environment['GOFLAGS'] = {go_flags!r}\n"
                    f'raise SystemExit(subprocess.call([{go!r}, *sys.argv[1:]], env=environment))\n')
    path.chmod(0o755)
    return path


def wails_compiler_command(command, directory, go_flags):
    compiler, remaining, index = 'go', [], 0
    while index < len(command):
        argument = command[index]
        if argument == '-compiler':
            if index + 1 == len(command):
                raise ValueError('Wails -compiler requires a compiler path')
            compiler = command[index + 1]
            index += 2
        elif argument.startswith('-compiler='):
            compiler = argument.split('=', 1)[1]
            index += 1
        else:
            remaining.append(argument)
            index += 1
    wrapper = prepare_wails_compiler(directory, go_flags, compiler)
    return [*remaining, '-compiler', str(wrapper)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', type=Path)
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    if not command:
        parser.error('a command is required after --')
    if any(value.startswith(('-modfile=', '-overlay=')) or value in ('-modfile', '-overlay') for value in command[1:]):
        parser.error('pass custom -modfile/-overlay through GOFLAGS so they can be preserved')
    if sys.platform != 'linux' or os.environ.get('GOOS', 'linux') != 'linux':
        return subprocess.call(command)
    with tempfile.TemporaryDirectory(prefix='cryptodesk-wails-startup-') as temporary:
        overlay = prepare_startup_overlay(Path(temporary))
        if args.manifest:
            args.manifest.parent.mkdir(parents=True, exist_ok=True)
            args.manifest.write_text(json.dumps(overlay['manifest'], indent=2) + '\n')
        environment = dict(os.environ)
        if Path(command[0]).name == 'wails' and command[1:2] in (['build'], ['dev']):
            command = wails_compiler_command(command, Path(temporary), overlay['goFlags'])
        else:
            environment['GOFLAGS'] = overlay['goFlags']
        return subprocess.call(command, env=environment)


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130)
    except (RuntimeError, ValueError, subprocess.SubprocessError, OSError) as error:
        print(f'Linux startup build view failed: {error}', file=sys.stderr)
        raise SystemExit(1)
