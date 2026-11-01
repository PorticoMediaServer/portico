#!/usr/bin/env python3
"""Offline refusal controls plus a real native delete-the-prefix fixture."""
import ast
import json
import os
from pathlib import Path
import platform
import runpy
import shutil
import subprocess
import tempfile
import unittest

M = runpy.run_path(str(Path(__file__).with_name('macos-engine-runtime.py')))


class RuntimeControls(unittest.TestCase):
    def test_disc_identity_seal_follows_all_binary_mutations_and_precedes_manifest(self):
        tree = ast.parse(Path(__file__).with_name('macos-engine-runtime.py').read_text())
        pack = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == 'pack')
        calls = [node for node in ast.walk(pack) if isinstance(node, ast.Call)]
        seal = [node for node in calls if isinstance(node.func, ast.Name) and node.func.id == 'seal_disc_identity']
        self.assertEqual(len(seal), 1)
        # All commands/copies/thinning in this producer must finish before the
        # identity seal, including future strip, install-name or signing steps.
        mutations = [node for node in calls if
            (isinstance(node.func, ast.Name) and node.func.id in ('run', 'copy_runtime')) or
            (isinstance(node.func, ast.Attribute) and node.func.attr in
             ('run', 'copyfile', 'copy2', 'copy', 'move', 'replace', 'write_bytes'))]
        self.assertTrue(mutations)
        self.assertTrue(all(node.end_lineno < seal[0].lineno for node in mutations))
        normalizer = ast.parse(Path(__file__).with_name('normalize-ffmpeg-bundle.py').read_text())
        packing = next(node for node in ast.walk(normalizer) if isinstance(node, ast.Call) and
            isinstance(node.func, ast.Subscript) and isinstance(node.func.slice, ast.Constant) and node.func.slice.value == 'pack')
        inventory = next(node for node in normalizer.body if isinstance(node, ast.Assign) and
            any(isinstance(t, ast.Name) and t.id == 'files' for t in node.targets))
        self.assertLess(packing.end_lineno, inventory.lineno)
        later_calls = [call for statement in normalizer.body if statement.lineno > packing.end_lineno
                       for call in ast.walk(statement) if isinstance(call, ast.Call)]
        self.assertFalse(any(isinstance(call.func, ast.Attribute) and
            call.func.attr in ('run', 'copyfile', 'copy2', 'copy', 'write_bytes') for call in later_calls))

    def test_disc_identity_preserves_provenance_and_refuses_stale_original(self):
        with tempfile.TemporaryDirectory() as temporary:
            bundle = Path(temporary); (bundle / 'bin').mkdir()
            binary = bundle / 'bin/portico-disc'; binary.write_bytes(b'linked')
            original = M['digest'](binary)
            identity = {'schema': 1, 'tool': 'portico-disc', 'target': 'macos-arm64',
                        'decryption': False, 'binarySha256': original,
                        'sourceCommit': 'a' * 40, 'sourceSha256': 'b' * 64,
                        'sourceFiles': {'portico-disc.c': 'b' * 64}, 'staticLibraries': {'kept': 'unchanged'}}
            path = bundle / 'portico-disc-build.json'; path.write_text(json.dumps(identity))
            binary.write_bytes(b'relocated')
            M['seal_disc_identity'](bundle, 'macos-arm64', original)
            final = json.loads(path.read_text())
            self.assertEqual(final, dict(identity, binarySha256=M['digest'](binary)))
            with self.assertRaisesRegex(ValueError, 'original linked binary'):
                M['seal_disc_identity'](bundle, 'macos-arm64', original)
            path.write_text(json.dumps(dict(identity, target='macos-x64')))
            with self.assertRaisesRegex(ValueError, 'original linked binary'):
                M['seal_disc_identity'](bundle, 'macos-arm64', original)

    def test_metadata_preserves_paths_with_spaces_and_refuses_embedded_environment(self):
        text = '''file:
Load command 0
          cmd LC_LOAD_DYLIB
      cmdsize 88
         name /source prefix/lib/libvulkan.1.dylib (offset 24)
Load command 1
          cmd LC_RPATH
      cmdsize 48
         path @loader_path/../lib (offset 12)
'''
        self.assertEqual(M['parse_commands'](text)['loads'], ['/source prefix/lib/libvulkan.1.dylib'])
        self.assertEqual(M['parse_commands'](text)['rpaths'], ['@loader_path/../lib'])
        with self.assertRaises(ValueError):
            M['parse_commands'](text + 'Load command 2\n cmd LC_DYLD_ENVIRONMENT\n')
        with self.assertRaises(ValueError):
            M['parse_commands'](text + 'Load command 2\n cmd LC_LOAD_DYLINKER\n name /tmp/dyld (offset 12)\n')

    def test_system_prefix_traversal_and_bundle_escape_refuse(self):
        self.assertTrue(M['system']('/usr/lib/libSystem.B.dylib'))
        self.assertFalse(M['system']('/usr/lib/../../tmp/libInjected.dylib'))
        self.assertFalse(M['system']('/usr/local/lib/libvulkan.dylib'))
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); bundle = root / 'bundle'; (bundle / 'bin').mkdir(parents=True)
            (bundle / 'lib').mkdir(); library = bundle / 'lib/libvulkan.1.dylib'; library.write_bytes(b'owned')
            self.assertEqual(M['owned_path']('@loader_path/../lib/libvulkan.1.dylib', bundle / 'bin/ffmpeg', bundle), library.resolve())
            with self.assertRaises(ValueError):
                M['owned_path']('@rpath/libvulkan.1.dylib', bundle / 'bin/ffmpeg', bundle)
            outside = root / 'libvulkan.1.dylib'; outside.write_bytes(b'outside')
            with self.assertRaises(ValueError):
                M['owned_path']('@loader_path/../../libvulkan.1.dylib', bundle / 'bin/ffmpeg', bundle)


@unittest.skipUnless(platform.system() == 'Darwin', 'Actual native Mach-O tools required')
class NativeRuntime(unittest.TestCase):
    def fixture(self, root):
        prefix = root / 'original source prefix'; (prefix / 'lib').mkdir(parents=True)
        (prefix / 'bin').mkdir(); (prefix / M['ICD']).parent.mkdir(parents=True)
        (prefix / M['ICD']).write_text(json.dumps({'file_format_version': '1.0.0',
            'ICD': {'library_path': './libMoltenVK.dylib', 'api_version': '1.3.0'}}))
        target = 'macos-arm64' if platform.machine() == 'arm64' else 'macos-x64'
        driver = root / 'driver.c'; driver.write_text('int fixture_driver(void){return 7;}\n')
        loader = root / 'loader.c'; loader.write_text('extern int fixture_driver(void); int fixture_loader(void){return fixture_driver();}\n')
        executable = root / 'main.c'
        executable.write_text('#include <dlfcn.h>\n#include <stdio.h>\nextern int fixture_loader(void);\n'
            'int main(void){void *p=dlopen("libvulkan.1.dylib",RTLD_NOW|RTLD_LOCAL);if(!p){fprintf(stderr,"%s\\n",dlerror());return 2;}'
            'printf("%d\\n",fixture_loader());dlclose(p);return fixture_loader()!=7;}\n')
        def cc(*args):
            subprocess.run(['/usr/bin/cc', '-Wl,-headerpad_max_install_names', *map(str, args)], check=True)
        cc('-dynamiclib', driver, '-Wl,-install_name,' + str(prefix / 'lib/libMoltenVK.dylib'), '-o', prefix / 'lib/libMoltenVK.dylib')
        cc('-dynamiclib', loader, prefix / 'lib/libMoltenVK.dylib',
           '-Wl,-install_name,' + str(prefix / 'lib/libvulkan.1.dylib'), '-o', prefix / 'lib/libvulkan.1.dylib')
        (prefix / 'lib/libvulkan.dylib').symlink_to('libvulkan.1.dylib')
        cc(executable, prefix / 'lib/libvulkan.1.dylib', '-Wl,-rpath,' + str(prefix / 'lib'), '-o', prefix / 'bin/ffmpeg')
        bundle = root / 'bundle'; (bundle / 'bin').mkdir(parents=True)
        for name in M['TOOLS']:
            shutil.copy2(prefix / 'bin/ffmpeg', bundle / 'bin' / name)
        identity = {'schema': 1, 'tool': 'portico-disc', 'target': target, 'decryption': False,
                    'binarySha256': M['digest'](bundle / 'bin/portico-disc'),
                    'sourceCommit': 'a' * 40, 'sourceSha256': 'b' * 64,
                    'sourceFiles': {'portico-disc.c': 'b' * 64}, 'staticLibraries': {}}
        (bundle / 'portico-disc-build.json').write_text(json.dumps(identity))
        return prefix, bundle, target

    def test_real_library_chain_executes_after_original_prefix_is_deleted(self):
        with tempfile.TemporaryDirectory(dir=os.environ.get('RUNNER_TEMP')) as temporary:
            root = Path(temporary); prefix, bundle, target = self.fixture(root)
            record = M['pack'](prefix, bundle, target)
            identity = json.loads((bundle / 'portico-disc-build.json').read_text())
            self.assertEqual(identity['binarySha256'], M['digest'](bundle / 'bin/portico-disc'))
            self.assertEqual(identity['binarySha256'], record['rewritten']['executablesAndLibraries']['bin/portico-disc']['sha256'])
            self.assertNotEqual(identity['binarySha256'], record['originalLoadCommands']['bin/portico-disc']['sha256'])
            shutil.rmtree(prefix)
            elsewhere = root / 'unrelated working directory'; elsewhere.mkdir()
            env = {k: v for k, v in os.environ.items() if not k.startswith(('DYLD_', 'VK_', 'VULKAN_'))}
            for name in M['TOOLS']:
                result = subprocess.run([bundle / 'bin' / name], cwd=elsewhere, env=env,
                                        capture_output=True, text=True, check=True, timeout=10)
                self.assertEqual(result.stdout.strip(), '7')
            M['verify'](bundle, target)
            report = dict(record, originalPrefixDeleted=True, cleanLoaderEnvironment=True,
                          realNativeLibraryChainExecuted=True, discIdentityMatchesFinalBinary=True,
                          discBinaryChangedDuringRelocation=True, engineQualified=False, release=False)
            destination = os.environ.get('PORTICO_RUNTIME_FIXTURE_REPORT')
            if destination:
                Path(destination).write_text(json.dumps(report, indent=2) + '\n')

    def test_real_unselected_dependency_refuses(self):
        with tempfile.TemporaryDirectory(dir=os.environ.get('RUNNER_TEMP')) as temporary:
            root = Path(temporary); prefix, bundle, target = self.fixture(root)
            subprocess.run(['/usr/bin/install_name_tool', '-change', str(prefix / 'lib/libvulkan.1.dylib'),
                            '/outside/libUnexpected.dylib', bundle / 'bin/ffmpeg'], check=True)
            with self.assertRaisesRegex(ValueError, 'Unselected non-system'):
                M['pack'](prefix, bundle, target)

    def test_actual_opposite_architecture_refuses(self):
        with tempfile.TemporaryDirectory(dir=os.environ.get('RUNNER_TEMP')) as temporary:
            root = Path(temporary); prefix, bundle, target = self.fixture(root)
            other = 'x86_64' if platform.machine() == 'arm64' else 'arm64'
            source = root / 'opposite.c'; source.write_text('int wrong_architecture(void){return 0;}\n')
            subprocess.run(['/usr/bin/cc', '-arch', other, '-dynamiclib', source,
                            '-o', prefix / 'lib/libMoltenVK.dylib'], check=True)
            with self.assertRaisesRegex(ValueError, 'Wrong or mixed Mach-O'):
                M['pack'](prefix, bundle, target)

    def test_actual_universal_moltenvk_selects_only_native_slice_and_executes_after_prefix_deletion(self):
        with tempfile.TemporaryDirectory(dir=os.environ.get('RUNNER_TEMP')) as temporary:
            root = Path(temporary); prefix, bundle, target = self.fixture(root)
            driver = prefix / 'lib/libMoltenVK.dylib'
            native_sha = M['digest'](driver)
            other = 'x86_64' if platform.machine() == 'arm64' else 'arm64'
            source = root / 'other.c'; source.write_text('int fixture_driver(void){return 99;}\n')
            opposite = root / 'other.dylib'
            subprocess.run(['/usr/bin/cc', '-arch', other, '-dynamiclib', source,
                '-Wl,-headerpad_max_install_names', '-Wl,-install_name,' + str(driver), '-o', opposite], check=True)
            universal = root / 'universal.dylib'
            subprocess.run(['/usr/bin/lipo', '-create', driver, opposite, '-output', universal], check=True)
            shutil.copyfile(universal, driver)
            universal_sha = M['digest'](driver)
            self.assertNotEqual(native_sha, universal_sha)
            record = M['pack'](prefix, bundle, target)
            selected = record['runtimeInputs']['libMoltenVK.dylib']
            self.assertEqual(set(selected['architectures']), {'arm64', 'x86_64'})
            self.assertEqual(selected['selectedArchitecture'], platform.machine())
            self.assertEqual(selected['sha256'], universal_sha)
            self.assertEqual(selected['selectedSliceSHA256'], native_sha)
            shutil.rmtree(prefix)
            elsewhere = root / 'unrelated'; elsewhere.mkdir()
            env = {k: v for k, v in os.environ.items() if not k.startswith(('DYLD_', 'VK_', 'VULKAN_'))}
            for name in M['TOOLS']:
                result = subprocess.run([bundle / 'bin' / name], cwd=elsewhere, env=env,
                                        capture_output=True, text=True, check=True, timeout=10)
                self.assertEqual(result.stdout.strip(), '7')
            M['verify'](bundle, target)
            destination = os.environ.get('PORTICO_RUNTIME_FIXTURE_REPORT')
            if destination:
                Path(destination).with_suffix('.universal.json').write_text(json.dumps(dict(record,
                    originalPrefixDeleted=True, realNativeLibraryChainExecuted=True,
                    selectedNativeSliceOnly=True, engineQualified=False, release=False), indent=2) + '\n')

    def test_a_mixed_architecture_vulkan_loader_still_refuses(self):
        with tempfile.TemporaryDirectory(dir=os.environ.get('RUNNER_TEMP')) as temporary:
            root = Path(temporary); prefix, bundle, target = self.fixture(root)
            loader = prefix / 'lib/libvulkan.1.dylib'
            other = 'x86_64' if platform.machine() == 'arm64' else 'arm64'
            source = root / 'other.c'; source.write_text('int fixture_loader(void){return 99;}\n')
            opposite = root / 'other.dylib'
            subprocess.run(['/usr/bin/cc', '-arch', other, '-dynamiclib', source, '-o', opposite], check=True)
            universal = root / 'universal.dylib'
            subprocess.run(['/usr/bin/lipo', '-create', loader, opposite, '-output', universal], check=True)
            shutil.copyfile(universal, loader)
            with self.assertRaisesRegex(ValueError, 'Wrong or mixed Mach-O'):
                M['pack'](prefix, bundle, target)


if __name__ == '__main__':
    unittest.main()
