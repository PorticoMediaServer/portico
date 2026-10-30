import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, mkdtempSync, rmSync, mkdirSync, writeFileSync} from 'node:fs';
import os from 'node:os';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import vm from 'node:vm';
import {execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {splitCopyright} from '../../../packages/client-core/src/licences/inventory.ts';

const require = createRequire(import.meta.url);
const root = fileURLToPath(new URL('..', import.meta.url));
const configSource = readFileSync(path.join(root, 'react-native.config.js'), 'utf8');
const manifest = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8'));
const codegen = require('react-native/scripts/codegen/generate-artifacts-executor/utils.js');

function config(target, packageManifest = manifest, environment = {}) {
  const module = {exports: {}};
  vm.runInNewContext(configSource, {
    module, __dirname: root, process: {env: {...environment, PORTICO_APPLE_TARGET: target}},
    require: id => id === './package.json' ? packageManifest : require(id),
  });
  return JSON.parse(JSON.stringify(module.exports));
}

test('ordinary iOS and Android keep default autolinking, including declared WebView', () => {
  const installed = {...manifest, dependencies: {...manifest.dependencies, 'react-native-webview': '16.0.0'}};
  for (const target of [undefined, '', 'ios', 'android']) assert.deepEqual(config(target, installed), {});
});

test('Xcode Apple TV codegen excludes WebView without the Podfile-only environment flag', () => {
  for (const platform of ['appletvos', 'appletvsimulator']) {
    assert.deepEqual(config(undefined, manifest, {PLATFORM_NAME: platform}), config('tvos'));
  }
  for (const platform of ['iphoneos', 'iphonesimulator', 'macosx', undefined]) {
    assert.deepEqual(config(undefined, manifest, {PLATFORM_NAME: platform}), {});
  }
});

test('real Fabric component generation omits unlinked tvOS WebView in Xcode and Pod paths, keeping iOS', t => {
  const temporary = mkdtempSync(path.join(os.tmpdir(), 'apple-fabric-provider-'));
  t.after(() => rmSync(temporary, {recursive: true, force: true}));
  for (const [platform, generatedAutolink] of [['appletvsimulator', false], ['appletvos', true], ['iphonesimulator', false]]) {
    const output = path.join(temporary, platform);
    const env = {...process.env, PLATFORM_NAME: platform};
    delete env.PORTICO_APPLE_TARGET;
    if (generatedAutolink) {
      const cli = require.resolve('@react-native-community/cli/build/bin.js');
      const actual = execFileSync(process.execPath, [cli, 'config'], {cwd: root, env, encoding: 'utf8'});
      const target = path.join(output, 'build/generated/autolinking');
      mkdirSync(target, {recursive: true}); writeFileSync(path.join(target, 'autolinking.json'), actual);
    }
    const entry = require.resolve('react-native/scripts/generate-codegen-artifacts.js');
    execFileSync(process.execPath, [entry, '--path', root, '--outputPath', output, '--targetPlatform', 'ios'], {
      cwd: root, env: {...env, RCT_SCRIPT_OUTPUT_DIR: output}, encoding: 'utf8',
    });
    const provider = readFileSync(path.join(output, 'build/generated/ios/ReactCodegen/RCTThirdPartyComponentsProvider.mm'), 'utf8');
    assert.match(provider, /NSClassFromString/);
    assert.match(provider, /RNSVG/); // Other real native components remain installed.
    if (platform.startsWith('appletv')) assert.doesNotMatch(provider, /RNCWebView/);
    else assert.match(provider, /@"RNCWebView": NSClassFromString\(@"RNCWebView"\)/);
    // The touch keyboard controller's views are iPhone/iPad only too.
    if (platform.startsWith('appletv')) assert.doesNotMatch(provider, /KeyboardControllerView/);
    else assert.match(provider, /@"KeyboardControllerView": NSClassFromString/);
  }
});

test('tvOS writes autolinking into its own project and disables only the declared touch-only Apple codegen', () => {
  const installed = {...manifest, dependencies: {...manifest.dependencies, 'react-native-webview': '16.0.0'}};
  const tv = config('tvos', installed);
  assert.deepEqual(tv, {
    project: {ios: {sourceDir: 'tvos'}},
    dependencies: {'react-native-webview': {platforms: {ios: null}}, 'react-native-keyboard-controller': {platforms: {ios: null}}},
  });
  assert.deepEqual(codegen.findDisabledLibrariesByPlatform(tv, 'ios'), ['react-native-webview', 'react-native-keyboard-controller']);
  assert.deepEqual(codegen.findDisabledLibrariesByPlatform(tv, 'android'), []);
});

test('tvOS does not ask the CLI to resolve a package absent from the application', () => {
  assert.deepEqual(config('tvos', {}), {project: {ios: {sourceDir: 'tvos'}}});
  for (const section of ['dependencies', 'devDependencies', 'optionalDependencies']) {
    assert.deepEqual(config('tvos', {[section]: {'react-native-webview': '16.0.0'}}).dependencies,
      {'react-native-webview': {platforms: {ios: null}}});
  }
});

test('tvOS target flag precedes CLI autolinking while both Apple deployment floors remain 15.1', () => {
  const podfile = readFileSync(path.join(root, 'tvos/Podfile'), 'utf8');
  assert.ok(podfile.indexOf("ENV['PORTICO_APPLE_TARGET'] = 'tvos'") < podfile.indexOf('use_native_modules!'));
  assert.ok(!readFileSync(path.join(root, 'ios/Podfile'), 'utf8').includes('PORTICO_APPLE_TARGET'));
  for (const [platform, setting] of [['ios', 'IPHONEOS'], ['tvos', 'TVOS']]) {
    const project = readFileSync(path.join(root, platform, 'Portico.xcodeproj/project.pbxproj'), 'utf8');
    const floors = [...project.matchAll(new RegExp(`${setting}_DEPLOYMENT_TARGET = ([^;]+);`, 'g'))];
    assert.ok(floors.length > 0);
    assert.ok(floors.every(match => match[1] === '15.1'));
  }
});

test('the refreshed tvOS lock belongs to the actual target-selecting Podfile', () => {
  const checksum = createHash('sha1').update(readFileSync(path.join(root, 'tvos/Podfile'))).digest('hex');
  const lock = readFileSync(path.join(root, 'tvos/Podfile.lock'), 'utf8');
  assert.equal(/^PODFILE CHECKSUM: (\S+)$/m.exec(lock)?.[1], checksum);
});

test('WebView native code stays iOS-only while its bundled JS has one exact npm notice', () => {
  const ios = readFileSync(path.join(root, 'ios/Podfile.lock'), 'utf8');
  const tvos = readFileSync(path.join(root, 'tvos/Podfile.lock'), 'utf8');
  assert.match(ios, /^  - react-native-webview \(16\.0\.0\):$/m);
  assert.doesNotMatch(tvos, /react-native-webview/);
  const inventory = JSON.parse(readFileSync(path.join(root, 'licences/generated/apple.json'), 'utf8'));
  const entries = inventory.components.filter(component => component.name === 'react-native-webview');
  assert.equal(entries.length, 1);
  const entry = entries[0];
  assert.equal(manifest.dependencies['react-native-webview'], '16.0.0');
  assert.equal(entry.id, 'npm:react-native-webview@16.0.0');
  assert.equal(entry.version, '16.0.0'); assert.equal(entry.group, 'packages');
  // The recorded Metro graphs now contain the JS package. Native Pod scope
  // above still excludes tvOS; its notice must not omit JS shipped in the bundle.
  for (const client of ['ios', 'android']) {
    const graph = readFileSync(path.join(root, `licences/js-${client}.txt`), 'utf8');
    assert.match(graph, /^react-native-webview@16\.0\.0\t/m);
  }
  assert.doesNotMatch(readFileSync(path.join(root, 'licences/js-vega.txt'), 'utf8'), /^react-native-webview@/m);
  assert.equal(entry.only, undefined); assert.equal(entry.licence, 'MIT'); assert.equal(entry.textPending, undefined);
  const registrations = JSON.parse(readFileSync(path.join(root, 'licences/native-npm.json'), 'utf8'));
  const registration = registrations.find(component => component.name === 'react-native-webview');
  assert.equal(registration.version, '16.0.0'); assert.equal(registration.licence, 'MIT');
  assert.deepEqual(registration.platforms, ['android', 'apple']);
  const upstream = readFileSync(path.join(root, 'node_modules/react-native-webview', registration.licenceFile), 'utf8');
  assert.equal(createHash('sha256').update(upstream).digest('hex'), registration.licenceSHA256);
  const licence = splitCopyright(upstream);
  assert.equal(inventory.texts[entry.text], licence.body);
  assert.deepEqual(entry.copyright ?? [], licence.copyright);
});

test('actual installed CLI selects the tvOS project without changing Android or other dependency declarations', () => {
  const cli = require.resolve('@react-native-community/cli/build/bin.js');
  const read = target => JSON.parse(execFileSync(process.execPath, [cli, 'config'], {
    cwd: root, encoding: 'utf8', env: {...process.env, PORTICO_APPLE_TARGET: target},
  }));
  const ordinary = read('ios');
  const tv = read('tvos');
  assert.equal(ordinary.project.ios.sourceDir, path.join(root, 'ios'));
  assert.equal(tv.project.ios.sourceDir, path.join(root, 'tvos'));
  assert.deepEqual(tv.project.android, ordinary.project.android);
  const ordinaryDependencies = {...ordinary.dependencies};
  const tvDependencies = {...tv.dependencies};
  for (const touchOnly of ['react-native-webview', 'react-native-keyboard-controller']) {
    assert.equal(tvDependencies[touchOnly].platforms.ios, null);
    assert.deepEqual(tvDependencies[touchOnly].platforms.android, ordinaryDependencies[touchOnly].platforms.android);
    delete ordinaryDependencies[touchOnly]; delete tvDependencies[touchOnly];
  }
  assert.deepEqual(tvDependencies, ordinaryDependencies);
});
