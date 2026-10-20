"""Apply the supported WebView2 Language option only to a temporary Go build view.

Wails 2.17 does not expose the environment Language setting. Its pinned WebView2
adapter already uses the public environment builder, so one additional option is
enough. Missing source is downloaded through Go's verified module cache; the
overlay keeps the checkout and downloaded dependency sources unchanged.
"""
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess

MODULE = 'github.com/wailsapp/go-webview2'
VERSION = 'v1.0.22'
SOURCE_FILE = 'pkg/edge/create_env_go.go'
SOURCE_SHA256 = 'c56b55d9050f28b53f8dee0e6aee6f830e09fa7ad4cf0a4def303273fbebf1b2'
PATCH_SITE = b'\t\twebviewloader.WithAdditionalBrowserArguments(additionalBrowserArgs),\n'


def patch_language_source(source, language='en-US'):
    """Verify the exact upstream file before adding one public Language option."""
    if not isinstance(language, str) or not re.fullmatch(r'[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*', language):
        raise ValueError('WebView2 language must be a BCP 47 language tag such as en-US')
    if hashlib.sha256(source).hexdigest() != SOURCE_SHA256:
        raise RuntimeError(f'The pinned {MODULE}@{VERSION} {SOURCE_FILE} source SHA-256 does not match; refusing to patch an unknown dependency')
    if source.count(PATCH_SITE) != 1 or b'webviewloader.WithLanguage(' in source:
        raise RuntimeError('The pinned WebView2 environment constructor does not have exactly one expected Language insertion point')
    insertion = f'\t\twebviewloader.WithLanguage("{language}"),\n'.encode('ascii')
    return source.replace(PATCH_SITE, PATCH_SITE + insertion, 1)


def quote_go_flag(argument):
    """Quote one whole GOFLAGS field using Go's quoted.Split rules, not shell rules."""
    if not any(character in argument for character in " \t\r\n'\""):
        return argument
    if "'" not in argument:
        return "'" + argument + "'"
    if '"' not in argument:
        return '"' + argument + '"'
    raise ValueError('The temporary overlay path contains both quote types and cannot be encoded in GOFLAGS; choose another temporary directory')


def prepare_language_overlay(directory: Path, language='en-US'):
    """Create a temporary module view and return build flags plus stable provenance.

    The caller owns the temporary directory's lifetime and appends ``goFlag`` to
    the build process's existing GOFLAGS. Store ``manifest`` in release metadata;
    the outer report also contains temporary paths that should not be persisted.
    """
    result = subprocess.run(['go', 'list', '-m', '-json', MODULE], check=True,
                            capture_output=True, text=True, timeout=30)
    module = json.loads(result.stdout)
    if module.get('Path') != MODULE or module.get('Version') != VERSION or module.get('Replace'):
        raise RuntimeError(f'The Windows language overlay requires the unmodified pinned {MODULE}@{VERSION}')
    if not module.get('Dir'):
        # A fresh Linux runner can resolve Windows-only module metadata without
        # downloading its source. Installing the Linux Wails CLI does not hydrate
        # this dependency either. Ask Go to fetch the exact selected version,
        # retaining its normal checksum verification and cache reuse.
        try:
            result = subprocess.run(['go', 'mod', 'download', '-json', f'{MODULE}@{VERSION}'],
                                    check=True, capture_output=True, text=True, timeout=180)
        except subprocess.CalledProcessError as error:
            detail = (error.stderr or '').strip()
            try:
                detail = json.loads(error.stdout or '{}').get('Error') or detail
            except json.JSONDecodeError:
                pass
            raise RuntimeError(f'Could not download the pinned {MODULE}@{VERSION}: {detail or error}') from error
        except subprocess.TimeoutExpired as error:
            raise RuntimeError(f'Timed out downloading the pinned {MODULE}@{VERSION}; check Go module proxy access and retry') from error
        downloaded = json.loads(result.stdout)
        if (downloaded.get('Path') != MODULE or downloaded.get('Version') != VERSION
                or downloaded.get('Replace') or downloaded.get('Error') or not downloaded.get('Dir')):
            raise RuntimeError(f'The download did not provide source for the unmodified pinned {MODULE}@{VERSION}')
        module = downloaded
    original = Path(module['Dir']).absolute() / SOURCE_FILE
    source = original.read_bytes()
    patched = patch_language_source(source, language)
    main_module = subprocess.run(['go', 'env', 'GOMOD'], check=True,
                                capture_output=True, text=True, timeout=30)
    main_mod_path = Path(main_module.stdout.strip())
    if not main_mod_path.is_file():
        raise RuntimeError('The Windows language overlay must be prepared from the application Go module')
    main_mod = main_mod_path.read_bytes()
    main_sum_path = main_mod_path.with_suffix('.sum')
    directory = Path(directory).absolute()
    if directory.resolve().is_relative_to(Path(module['Dir']).resolve()) or directory.resolve().is_relative_to(Path(__file__).resolve().parent.parent):
        raise RuntimeError('The language overlay must use a temporary directory outside the checkout and pinned module directory')
    overlay_path = directory / 'overlay.json'
    patched_path = directory / 'create_env_go.go'
    module_copy = directory / 'go-webview2-v1.0.22'
    modfile_path = directory / 'app.mod'
    sumfile_path = directory / 'app.sum'
    go_flag = quote_go_flag('-modfile=' + str(modfile_path)) + ' ' + quote_go_flag('-overlay=' + str(overlay_path))
    # Never replace an existing scratch file or follow a destination symlink.
    for path in (patched_path, overlay_path, module_copy, modfile_path, sumfile_path):
        if path.exists() or path.is_symlink():
            raise RuntimeError(f'Refusing to replace an existing language overlay file: {path}')
    directory.mkdir(parents=True, exist_ok=True)
    # Go forbids overlays below GOMODCACHE. Resolve the same pinned module through
    # a temporary replace directive, then overlay its temporary copy instead.
    shutil.copytree(Path(module['Dir']), module_copy)
    replacement = '\nreplace ' + MODULE + ' ' + VERSION + ' => ' + json.dumps(str(module_copy)) + '\n'
    with modfile_path.open('xb') as output:
        output.write(main_mod + replacement.encode('utf-8'))
    if main_sum_path.is_file():
        with sumfile_path.open('xb') as output:
            output.write(main_sum_path.read_bytes())
    with patched_path.open('xb') as output:
        output.write(patched)
    overlay = {'Replace': {str(module_copy / SOURCE_FILE): str(patched_path)}}
    with overlay_path.open('x', encoding='utf-8', newline='\n') as output:
        json.dump(overlay, output, indent=2)
        output.write('\n')
    return {
        'overlayPath': str(overlay_path),
        'modfilePath': str(modfile_path),
        'goFlag': go_flag,
        'language': language,
        'manifest': {
            'module': MODULE,
            'version': VERSION,
            'sourceFile': SOURCE_FILE,
            'originalSHA256': SOURCE_SHA256,
            'patchedSHA256': hashlib.sha256(patched).hexdigest(),
            'api': 'webviewloader.WithLanguage',
            'method': 'temporary-module-replace-and-source-overlay',
            'language': language,
        },
    }
