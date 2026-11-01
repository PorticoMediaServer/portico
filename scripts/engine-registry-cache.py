#!/usr/bin/env python3
"""Opt-in hosted BuildKit cache hints; source verification remains authoritative."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

TARGETS = ('windows-x64', 'windows-arm64', 'linux-arm64')
IMAGE_ID = re.compile(r'sha256:[a-f0-9]{64}')


def sha(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def files(root, relative):
    root = Path(root).resolve()
    path = root / relative
    if path.is_symlink() or not path.resolve().is_relative_to(root):
        raise ValueError('cache input escaped recipe or is a symlink: ' + relative)
    if path.is_file():
        return dict(sha256=hashlib.sha256(path.read_bytes()).hexdigest(), mode=path.stat().st_mode & 0o777)
    if not path.is_dir():
        raise ValueError('missing cache input: ' + relative)
    result = {}
    for item in sorted(path.rglob('*')):
        if item.is_symlink():
            raise ValueError('cache input contains symlink: ' + str(item))
        if item.is_file():
            result[str(item.relative_to(path))] = dict(sha256=hashlib.sha256(item.read_bytes()).hexdigest(), mode=item.stat().st_mode & 0o777)
    return result


class RegistryCache:
    def __init__(self, target, env=None):
        env = os.environ if env is None else env
        if target not in TARGETS:
            raise ValueError('unsupported registry cache target')
        self.target = target
        self.enabled = env.get('PORTICO_ENGINE_REGISTRY_CACHE') == '1'
        self.write = env.get('PORTICO_ENGINE_CACHE_WRITE') == '1'
        if self.write and not self.enabled:
            raise ValueError('cache writes require explicit registry cache activation')
        if self.enabled and (env.get('GITHUB_ACTIONS') != 'true' or
                env.get('RUNNER_ENVIRONMENT') != 'github-hosted' or
                env.get('GITHUB_REPOSITORY') != 'PorticoMediaServer/portico' or
                env.get('GITHUB_EVENT_NAME') != 'workflow_dispatch' or
                not env.get('GITHUB_REF', '').startswith('refs/heads/') or
                not env.get('GITHUB_WORKFLOW_REF', '').startswith(
                    'PorticoMediaServer/portico/.github/workflows/ffmpeg-toolchain.yml@refs/heads/')):
            raise ValueError('registry cache requires explicit dedicated hosted workflow')
        self.receipts = {}

    def arguments(self, stage, key):
        if not re.fullmatch(r'[a-z0-9][a-z0-9-]*', stage) or not re.fullmatch(r'[a-f0-9]{64}', key):
            raise ValueError('invalid cache stage or key')
        if stage in ('portico-ffmpeg', 'portico-engine-output'):
            raise ValueError('final FFmpeg outputs cannot use registry cache')
        if not self.enabled:
            return []
        ref = f'ghcr.io/porticomediaserver/ffmpeg-cache-{self.target}:{stage}-{key}'
        graph_ref = f'ghcr.io/porticomediaserver/ffmpeg-cache-{self.target}:{stage}-graph'
        # The rolling hint preserves unchanged internal layers after one input
        # changes. BuildKit still validates each operation and source digest.
        self.receipts[stage] = dict(key=key, ref=ref, graphRef=graph_ref, read=True, write=self.write, mode='max')
        args = ['--cache-from', 'type=registry,ref=' + ref,
                '--cache-from', 'type=registry,ref=' + graph_ref]
        if self.write:
            for destination in (ref, graph_ref):
                args += ['--cache-to', 'type=registry,ref=' + destination + ',mode=max']
        return args


def context_key(recipe, relative, dependencies=None, source_date_epoch=None):
    # Contexts contain the compiler recipe/config and capture scripts, never
    # the FFmpeg configure script or its packaging inputs.
    return sha(dict(schema=1, context=files(recipe, relative), dependencies=dependencies or {},
                    sourceDateEpoch=(os.environ.get('SOURCE_DATE_EPOCH', '') if source_date_epoch is None
                                     else str(source_date_epoch))))


def block_inputs(recipe, block):
    mounted = {relative: files(recipe, relative)
               for relative in re.findall(r'--mount=src=([^,\s]+)', block)}
    # COPY context inputs (not stage copies) also affect execution, e.g.
    # base-layer's util/run_stage.sh. Refuse syntax we cannot account for.
    for line in block.splitlines():
        if not re.match(r'(?i)^COPY\s', line) or '--from=' in line:
            continue
        words = [v for v in line.split()[1:] if not v.startswith('--')]
        if len(words) < 2 or any(c in ' '.join(words[:-1]) for c in '*?[$'):
            raise ValueError('unmodelled Docker COPY cache input')
        for relative in words[:-1]:
            mounted[relative] = files(recipe, relative)
    return mounted


def stage_key(recipe, name, deps, compiler_id, results):
    if not IMAGE_ID.fullmatch(compiler_id):
        raise ValueError('actual immutable compiler image identity required')
    text = (recipe / 'Dockerfile').read_text()
    blocks = re.split(r'(?im)^(?=FROM )', text)
    block = next(b for b in blocks if re.search(r'(?im)^FROM \S+ AS ' + re.escape(name) + r'$', b))
    mounted = block_inputs(recipe, block)
    return sha(dict(schema=1, stage=block, sources=mounted, compiler=compiler_id,
                    dependencies={dep: results[dep]['key'] for dep in deps}))


def configure_makeimage(recipe, helper, target):
    """Replace the six pinned local-cache hooks, preserving the Docker graph."""
    path = recipe / 'makeimage.sh'
    text = path.read_text()
    # Reset by caller from pinned Git on every run; never silently double patch.
    for variable, phase in [('BASE_IMAGE', 'base'), ('TARGET_IMAGE', 'compiler'), ('IMAGE', 'deps')]:
        old = ('--cache-from=type=local,src=.cache/"${' + variable + '/:/_}" \\\n' +
               ' ' * (12 if phase != 'deps' else 4) +
               '--cache-to=type=local,mode=max,dest=.cache/"${' + variable + '/:/_}"')
        if text.count(old) != 1:
            raise ValueError('pinned makeimage cache hooks changed: ' + phase)
        # mapfile cannot propagate process substitution failure: command
        # substitution assignment must succeed before parsing its lines.
        marker = ('        docker buildx --builder ffbuilder build \\\n' if phase != 'deps' else
                  'docker buildx --builder ffbuilder build \\\n')
        # Match the original unique pair, before replacing it with an array.
        boundary = marker + (' ' * (12 if phase != 'deps' else 4)) + old
        if text.count(boundary) != 1:
            raise ValueError('pinned makeimage command boundary changed: ' + phase)
        compiler = (' --compiler-image "$TARGET_IMAGE"' if phase == 'deps' else
                    ' --compiler-image "$BASE_IMAGE"' if phase == 'compiler' else '')
        setup = ('PORTICO_CACHE_LINES="$(python3 "$PORTICO_REGISTRY_CACHE_HELPER" args --recipe "$PWD"'
                 ' --target "$PORTICO_REGISTRY_CACHE_TARGET" --phase ' + phase + compiler + ')"\n'
                 'PORTICO_CACHE_ARGS=()\n'
                 'if [[ -n "$PORTICO_CACHE_LINES" ]]; then mapfile -t PORTICO_CACHE_ARGS <<< "$PORTICO_CACHE_LINES"; fi\n')
        replacement = (marker + (' ' * (12 if phase != 'deps' else 4)) +
                       '--build-arg SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:?Explicit source epoch required}" \\\n' +
                       (' ' * (12 if phase != 'deps' else 4)) + '"${PORTICO_CACHE_ARGS[@]}"')
        text = text.replace(boundary, setup + replacement)
    # A task-local prefix must not salt either BuildKit's graph or stage key.
    # The normal upstream namespace is stable; Windows uses its own adapter.
    path.write_text(text)


def makeimage_args(recipe, target, phase, compiler_image=None):
    policy = RegistryCache(target)
    if phase == 'base':
        key = context_key(recipe, 'images/base')
    elif phase == 'compiler':
        base_id = subprocess.check_output(['docker', 'image', 'inspect', '-f', '{{.Id}}', compiler_image], text=True).strip()
        if not IMAGE_ID.fullmatch(base_id):
            raise ValueError('actual base image identity required')
        key = context_key(recipe, 'images/base-' + {'linux-arm64': 'linuxarm64',
                          'windows-arm64': 'winarm64', 'windows-x64': 'win64'}[target],
                          {'base': context_key(recipe, 'images/base'), 'baseImageId': base_id})
    else:
        identity = subprocess.check_output(['docker', 'image', 'inspect', '-f', '{{.Id}}', compiler_image], text=True).strip()
        if not IMAGE_ID.fullmatch(identity):
            raise ValueError('actual dependency compiler image identity required')
        text = (recipe / 'Dockerfile').read_text()
        # makeimage only builds dependencies: final compilation is build.sh.
        if 'portico-ffmpeg' in text:
            raise ValueError('makeimage dependency graph contains final FFmpeg stage')
        # Include copied runner helpers; these drive all dependency stages.
        key = sha(dict(schema=1, compiler=identity, dockerfile=text,
                       sources=block_inputs(recipe, text),
                       runner=files(recipe, 'util/run_stage.sh')))
    args = policy.arguments(phase, key)
    receipt = recipe.parent / ('portico-registry-cache-' + phase + '.json')
    receipt.write_text(json.dumps(dict(enabled=policy.enabled, target=target, stages=policy.receipts), indent=2) + '\n')
    return args


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('configure-makeimage', 'args'))
    parser.add_argument('--recipe', required=True, type=Path)
    parser.add_argument('--target', required=True, choices=TARGETS)
    parser.add_argument('--phase', choices=('base', 'compiler', 'deps'))
    parser.add_argument('--compiler-image')
    args = parser.parse_args()
    RegistryCache(args.target)  # refuse untrusted opt-in before mutations
    if args.action == 'configure-makeimage':
        configure_makeimage(args.recipe, Path(__file__).resolve(), args.target)
    else:
        if not args.phase or (args.phase in ('compiler', 'deps') and not args.compiler_image):
            parser.error('phase and dependency compiler image required')
        print('\n'.join(makeimage_args(args.recipe, args.target, args.phase, args.compiler_image)))
