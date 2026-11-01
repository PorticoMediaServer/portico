#!/usr/bin/env python3
"""Cache dependency prefix/source deltas around the checked native Mac recipe."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream, 'sha256').hexdigest()


def module():
    spec = importlib.util.spec_from_file_location('prefix_cache', Path(__file__).with_name('engine-prefix-cache.py'))
    value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value); return value


def wrapper(name, body):
    # No function/conditional around the real body: Bash errexit must retain
    # its original behavior. A failed dependency can never create a cache hit.
    return ('portico_cache_result=$(portico_dependency_cache begin "' + name + '" "") || exit "$?"\n'
            'if [[ "$portico_cache_result" == MISS ]]; then\n' + body +
            '\nportico_dependency_cache finish "' + name + '" "" >/dev/null || exit "$?"\nfi\n')


def configure(recipe, root=ROOT):
    if not os.environ.get('PORTICO_ENGINE_STAGE_CACHE'):
        return
    homebridge = recipe / 'build-ffmpeg'; text = homebridge.read_text()
    if 'portico_dependency_cache()' in text:
        raise ValueError('fresh Mac dependency cache instrumentation required')
    programs = {}
    for match in re.finditer(r'(?ms)^\s*if build "([^"\n]+)" "([^"\n]+)"; then\n(.*?)^\s*build_done "\1"[^\n]*', text):
        name, version, body = match.groups()
        if name != 'ffmpeg': programs[name] = dict(version=version, program=body)
    if not programs or 'cmake' not in programs:
        raise ValueError('pinned Homebridge dependency stage boundaries changed')
    functions = text[text.index('# Utilities.'):text.index('while (($# > 0)); do')]
    functions = re.sub(r'(?m)^.*CONFIGURE_OPTIONS.*\n?', '', functions)
    hooks = '''portico_dependency_cache() {
  PORTICO_STAGE_CFLAGS="$CFLAGS" PORTICO_STAGE_LDFLAGS="$LDFLAGS" \\
  PORTICO_STAGE_LDEXEFLAGS="$LDEXEFLAGS" PORTICO_STAGE_LIBS="$EXTRALIBS" \\
    python3 "$CWD/portico-macos-dependency-cache.py" "$1" --recipe "$CWD" --stage "$2" --version "$3"
}
'''
    marker = 'build() {\n'
    if text.count(marker) != 1 or text.count('build_done() {\n') != 1:
        raise ValueError('pinned Mac build/cache hooks changed')
    text = text.replace(marker, hooks + marker + '''  if [[ "$1" != ffmpeg ]]; then
    portico_cache_result=$(portico_dependency_cache begin "$1" "$2") || exit "$?"
  else
    # A cache hit never counts as a repeated FFmpeg compilation.
    if [[ -f "$PACKAGES/ffmpeg.done" ]]; then python3 -c 'import pathlib,sys;pathlib.Path(sys.argv[1]).unlink()' "$PACKAGES/ffmpeg.done"; fi
  fi
''', 1)
    # Save after the checked recipe's own completion marker has been written.
    end = '  echo "$2" >"${PACKAGES}/$1.done"\n'
    if text.count(end) != 1:
        raise ValueError('pinned Mac dependency completion marker changed')
    text = text.replace(end, end + '  if [[ "$1" != ffmpeg ]]; then portico_dependency_cache finish "$1" "$2" >/dev/null || exit "$?"; fi\n', 1)
    homebridge.write_text(text)
    extras = recipe / 'portico-macos-extras.sh'
    original = extras.read_text()
    matches = list(re.finditer(r'(?m)^portico_clone ([A-Za-z0-9]+)\n', original))
    if not matches:
        raise ValueError('pinned Mac extras stage boundaries changed')
    transformed = original[:matches[0].start()]
    helper_functions = original[:original.index('export PKG_CONFIG_PATH=')]
    for index, match in enumerate(matches):
        name = 'portico-' + match[1]
        body = original[match.start():matches[index + 1].start() if index + 1 < len(matches) else len(original)]
        exports = '\n'.join(re.findall(r'(?m)^export CFLAGS=.*$', body))
        body = re.sub(r'(?m)^export CFLAGS=.*\n', '', body)
        if exports: transformed += exports + '\n'
        programs[name] = dict(program=body, helpers=helper_functions)
        transformed += wrapper(name, body)
    # libdovi precedes the clone-driven extras. Its sources, vendor tree,
    # notices and native tools are tracked roots, not disposable cache metadata.
    dovi = re.search(r'(?m)^python3 "\$CWD/portico-build-macos-libdovi.py" build[^\n]*\\\n[^\n]*\n', transformed)
    if dovi:
        body = dovi[0]; programs['portico-libdovi'] = dict(program=body)
        transformed = transformed[:dovi.start()] + wrapper('portico-libdovi', body) + transformed[dovi.end():]
    extras.write_text(transformed)
    zvbi = recipe / 'portico-build-macos-zvbi.sh'
    if zvbi.is_file():
        ztext = zvbi.read_text()
        start = ztext.index(' [ ! -e "$tools" ]')
        split = ztext.index(' portico_clone zvbi\n', start)
        finish = ztext.rindex('\n}\nportico_zvbi')
        gettext, library = ztext[start:split], ztext[split:finish]
        exports = re.findall(r'(?m)^ export (?:PATH|ACLOCAL_PATH)=.*$', gettext)
        for line in exports: gettext = gettext.replace(line + '\n', '')
        programs['portico-gettext-tools'] = dict(program=gettext)
        programs['portico-zvbi'] = dict(program=library)
        ztext = ztext[:start] + wrapper('portico-gettext-tools', gettext) + '\n'.join(exports) + '\n' + wrapper('portico-zvbi', library) + ztext[finish:]
        zvbi.write_text(ztext)
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    # FFmpeg source, configure flags and packaging code are deliberately not
    # dependency inputs. Each stage's actual body and dependency state are.
    context = dict(target=os.environ['PORTICO_FFMPEG_TARGET'],
                   homebridge=lock['macosRecipe']['commit'],
                   patches={p.name: digest(p) for p in (root / 'third_party/ffmpeg/patches').glob('homebridge-*.patch')},
                   extras=json.loads((recipe / 'portico-macos-extras.lock.json').read_text()),
                   nativeRust=lock.get('nativeRustTools'), libdovi=lock.get('libdovi'),
                   compiler=json.loads(os.environ['PORTICO_ENGINE_MAC_COMPILER']),
                   commonFunctionsSHA256=hashlib.sha256(functions.encode()).hexdigest(),
                   cacheAdapterSHA256=digest(Path(__file__)))
    helper_names = ('portico-build-macos-libdovi.py', 'portico-rust-tools.py', 'portico-configure-macos-shaderc.py',
                    'portico-macos-ffmpeg-source.py', 'portico-macos-system-libs.py')
    context['helpers'] = {name: digest(recipe / name) for name in helper_names if (recipe / name).is_file()}
    (recipe / 'portico-dependency-cache.json').write_text(json.dumps(dict(context=context, stages=programs), sort_keys=True) + '\n')
    shutil.copyfile(Path(__file__), recipe / 'portico-macos-dependency-cache.py')
    shutil.copyfile(Path(__file__).with_name('engine-prefix-cache.py'), recipe / 'engine-prefix-cache.py')


def stage(action, recipe, name, version):
    plan = json.loads((recipe / 'portico-dependency-cache.json').read_text())
    if name not in plan['stages'] or name == 'ffmpeg':
        raise ValueError('unknown dependency cache stage')
    roots = dict(prefix=recipe / 'workspace', packages=recipe / 'packages',
                 gettext=recipe.parent / 'gettext-tools', rust=recipe.parent / 'native-rust-tools')
    inputs = dict(**plan['context'], stage=name, version=version, program=plan['stages'][name],
                  flags={key: os.environ.get(key, '') for key in ('PORTICO_STAGE_CFLAGS', 'PORTICO_STAGE_LDFLAGS',
                      'PORTICO_STAGE_LDEXEFLAGS', 'PORTICO_STAGE_LIBS', 'MACOSX_DEPLOYMENT_TARGET',
                      'SDKROOT', 'CC', 'CXX', 'AR', 'RANLIB', 'PORTICO_BUILD_JOBS', 'MAKEFLAGS')})
    cache = module().PrefixCache(Path(os.environ['PORTICO_ENGINE_STAGE_CACHE']) / 'macos', recipe.parent / '.dependency-cache-state')
    result = cache.begin(inputs, roots) if action == 'begin' else cache.finish(inputs, roots)
    evidence = recipe.parent / 'dependency-cache-receipts'; evidence.mkdir(exist_ok=True)
    if action == 'finish' or result['hit']:
        value = result.get('receipt', result)
        (evidence / (name + '.json')).write_text(json.dumps(dict(cacheHit=bool(result.get('hit')), **value), sort_keys=True) + '\n')
    return ('HIT' if result['hit'] else 'MISS') if action == 'begin' else 'SAVED'


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('configure', 'begin', 'finish'))
    parser.add_argument('--recipe', type=Path, required=True)
    parser.add_argument('--stage'); parser.add_argument('--version', default='')
    args = parser.parse_args()
    if args.action == 'configure': configure(args.recipe.resolve())
    else: print(stage(args.action, args.recipe.resolve(), args.stage, args.version))
