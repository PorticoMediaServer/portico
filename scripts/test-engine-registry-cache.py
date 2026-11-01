#!/usr/bin/env python3
"""Registry hint unit coverage and an explicit hosted-only tiny BuildKit control.

Set PORTICO_REGISTRY_CACHE_CONTROL=1 to run the control. It uses five fresh
builders and a disposable local registry by default. Optional
PORTICO_REGISTRY_CACHE_CONTROL_ROOT=ghcr.io/<owner>/<control-package> uses
existing workflow authentication and unique tags instead. This builds only a
small sentinel graph, never FFmpeg or an engine/compiler toolchain.
"""
import importlib.util
import json
import os
import re
import subprocess
import uuid
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent


def module(name):
    spec = importlib.util.spec_from_file_location(name.replace('-', '_'), ROOT / (name + '.py'))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


cache = module('engine-registry-cache')
adapter = module('engine-recipe-adapter')
HOSTED = dict(GITHUB_ACTIONS='true', RUNNER_ENVIRONMENT='github-hosted',
              PORTICO_ENGINE_BUILD_CONTEXT='development',
              GITHUB_REPOSITORY='PorticoMediaServer/portico', GITHUB_EVENT_NAME='workflow_dispatch',
              GITHUB_REF='refs/heads/cache-tests',
              GITHUB_WORKFLOW_REF='PorticoMediaServer/portico/.github/workflows/ffmpeg-toolchain.yml@refs/heads/cache-tests',
              PORTICO_ENGINE_REGISTRY_CACHE='1')
IDENTITY = 'sha256:' + '1' * 64


class PolicyTests(unittest.TestCase):
    def test_qualified_release_and_implicit_context_cannot_read_even_an_existing_registry_cache(self):
        for value in ('qualified', 'release', '', 'publisher'):
            with self.subTest(context=value), self.assertRaisesRegex(ValueError, 'development-only'):
                cache.RegistryCache('windows-x64', dict(HOSTED, PORTICO_ENGINE_BUILD_CONTEXT=value))

    def test_disabled_has_no_registry_arguments(self):
        self.assertEqual(cache.RegistryCache('windows-x64', {}).arguments('base', 'a' * 64), [])

    def test_read_only_and_explicit_mode_max_writes(self):
        policy = cache.RegistryCache('linux-arm64', HOSTED)
        args = policy.arguments('deps', 'a' * 64)
        self.assertEqual(args.count('--cache-from'), 2)
        self.assertNotIn('--cache-to', args)
        self.assertIn('ffmpeg-cache-linux-arm64:deps-' + 'a' * 64, args[1])
        writer = cache.RegistryCache('linux-arm64', dict(HOSTED, PORTICO_ENGINE_CACHE_WRITE='1'))
        args = writer.arguments('deps', 'a' * 64)
        self.assertEqual(args.count('--cache-to'), 2)
        self.assertTrue(all(args[i + 1].endswith(',mode=max') for i, arg in enumerate(args) if arg == '--cache-to'))
        self.assertEqual(writer.receipts['deps']['key'], 'a' * 64)

    def test_refuse_untrusted_activation_and_invalid_refs(self):
        for env in ({'PORTICO_ENGINE_CACHE_WRITE': '1'},
                    {'PORTICO_ENGINE_REGISTRY_CACHE': '1'},
                    dict(HOSTED, RUNNER_ENVIRONMENT='self-hosted'),
                    dict(HOSTED, GITHUB_EVENT_NAME='pull_request'),
                    dict(HOSTED, GITHUB_REPOSITORY='attacker/portico')):
            with self.assertRaises(ValueError):
                cache.RegistryCache('windows-x64', env)
        policy = cache.RegistryCache('windows-x64', HOSTED)
        for stage, key in [('x,mode=max', 'a' * 64), ('base', '../bad'), ('portico-ffmpeg', 'a' * 64), ('portico-engine-output', 'a' * 64)]:
            with self.assertRaises(ValueError):
                policy.arguments(stage, key)


class InputTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.recipe = Path(self.temp.name) / 'recipe'
        self.recipe.mkdir()
        for directory in ('scripts.d', 'util', '.cache/downloads', 'images/base', 'images/base-linuxarm64'):
            (self.recipe / directory).mkdir(parents=True)
        self.write('scripts.d/50-one.sh', 'build one\n')
        self.write('scripts.d/50-two.sh', 'build two\n')
        self.write('.cache/downloads/one.tar.xz', 'source one')
        self.write('util/run_stage.sh', 'run stage\n')
        self.write('images/base/Dockerfile', 'FROM ubuntu:26.04\n')
        self.write('images/base-linuxarm64/Dockerfile', 'FROM base\nRUN compiler\n')
        self.graph = '''FROM local/portico-engine/engine/base-win64:latest AS base-layer
ENV TARGET=win64 CFLAGS=-O2
COPY --link util/run_stage.sh /usr/bin/run_stage
FROM base-layer AS one
RUN --mount=src=scripts.d/50-one.sh,dst=/stage --mount=src=.cache/downloads/one.tar.xz,dst=/source build
FROM base-layer AS two
RUN --mount=src=scripts.d/50-two.sh,dst=/stage build
FROM one AS portico-ffmpeg
RUN configure-final
'''
        self.write('Dockerfile', self.graph)

    def write(self, name, value):
        (self.recipe / name).write_text(value)

    def keys(self):
        results = {}
        for stage, deps in [('base-layer', []), ('one', ['base-layer']), ('two', ['base-layer'])]:
            results[stage] = {'key': cache.stage_key(self.recipe, stage, deps, IDENTITY, results)}
        return {name: row['key'] for name, row in results.items()}

    def test_dependency_sources_and_only_their_descendants_invalidate(self):
        before = self.keys()
        self.write('.cache/downloads/one.tar.xz', 'changed exact source')
        after = self.keys()
        self.assertNotEqual(before['one'], after['one'])
        self.assertEqual(before['two'], after['two'])
        self.write('util/run_stage.sh', 'changed runner')
        last = self.keys()
        self.assertNotEqual(after['one'], last['one'])
        self.assertNotEqual(after['two'], last['two'])

    def test_final_configure_and_packaging_do_not_invalidate_dependencies(self):
        before = self.keys()
        self.write('build.sh', 'new final flags and packaging')
        self.write('Dockerfile', self.graph.replace('RUN configure-final', 'RUN different-final'))
        self.assertEqual(before, self.keys())

    def test_compiler_flags_and_script_content_invalidate(self):
        before = self.keys()
        self.write('Dockerfile', self.graph.replace('CFLAGS=-O2', 'CFLAGS=-O3'))
        self.assertNotEqual(before['one'], self.keys()['one'])
        self.assertNotEqual(cache.stage_key(self.recipe, 'base-layer', [], IDENTITY, {}),
                            cache.stage_key(self.recipe, 'base-layer', [], 'sha256:' + '2' * 64, {}))
        before = self.keys()
        self.write('scripts.d/50-one.sh', 'different build command')
        self.assertNotEqual(before['one'], self.keys()['one'])
        self.assertEqual(before['two'], self.keys()['two'])

    def test_stable_task_namespace_and_path(self):
        canonical = 'local/portico-engine/engine'
        expected = None
        for owner in ('owner-a', 'owner-b'):
            text = self.graph.replace(canonical, 'local/' + owner + '/engine')
            text = text.replace('ENV TARGET=', 'ENV REPO=' + owner + '/engine TARGET=')
            self.write('Dockerfile', text)
            adapter.stable_graph(self.recipe, 'local/' + owner + '/engine', canonical)
            if expected is None:
                expected = self.keys()
            self.assertEqual(expected, self.keys())
        with tempfile.TemporaryDirectory() as elsewhere:
            import shutil
            copy = Path(elsewhere) / 'different-absolute-path'
            shutil.copytree(self.recipe, copy)
            self.assertEqual(cache.context_key(self.recipe, 'images/base'), cache.context_key(copy, 'images/base'))

    def test_refuse_symlink_escape_missing_inputs_and_mutable_identity(self):
        with self.assertRaises(ValueError):
            cache.files(self.recipe, '../escape')
        with self.assertRaises(ValueError):
            cache.files(self.recipe, 'missing')
        (self.recipe / 'linked').symlink_to(self.recipe / 'util/run_stage.sh')
        with self.assertRaises(ValueError):
            cache.files(self.recipe, 'linked')
        with self.assertRaises(ValueError):
            cache.stage_key(self.recipe, 'one', [], 'compiler:latest', {})

    def test_compiler_context_inputs_and_parent_identity_invalidate(self):
        first = cache.context_key(self.recipe, 'images/base-linuxarm64', {'baseImageId': IDENTITY})
        self.write('images/base-linuxarm64/Dockerfile', 'FROM base\nRUN changed compiler\n')
        second = cache.context_key(self.recipe, 'images/base-linuxarm64', {'baseImageId': IDENTITY})
        self.assertNotEqual(first, second)
        self.assertNotEqual(second, cache.context_key(self.recipe, 'images/base-linuxarm64', {'baseImageId': 'sha256:' + '2' * 64}))

    def test_arm_arguments_inspect_actual_compiler_and_record_exact_refs(self):
        self.write('Dockerfile', self.graph.split('FROM one AS portico-ffmpeg')[0])
        with patch.dict(os.environ, HOSTED, clear=True), patch.object(cache.subprocess, 'check_output', return_value=IDENTITY + '\n') as inspect:
            args = cache.makeimage_args(self.recipe, 'linux-arm64', 'deps', 'actual/compiler:latest')
            inspect.assert_called_once_with(['docker', 'image', 'inspect', '-f', '{{.Id}}', 'actual/compiler:latest'], text=True)
            receipt = json.loads((self.recipe.parent / 'portico-registry-cache-deps.json').read_text())
            self.assertIn(receipt['stages']['deps']['ref'], args[1])
            with patch.object(cache.subprocess, 'check_output', return_value='mutable:tag'):
                with self.assertRaises(ValueError):
                    cache.makeimage_args(self.recipe, 'linux-arm64', 'deps', 'actual/compiler:latest')
        self.write('Dockerfile', self.graph)
        with patch.dict(os.environ, HOSTED, clear=True), patch.object(cache.subprocess, 'check_output', return_value=IDENTITY):
            with self.assertRaises(ValueError):
                cache.makeimage_args(self.recipe, 'linux-arm64', 'deps', 'actual/compiler:latest')

    def test_arm_final_flags_do_not_invalidate_and_graph_hint_survives_dependency_edit(self):
        self.write('Dockerfile', self.graph.split('FROM one AS portico-ffmpeg')[0])
        with patch.dict(os.environ, HOSTED, clear=True), patch.object(cache.subprocess, 'check_output', return_value=IDENTITY):
            before = cache.makeimage_args(self.recipe, 'linux-arm64', 'deps', 'compiler:latest')
            self.write('build.sh', 'different final FFmpeg configure and packaging')
            self.assertEqual(before, cache.makeimage_args(self.recipe, 'linux-arm64', 'deps', 'compiler:latest'))
            self.write('Dockerfile', (self.recipe / 'Dockerfile').read_text() + '\nENV FF_CONFIGURE=changed-final-flags\n')
            final_flags = cache.makeimage_args(self.recipe, 'linux-arm64', 'deps', 'compiler:latest')
            self.assertNotEqual(before[1], final_flags[1])
            self.assertEqual(before[3], final_flags[3])
            self.write('scripts.d/50-one.sh', 'dependency source recipe changed')
            after = cache.makeimage_args(self.recipe, 'linux-arm64', 'deps', 'compiler:latest')
            self.assertNotEqual(before[1], after[1])
            self.assertEqual(before[3], after[3])  # rolling graph ref
            self.assertFalse(list(self.recipe.glob('portico-registry-cache-*.json')))

    def test_registry_hint_cannot_replace_missing_checked_sources(self):
        hint = cache.RegistryCache('windows-x64', HOSTED).arguments('one', self.keys()['one'])
        self.assertIn('--cache-from', hint)
        (self.recipe / '.cache/downloads/one.tar.xz').unlink()
        with self.assertRaises(ValueError):
            self.keys()

    def test_pinned_makeimage_hooks_and_failure_propagation(self):
        commands = []
        for variable, indent in [('BASE_IMAGE', 8), ('TARGET_IMAGE', 8), ('IMAGE', 0)]:
            pad = ' ' * (indent + 4)
            commands.append(' ' * indent + 'docker buildx --builder ffbuilder build \\\n' + pad +
                            '--cache-from=type=local,src=.cache/"${' + variable + '/:/_}" \\\n' + pad +
                            '--cache-to=type=local,mode=max,dest=.cache/"${' + variable + '/:/_}" \\\n' + pad + '--load .\n')
        self.write('makeimage.sh', ''.join(commands))
        cache.configure_makeimage(self.recipe, ROOT / 'engine-registry-cache.py', 'linux-arm64')
        text = (self.recipe / 'makeimage.sh').read_text()
        self.assertNotIn('type=local', text)
        self.assertEqual(text.count('--build-arg SOURCE_DATE_EPOCH='), 3)
        self.assertEqual(text.count('"${PORTICO_CACHE_ARGS[@]}"'), 3)
        self.assertIn('--compiler-image "$TARGET_IMAGE"', text)
        self.assertIn('--compiler-image "$BASE_IMAGE"', text)
        self.assertIn('PORTICO_CACHE_LINES="$(python3 ', text)
        self.assertNotIn('mapfile -t PORTICO_CACHE_ARGS < <(', text)
        with self.assertRaises(ValueError):
            cache.configure_makeimage(self.recipe, ROOT / 'engine-registry-cache.py', 'linux-arm64')


@unittest.skipUnless(os.environ.get('PORTICO_REGISTRY_CACHE_CONTROL') == '1',
                     'explicit hosted tiny BuildKit control only')
class HostedRegistryControl(unittest.TestCase):
    def test_independent_builders_cold_hint_invalidation_and_fresh_terminal(self):
        # Evaluate the actual hosted environment, not the unit test fixture.
        policy = cache.RegistryCache('windows-x64')
        self.assertTrue(policy.enabled, 'explicit hosted registry cache activation required')
        self.assertTrue(policy.write, 'control requires explicit cache write authorization')
        nonce = uuid.uuid4().hex[:12]
        runner_temp = Path(os.environ['RUNNER_TEMP']).resolve()
        self.assertFalse(str(runner_temp).startswith(('/tmp/', '/dev/shm/')))
        report_path = Path(os.environ.get('PORTICO_REGISTRY_CACHE_CONTROL_OUTPUT',
                                         str(runner_temp / 'portico-registry-cache-control.json')))
        evidence = report_path.parent / ('registry-control-' + nonce)
        evidence.mkdir(parents=True)
        scratch = tempfile.TemporaryDirectory(prefix='registry-control-', dir=runner_temp)
        self.addCleanup(scratch.cleanup)
        work = Path(scratch.name)
        command_index = 0
        commands = []

        def run(label, command):
            nonlocal command_index
            command_index += 1
            log = evidence / (str(command_index) + '-' + label + '.log')
            with log.open('wb') as stream:
                result = subprocess.run(command, stdout=stream, stderr=subprocess.STDOUT)
            commands.append(dict(label=label, command=command, exitCode=result.returncode, log=str(log)))
            self.assertEqual(result.returncode, 0, f'{label} failed; see {log}')
            return log.read_text(errors='replace')

        def cleanup(command):
            subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)

        registry_root = os.environ.get('PORTICO_REGISTRY_CACHE_CONTROL_ROOT', '')
        config = work / 'buildkit.toml'
        if registry_root:
            self.assertRegex(registry_root, r'^ghcr\.io/[a-z0-9._-]+/[a-z0-9._/-]+$')
            config.write_text('[worker.oci]\n  max-parallelism = 1\n')
            driver_args = []
        else:
            network = 'portico-cache-control-' + nonce
            registry = network + '-registry'
            run('network', ['docker', 'network', 'create', network])
            self.addCleanup(cleanup, ['docker', 'network', 'rm', network])
            run('registry', ['docker', 'run', '--detach', '--name', registry, '--network', network,
                             '--network-alias', 'portico-control-registry', 'registry:2.8.3'])
            self.addCleanup(cleanup, ['docker', 'rm', '--force', '--volumes', registry])
            registry_root = 'portico-control-registry:5000/portico-control'
            config.write_text('[worker.oci]\n  max-parallelism = 1\n'
                              '[registry."portico-control-registry:5000"]\n  http = true\n')
            driver_args = ['--driver-opt', 'network=' + network]
        # This is a tiny control base, not an engine/compiler toolchain.
        base = os.environ.get('PORTICO_REGISTRY_CACHE_CONTROL_BASE', 'busybox:1.37.0')
        run('pull-control-base', ['docker', 'pull', base])
        inspected = json.loads(subprocess.check_output(['docker', 'image', 'inspect', base], text=True))
        base_ref = inspected[0]['RepoDigests'][0]
        self.assertRegex(base_ref, r'@sha256:[a-f0-9]{64}$')
        dockerfile = '''ARG CONTROL_BASE
FROM ${CONTROL_BASE} AS compiler
COPY compiler-input /compiler-input
RUN sha256sum /compiler-input > /compiler-identity
FROM compiler AS sibling
COPY sibling-input /sibling-input
RUN cat /proc/sys/kernel/random/uuid > /sibling-sentinel
FROM compiler AS dependency
COPY source-input /source-input
RUN cat /proc/sys/kernel/random/uuid > /dependency-sentinel
FROM dependency AS prefix
ENV FF_CONFIGURE=control-v1
COPY --from=sibling /sibling-sentinel /sibling-sentinel
FROM prefix AS portico-ffmpeg
COPY configure-input /configure-input
RUN cat /proc/sys/kernel/random/uuid > /terminal-sentinel
FROM scratch AS output
COPY --from=portico-ffmpeg /dependency-sentinel /dependency-sentinel
COPY --from=portico-ffmpeg /sibling-sentinel /sibling-sentinel
COPY --from=portico-ffmpeg /terminal-sentinel /terminal-sentinel
'''
        (work / 'Dockerfile').write_text(dockerfile)
        (evidence / 'Dockerfile').write_text(dockerfile)
        for name in ('compiler-input', 'source-input', 'sibling-input', 'configure-input'):
            (work / name).write_text(name + ':v1\n')
        records = []
        report = dict(qualified=False, fullCompilerAcceptance=False, engineBuild=False,
                      controlOnly=True, baseRef=base_ref, registryRoot=registry_root,
                      commands=commands, rounds=records)
        try:
            for index, change in enumerate(('cold', 'configure', 'repeat', 'source', 'compiler'), 1):
                if change in ('configure', 'source', 'compiler'):
                    name = {'configure': 'configure-input', 'source': 'source-input',
                            'compiler': 'compiler-input'}[change]
                    (work / name).write_text(name + ':v2\n')
                    if change == 'configure':
                        # Like makeimage's final dependency image ENV: the
                        # aggregate key changes but library RUN inputs do not.
                        (work / 'Dockerfile').write_text(dockerfile.replace('FF_CONFIGURE=control-v1',
                                                                              'FF_CONFIGURE=control-v2'))
                key = cache.sha(dict(baseRef=base_ref, dockerfile=cache.files(work, 'Dockerfile'),
                                     compiler=cache.files(work, 'compiler-input'),
                                     source=cache.files(work, 'source-input'),
                                     sibling=cache.files(work, 'sibling-input')))
                # Exercise production command generation with an isolated
                # repository destination; production cache tags are untouched.
                generated = policy.arguments('control-' + nonce + '-deps', key)
                original_root = 'ghcr.io/porticomediaserver/ffmpeg-cache-windows-x64'
                cache_args = [arg.replace(original_root, registry_root) for arg in generated]
                builder = 'portico-registry-control-' + nonce + '-' + str(index)
                run('builder-' + str(index), ['docker', 'buildx', 'create', '--bootstrap', '--name', builder,
                                              '--driver', 'docker-container', '--config', str(config)] + driver_args)
                self.addCleanup(cleanup, ['docker', 'buildx', 'rm', '--force', builder])
                common = ['docker', 'buildx', '--builder', builder, 'build', '--progress=plain',
                          '--build-arg', 'CONTROL_BASE=' + base_ref,
                          '--build-arg', 'SOURCE_DATE_EPOCH=1700000000', '--file', str(work / 'Dockerfile')]
                # Never export the terminal graph into the registry.
                dep_log = run('dependency-' + str(index), common + cache_args + ['--target', 'prefix', str(work)])
                read_args = []
                for offset, arg in enumerate(cache_args):
                    if arg == '--cache-from':
                        read_args += [arg, cache_args[offset + 1]]
                output = work / ('output-' + str(index))
                terminal_log = run('terminal-' + str(index), common + read_args + [
                    '--target', 'output', '--no-cache-filter', 'portico-ffmpeg',
                    '--output', 'type=local,dest=' + str(output), str(work)])
                sentinels = {name: (output / (name + '-sentinel')).read_text().strip()
                             for name in ('dependency', 'sibling', 'terminal')}
                for value in sentinels.values():
                    self.assertRegex(value, r'^[a-f0-9-]{36}$')
                # Repeat unchanged terminal inputs within this same builder,
                # so only the explicit filter forces the second execution.
                second_output = work / ('output-' + str(index) + '-fresh')
                run('terminal-again-' + str(index), common + read_args + [
                    '--target', 'output', '--no-cache-filter', 'portico-ffmpeg',
                    '--output', 'type=local,dest=' + str(second_output), str(work)])
                second = {name: (second_output / (name + '-sentinel')).read_text().strip()
                          for name in ('dependency', 'sibling', 'terminal')}
                self.assertEqual(sentinels['dependency'], second['dependency'])
                self.assertEqual(sentinels['sibling'], second['sibling'])
                self.assertNotEqual(sentinels['terminal'], second['terminal'])
                record = dict(round=index, change=change, key=key, sentinels=sentinels,
                              repeatedTerminalSentinels=second, freshTerminalExecutions=2,
                              refs=[value for value in cache_args if value.startswith('type=registry')],
                              terminalFreshFilter=True, builder=builder)
                if index == 1:
                    record['missingColdCacheObserved'] = bool(re.search(
                        r'not found|404|manifest unknown|failed to configure registry cache importer', dep_log, re.I))
                    self.assertTrue(record['missingColdCacheObserved'], 'cold missing-cache import must be visible')
                else:
                    previous = records[-1]
                    self.assertNotEqual(previous['sentinels']['terminal'], sentinels['terminal'])
                    if change in ('configure', 'repeat'):
                        if change == 'configure':
                            self.assertNotEqual(previous['key'], key)
                        else:
                            self.assertEqual(previous['key'], key)
                        self.assertEqual(previous['sentinels']['dependency'], sentinels['dependency'])
                        self.assertEqual(previous['sentinels']['sibling'], sentinels['sibling'])
                    elif change == 'source':
                        self.assertNotEqual(previous['key'], key)
                        self.assertNotEqual(previous['sentinels']['dependency'], sentinels['dependency'])
                        self.assertEqual(previous['sentinels']['sibling'], sentinels['sibling'])
                    else:
                        self.assertNotEqual(previous['key'], key)
                        self.assertNotEqual(previous['sentinels']['dependency'], sentinels['dependency'])
                        self.assertNotEqual(previous['sentinels']['sibling'], sentinels['sibling'])
                record['terminalLogHasExecution'] = 'terminal-sentinel' in terminal_log
                records.append(record)
                run('retire-builder-' + str(index), ['docker', 'buildx', 'rm', '--force', builder])
            report['passed'] = True
        finally:
            report_path.parent.mkdir(parents=True, exist_ok=True)
            report_path.write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    unittest.main()
