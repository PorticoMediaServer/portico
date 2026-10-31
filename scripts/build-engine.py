#!/usr/bin/env python3
"""Local Windows dependency diagnostic runner. Not engine/native qualification.

Invoke through portico-heavy D-engine. Successful BuildKit dependency caches
retain their complete source downloads in the owned cache, never just binaries.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
import uuid
import signal
import fcntl

spec = importlib.util.spec_from_file_location("engine_inputs", Path(__file__).with_name("engine_inputs.py"))
inputs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inputs)
spec = importlib.util.spec_from_file_location('final_cache', Path(__file__).with_name('engine-final-cache.py'))
final_cache = importlib.util.module_from_spec(spec)
spec.loader.exec_module(final_cache)

ROOT = Path(__file__).resolve().parents[1]
LIMIT = 8 * 1024**3


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def inventory(root):
    return {str(p.relative_to(root)): digest(p) for p in sorted(root.rglob('*')) if p.is_file()}


def atomic(path, value):
    temp = path.with_suffix('.new')
    temp.write_text(json.dumps(value, indent=2) + '\n')
    temp.replace(path)


def stages(text):
    """Read actual generated Docker graph; reject unmodelled stage references."""
    result = {}
    current = None
    for line in text.splitlines():
        match = re.match(r'FROM\s+(\S+)(?:\s+AS\s+(\S+))?$', line, re.I)
        if match:
            parent, name = match.groups()
            current = name
            if name:
                result[name] = [parent] if parent in result else []
        elif current:
            for source in re.findall(r'--from=(\S+)', line):
                if source not in result:
                    raise ValueError('unmodelled Docker stage: ' + source)
                if source not in result[current]:
                    result[current].append(source)
    if not result or 'base-layer' not in result:
        raise ValueError('generated Docker graph changed')
    return result


def aggregate(graph, execute, update=lambda _: None):
    results = {}
    for name, deps in graph.items():
        blocked = [d for d in deps if results[d]['state'] in ('failed', 'blocked')]
        if blocked:
            results[name] = {'state': 'blocked', 'prerequisites': blocked}
        else:
            try:
                results[name] = execute(name, deps)
            except Exception as error:
                results[name] = {'state': 'failed', 'error': str(error)}
        update(results)
    return results


def dependency_steps(text):
    """Split ordered multi-library stages without changing their operations.

    Each source-build RUN becomes an addressable target. Group outputs still
    have the original alias, so existing prefix copies and final flags stay put.
    """
    result = []
    for block in re.split(r'(?im)^(?=FROM )', text):
        lines = block.splitlines(keepends=True)
        header = re.fullmatch(r'FROM (\S+) AS (\S+)\n?', lines[0], re.I) if lines else None
        runs = [i for i, line in enumerate(lines) if re.search(r'^RUN --mount=src=scripts\.d/', line)]
        if header is None or len(runs) <= 1:
            result.append(block)
            continue
        parent, name = header.groups()
        lines[0] = f'FROM {parent} AS {name}-input\n'
        previous = name + '-input'
        insertions = {}
        for index in runs:
            script = re.search(r'--mount=src=(scripts\.d/[^,]+)', lines[index]).group(1)
            alias = name + '-' + Path(script).stem
            before = index - 1 if lines[index - 1].startswith('ENV SELF=') else index
            insertions[before] = f'FROM {previous} AS {alias}\n'
            previous = alias
        result.append(''.join(insertions.get(i, '') + line for i, line in enumerate(lines)))
        result.append(f'FROM {previous} AS {name}\n')
    return ''.join(result)


def cached_mounts(text):
    if isinstance(text, bytes):
        text = text.decode('utf-8', errors='replace')
    steps = set(re.findall(r'(?m)^#(\d+) \[[^\n]+\] RUN --mount=', text))
    cached = set(re.findall(r'(?m)^#(\d+) CACHED$', text))
    return bool(steps) and steps <= cached


def download_sources(recipe, execute, update=lambda _: None):
    results, failed = {}, set()
    for script, archive in inputs.source_pairs(recipe, require_files=False).items():
        try:
            execute(script)
            results[script] = {'state': 'passed', 'archive': archive, 'sha256': digest(recipe / archive)}
        except Exception as error:
            failed.add(archive)
            results[script] = {'state': 'failed', 'error': str(error)}
        update(results)
    return results, failed


def stage_key(recipe, name, deps, compiler_id, results):
    text = (recipe / 'Dockerfile').read_text()
    blocks = re.split(r'(?im)^(?=FROM )', text)
    block = next(block for block in blocks if re.search(r'(?im)^FROM \S+ AS ' + re.escape(name) + r'$', block))
    mounted = {}
    for relative in re.findall(r'--mount=src=([^,\s]+)', block):
        path = recipe / relative
        if not path.resolve().is_relative_to(recipe.resolve()):
            raise ValueError('source mount escaped recipe')
        if path.is_file():
            mounted[relative] = digest(path)
        elif path.is_dir():
            mounted[relative] = inventory(path)
        else:
            raise ValueError('missing dependency source: ' + relative)
    return hashlib.sha256(json.dumps({'stage': block, 'sources': mounted, 'compiler': compiler_id,
        'dependencies': {dep: results[dep]['key'] for dep in deps}}, sort_keys=True).encode()).hexdigest()


def prune(cache, limit=LIMIT):
    entries = sorted((p for p in cache.glob('stage-*') if p.is_dir()), key=lambda p: p.stat().st_mtime)
    sizes = {p: sum(f.stat().st_size for f in p.rglob('*') if f.is_file()) for p in entries}
    for p in entries:
        if sum(sizes.values()) <= limit:
            break
        shutil.rmtree(p)
        sizes.pop(p)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('target', choices=['windows-x64'])
    p.add_argument('--state', type=Path, required=True, help='fresh owned on-disk state directory')
    a = p.parse_args()
    # Re-exec the entire lifetime under the machine's existing lock. No queue.
    if os.environ.get('PORTICO_ENGINE_LOCKED') != '1':
        env = dict(os.environ, PORTICO_ENGINE_LOCKED='1')
        return subprocess.call(['portico-heavy', 'D-engine', '--', sys.executable, __file__, *sys.argv[1:]], env=env)
    state = a.state.absolute()
    if state.exists() and (state.is_symlink() or not (state / 'owner.json').is_file()):
        raise ValueError('state must be fresh or owned by this runner')
    state.mkdir(parents=True, exist_ok=True)
    if str(state).startswith(('/tmp/', '/dev/shm/')):
        raise ValueError('scratch must be on disk')
    if shutil.disk_usage(state).free < 30 * 1024**3:
        raise ValueError('less than 30 GiB free')
    state_lock = (state / 'run.lock').open('a')
    fcntl.flock(state_lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    owner = 'portico-d-engine-' + hashlib.sha256(str(state).encode()).hexdigest()[:12]
    atomic(state / 'owner.json', {'runner': str(Path(__file__).resolve()), 'release': False})
    cache = state / 'cache'
    cache.mkdir(exist_ok=True)
    report = {'target': a.target, 'context': 'local', 'qualified': False, 'stages': {},
              'unexercised': ['bundle assembly', 'native runtime', 'ABI', 'repeat build', 'installer']}
    scratch = Path(tempfile.mkdtemp(prefix='scratch-', dir=state))
    def interrupted(signum, frame):
        raise KeyboardInterrupt('engine interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    images = []
    env = dict(os.environ, TMPDIR=str(scratch), PORTICO_BUILD_JOBS='2', MAKEFLAGS='-j1',
               NUMJOBS='2', CARGO_BUILD_JOBS='2', CMAKE_BUILD_PARALLEL_LEVEL='2', RAYON_NUM_THREADS='2')
    def run(name, command, cwd=ROOT):
        with (state / (name + '.log')).open('w') as log:
            code = subprocess.call([str(x) for x in command], cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT)
        if code:
            raise RuntimeError(f'{name} exit {code}; see {state / (name + ".log")}')
    def telemetry(label):
        with (state / 'resources.log').open('a') as log:
            log.write(label + ' ' + time.strftime('%FT%TZ', time.gmtime()) + '\n'); log.flush()
            for command in (['df', '-h', str(state)], ['free', '-h']):
                subprocess.run(command, stdout=log, stderr=log)
    try:
        telemetry('start')
        source_sha = inputs.checked_source(ROOT)
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        report.update(engineSourceSHA=source_sha, sourceAuthoritySHA256=lock['sourceAuthority']['sha256'])
        source_work = state / '.scratch/hardware-r29'
        env.update(PORTICO_LANE_SCRATCH=str(source_work), PORTICO_FFMPEG_SOURCE_ONLY='1')
        run('source', ['bash', ROOT / 'scripts/build-ffmpeg.sh', a.target, source_work / 'source'])
        env.pop('PORTICO_FFMPEG_SOURCE_ONLY')
        recipe = state / 'recipe'
        if not recipe.exists():
            run('clone', ['git', 'clone', lock['btbn']['repository'], recipe])
        else:
            run('restore', ['git', 'restore', '--source=' + lock['btbn']['commit'], '--', '.'], recipe)
        run('checkout', ['git', 'checkout', '--detach', lock['btbn']['commit']], recipe)
        for patch in ('btbn-gnu-ncurses-mirror.patch', 'btbn-verified-source.patch'):
            run(patch, ['git', 'apply', ROOT / 'third_party/ffmpeg/patches' / patch], recipe)
        run('system-error', ['bash', ROOT / 'scripts/patch-btbn-winarm64-system-error.sh', recipe / 'build.sh'])
        env['PORTICO_DISC_SOURCE_COMMIT'] = lock['discHelper']['sourceCommit']
        prepared = next((source_work / 'source').glob('patched-source*'))
        run('configure', [sys.executable, ROOT / 'scripts/configure-ffmpeg-recipe.py', recipe, prepared, a.target])
        shutil.copy2(next((source_work / 'source').glob('ffmpeg-*.tar.xz')), recipe / 'portico-ffmpeg-source.tar.xz')
        spec = importlib.util.spec_from_file_location('adapter', ROOT / 'scripts/engine-recipe-adapter.py')
        adapter = importlib.util.module_from_spec(spec); spec.loader.exec_module(adapter)
        adapter.prepare_recipe(recipe, owner)
        # Every image name is task-owned. No pulls from/pushes to this namespace.
        env.update(PORTICO_ENGINE_REPOSITORY=owner + '/engine', REGISTRY_OVERRIDE='local')
        base = 'local/' + owner + '/engine/base:latest'
        compiler = 'local/' + owner + '/engine/base-win64:latest'
        images.extend([compiler, base])
        config = scratch / 'buildkit.toml'
        config.write_text('[worker.oci]\n max-parallelism = 1\n gc = true\n reservedSpace = "1GB"\n maxUsedSpace = "12GB"\n')
        run('generate', ['bash', 'generate.sh', 'win64', 'gpl', '8.1'], recipe)
        (recipe / 'Dockerfile').write_text(dependency_steps((recipe / 'Dockerfile').read_text()))
        graph = stages((recipe / 'Dockerfile').read_text())
        report['stages'] = {n: {'state': 'pending'} for n in graph}
        atomic(state / 'report.json', report)
        run('builder', ['docker', 'buildx', 'create', '--name', owner, '--driver', 'docker-container', '--config', config,
                        '--driver-opt', 'env.BUILDKIT_STEP_LOG_MAX_SIZE=67108864', '--driver-opt', 'memory=4g', '--driver-opt', 'cpu-quota=200000', '--driver-opt', 'cpu-period=100000'])
        common = ['docker', 'buildx', '--builder', owner, 'build', '--progress=plain']
        run('base', common + ['--load', '-t', base, 'images/base'], recipe)
        # buildx docker-container needs an explicit OCI context for local images.
        base_oci = scratch / 'base-oci'
        run('base-export', common + ['--output', 'type=oci,tar=false,dest=' + str(base_oci), 'images/base'], recipe)
        run('compiler', common + ['--load', '-t', compiler, '--build-arg', 'GH_REPO=local/' + owner + '/engine',
            '--build-context', base + '=oci-layout://' + str(base_oci), 'images/base-win64'], recipe)
        compiler_id = subprocess.check_output(['docker', 'image', 'inspect', '-f', '{{.Id}}', compiler], text=True).strip()
        # Export from the completed, exact image, never from BuildKit's private
        # executor filesystem or an independently fetched compiler snapshot.
        source_export = state / 'custody' / ('compiler-' + compiler_id.removeprefix('sha256:'))
        if not source_export.exists():
            pending_export = scratch / 'compiler-source-export'
            pending_export.mkdir()
            export_container = owner + '-compiler-inputs'
            created = False
            try:
                run('compiler-custody-container', ['docker', 'create', '--name', export_container, compiler_id, 'true'])
                created = True
                run('compiler-custody-export', ['docker', 'cp', export_container + ':/opt/portico-compiler-source/.', pending_export])
                inputs.verify_compiler_sources(pending_export)
                source_export.parent.mkdir(parents=True, exist_ok=True)
                pending_export.rename(source_export)
            finally:
                if created:
                    subprocess.run(['docker', 'rm', export_container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        compiler_inputs = recipe / 'portico-compiler-inputs'
        if compiler_inputs.is_symlink():
            raise ValueError('compiler source output must not be a symlink')
        if compiler_inputs.exists():
            shutil.rmtree(compiler_inputs)
        shutil.copytree(source_export, compiler_inputs)
        report.update(compilerImageId=compiler_id,
                      compilerSourceInventorySHA256=digest(compiler_inputs / 'compiler-inputs.json'))
        atomic(recipe / 'portico-compiler-image.json', {'compilerImageId': compiler_id,
            'sourceInventorySHA256': report['compilerSourceInventorySHA256']})
        inputs.compiler_inputs(recipe, report)
        atomic(state / 'report.json', report)
        # BuildKit also uses the host SOURCE_DATE_EPOCH for image config/history
        # timestamps. Export in the completed image's original environment,
        # before the final FFmpeg payload sets its epoch below.
        compiler_dir = scratch / 'compiler-layout'
        run('compiler-export', common + ['--build-arg', 'GH_REPO=local/' + owner + '/engine',
            '--build-context', base + '=oci-layout://' + str(base_oci),
            '--output', 'type=oci,tar=false,dest=' + str(compiler_dir), 'images/base-win64'], recipe)
        report['compilerContext'] = inputs.compiler_layout(compiler_dir, compiler_id)
        atomic(state / 'report.json', report)
        def publish_downloads(results):
            report['downloads'] = dict(results)
            atomic(state / 'report.json', report)
        report['downloads'], failed_sources = download_sources(recipe,
            lambda script: run('download-' + Path(script).stem, ['bash', 'download.sh', Path(script).name], recipe),
            publish_downloads)
        run('generate', ['bash', 'generate.sh', 'win64', 'gpl', '8.1'], recipe)
        (recipe / 'Dockerfile').write_text(dependency_steps((recipe / 'Dockerfile').read_text()))
        if 'scripts.d/50-vaapi/50-libva.sh' in (recipe / 'Dockerfile').read_text():
            raise ValueError('Windows graph unexpectedly contains libva')
        disc = recipe / 'portico-disc'; disc.mkdir(exist_ok=True)
        for name in ('portico-disc.c', 'mpegts_dovi.h'):
            shutil.copy2(ROOT / 'third_party/portico-disc' / name, disc)
        shutil.copy2(ROOT / 'LICENSE', disc / 'LICENSE')
        shutil.copy2(ROOT / 'scripts/build-portico-disc.py', disc)
        env['SOURCE_DATE_EPOCH'] = str(lock['sourceDateEpoch'])
        final_cache.compile_program(recipe, 'win64', recipe / 'portico-compile.sh', env)
        dockerfile = recipe / 'Dockerfile'
        dockerfile.write_text(final_cache.stages(dockerfile.read_text(), os.getuid(), os.getgid(),
                                                lock['sourceDateEpoch']))
        dockerfile_sha = digest(dockerfile)
        graph = stages(inputs.consumed_dockerfile(recipe, dockerfile_sha).read_text())
        report['stages'] = {n: {'state': 'pending'} for n in graph}
        atomic(state / 'report.json', report)
        # Capture the file BuildKit actually consumes, after final generation.
        # No separately regenerated Dockerfile supplies the sealed receipt.
        recipe_sha = inputs.json_digest(inputs.recipe_tree(recipe))
        report.update(dockerfileSHA256=dockerfile_sha, recipeTreeSHA256=recipe_sha,
                      configureScripts=inputs.configure_scripts(ROOT))
        atomic(state / 'report.json', report)
        # Source snapshots stay in the owned recipe cache across retries. Never
        # use a successful layer as a substitute for corresponding source.
        custody = state / 'custody'
        custody.mkdir(exist_ok=True)
        atomic(custody / 'recipe-inputs.json', {str(p.relative_to(recipe)): digest(p)
            for p in sorted(recipe.rglob('*')) if p.is_file() and '.git' not in p.parts
            and '.cache' not in p.parts})
        stage_results = {}
        def publish(results):
            stage_results.update(results)
            report['stages'].update(results)
            atomic(state / 'report.json', report)
        def execute(name, deps):
            telemetry(name)
            # BuildKit validates content-addressed cached layers itself. Always
            # request the target: a receipt alone cannot prove a GC'd layer exists.
            # Stable namespace, compiler identity and context make retries hit
            # unchanged stages while changed inputs invalidate their descendants.
            block = next(b for b in re.split(r'(?im)^(?=FROM )', (recipe / 'Dockerfile').read_text())
                if re.search(r'(?im)^FROM \S+ AS ' + re.escape(name) + r'$', b))
            unavailable = sorted(failed_sources & set(re.findall(r'--mount=src=([^,\s]+)', block)))
            if unavailable:
                return {'state': 'blocked', 'prerequisites': ['download:' + p for p in unavailable]}
            key = stage_key(recipe, name, graph[name], compiler_id, stage_results)
            metadata = state / ('metadata-' + name + '.json')
            args = common + ['--file', inputs.consumed_dockerfile(recipe, dockerfile_sha),
                             '--target', name, '--build-context', compiler + '=oci-layout://' + str(compiler_dir),
                             '--metadata-file', metadata]
            run('dependency-' + name, args + ['.'], recipe)
            inputs.consumed_dockerfile(recipe, dockerfile_sha)
            log = state / ('dependency-' + name + '.log')
            result = {'state': 'cached' if cached_mounts(log.read_bytes()) else 'passed', 'key': key,
                      'compilerImageId': compiler_id, 'logSHA256': digest(log)}
            if metadata.exists():
                result['metadataSHA256'] = digest(metadata)
            atomic(cache / (name + '.json'), result)
            return result
        report['stages'] = aggregate(graph, execute, publish)
        if all(v['state'] in ('passed', 'cached') for v in report['stages'].values()):
            prefix = scratch / 'engine-prefix'
            args = common + ['--file', inputs.consumed_dockerfile(recipe, dockerfile_sha),
                             '--target', 'portico-engine-output',
                             '--output', 'type=local,dest=' + str(prefix),
                             '--build-context', compiler + '=oci-layout://' + str(compiler_dir)]
            run('ffmpeg-export', args + ['.'], recipe)
            inputs.consumed_dockerfile(recipe, dockerfile_sha)
            package = scratch / ('ffmpeg-' + lock['ffmpeg']['version'] + '-win64-gpl-8.1')
            final_cache.package(recipe, 'win64', prefix, prepared, package, env)
            if inputs.json_digest(inputs.recipe_tree(recipe)) != recipe_sha:
                raise ValueError('recipe inputs changed during compilation')
            if inputs.checked_source(ROOT) != source_sha:
                raise ValueError('engine source changed during compilation')
            raw_output = state / ('raw-' + uuid.uuid4().hex[:12])
            raw_output.mkdir()
            final_cache.archive(package, raw_output / (package.name + '.zip'))
            archives = sorted(raw_output.glob('*.zip'))
            if not archives:
                raise ValueError('compiler produced no Windows archive')
            report.update(rawOutputRoot=str(raw_output),
                rawArchives={p.name: digest(p) for p in archives}, recipeFiles=inputs.recipe_inventory(recipe))
            shutil.copytree(disc, custody / 'portico-disc', dirs_exist_ok=True)
            shutil.copytree(ROOT / 'scripts', custody / 'portico-build-scripts', ignore=shutil.ignore_patterns('__pycache__'), dirs_exist_ok=True)
            shutil.copytree(ROOT / 'third_party/ffmpeg', custody / 'portico-contract', dirs_exist_ok=True)
            report.update(rawCompilation=True, compilationTransport='buildkit',
                          compilationState=report['stages']['portico-ffmpeg']['state'])
            atomic(raw_output / 'build-report.json', report)
    except (Exception, KeyboardInterrupt) as error:
        report['blocker'] = str(error)
        for name, entry in report['stages'].items():
            if entry['state'] == 'pending':
                report['stages'][name] = {'state': 'blocked', 'prerequisites': ['bootstrap/source-download'], 'error': str(error)}
    finally:
        # systemd may signal the whole process group again while it drains.
        # Finish evidence/cache cleanup under the held lease before exiting.
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        telemetry('finish')
        # Only the unique builder and image names allocated by this invocation.
        subprocess.run(['docker', 'buildx', 'rm', '--keep-state', owner], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for image in images:
            subprocess.run(['docker', 'image', 'rm', image], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        shutil.rmtree(scratch)
        atomic(state / 'report.json', report)
    print(json.dumps(report, indent=2))
    return 1 if report.get('blocker') or any(v['state'] in ('failed', 'blocked') for v in report['stages'].values()) else 0


if __name__ == '__main__':
    sys.exit(main())
