import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import {
  apkFile,
  artifactFileName,
  directoryManifest,
  entryData,
  gradleArguments,
  lockFile,
  lockedCommand,
  parseArguments,
  publishArtifact,
  readZip,
  supervise,
  treeDigest,
  verifyPackage,
} from '../scripts/package-android-emulator.mjs';

const bundle = Buffer.from('var __BUNDLE_START_TIME__=Date.now();', 'utf8');

/** A ZIP with stored entries: enough of the format for the reader, with no writer dependency. */
function storedZip(files) {
  const locals = [], centrals = [];
  let offset = 0;
  for (const {name, data} of files) {
    const nameBytes = Buffer.from(name, 'utf8');
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0, 8);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(nameBytes.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(20, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt32LE(data.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(nameBytes.length, 28);
    central.writeUInt32LE(offset, 42);
    locals.push(local, nameBytes, data);
    centrals.push(central, nameBytes);
    offset += local.length + nameBytes.length + data.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(files.length, 8);
  end.writeUInt16LE(files.length, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}

const readerFile = (name, text) => ({name: `assets/PorticoReader.bundle/${name}`, data: Buffer.from(text)});

test('arguments default to a phone-and-TV debug APK for the x86_64 emulator', () => {
  const options = parseArguments([]);
  assert.equal(options.variant, 'googleDebug');
  assert.equal(options.abi, 'x86_64');
  assert.equal(options.offline, false);
  assert.ok(options.timeoutSeconds > 0);
});

test('arguments take the variant, architecture, output and reader bundle', () => {
  const options = parseArguments(['--variant', 'googleRelease', '--abi', 'arm64-v8a', '--output', '/tmp/out',
    '--reader-bundle', '/tmp/reader', '--offline', '--timeout', '90']);
  assert.equal(options.variant, 'googleRelease');
  assert.equal(options.abi, 'arm64-v8a');
  assert.equal(options.output, '/tmp/out');
  assert.equal(options.readerBundle, '/tmp/reader');
  assert.equal(options.offline, true);
  assert.equal(options.timeoutSeconds, 90);
});

test('arguments take a media engine built in another checkout', () => {
  assert.equal(parseArguments(['--libmpv-dir', '/engine/out/android']).libmpvDir, '/engine/out/android');
  assert.equal(parseArguments([]).libmpvDir, null);
});

test('arguments reject a variant Gradle does not define', () => {
  assert.throws(() => parseArguments(['--variant', 'googleProfile']), /unknown_variant/);
  assert.throws(() => parseArguments(['--nonsense']), /unknown_argument/);
  assert.throws(() => parseArguments(['--timeout', '0']), /invalid_timeout/);
});

test('the APK sits where Gradle writes it for the flavour and build type', () => {
  assert.equal(apkFile('/a', 'googleDebug'), path.join('/a', 'app/build/outputs/apk/google/debug/app-google-debug.apk'));
  assert.equal(apkFile('/a', 'firetvRelease'), path.join('/a', 'app/build/outputs/apk/firetv/release/app-firetv-release.apk'));
});

test('the artifact name carries the variant, architecture and revision', () => {
  assert.equal(artifactFileName('googleDebug', 'x86_64', '82662ca6f1f2f3f4f5f6'),
    'portico-google-debug-x86_64-82662ca6.apk');
  assert.equal(artifactFileName('firetvRelease', 'arm64-v8a', 'abcdef0123456789'),
    'portico-firetv-release-arm64-v8a-abcdef01.apk');
});

test('the tree digest is the one the shell prints for the same files', () => {
  // sha256sum 256 of `find . -type f | LC_ALL=C sort | xargs sha256sum | sha256sum`.
  const digest = treeDigest([
    {path: 'b/c.txt', sha256: 'b9dd960c1753459a78115d3cb845a57d924b6877e805b08bd01086ccdf34433c'},
    {path: 'a.txt', sha256: '8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8'},
  ]);
  assert.equal(digest, '012b0be6a2ce5145dd35eb3e6b2d0b34c416d4fb33a818989617d0a953ff323a');
  assert.equal(treeDigest([]), createHash('sha256').digest('hex'));
});

test('the zip reader returns entry names and payloads', () => {
  const zip = storedZip([
    {name: 'assets/index.android.bundle', data: bundle},
    readerFile('index.html', '<html></html>'),
  ]);
  const entries = readZip(zip);
  assert.deepEqual(entries.map(entry => entry.name), ['assets/index.android.bundle', 'assets/PorticoReader.bundle/index.html']);
  assert.equal(entryData(zip, entries[0]).toString('utf8'), bundle.toString('utf8'));
  assert.equal(entryData(zip, entries[1]).toString('utf8'), '<html></html>');
});

test('a package carries embedded JavaScript and the same Reader tree', () => {
  const reader = [{path: 'index.html', sha256: createHash('sha256').update('<html></html>').digest('hex')}];
  const zip = storedZip([
    {name: 'assets/index.android.bundle', data: bundle},
    readerFile('index.html', '<html></html>'),
    {name: 'classes.dex', data: Buffer.from('dex')},
  ]);
  const result = verifyPackage(zip, {readerManifest: reader});
  assert.equal(result.embeddedJavaScript, true);
  assert.equal(result.javascriptBytes, bundle.length);
  assert.deepEqual(result.reader, {present: true, files: 1, treeDigest: treeDigest(reader)});
});

test('a package without its JavaScript is refused', () => {
  const zip = storedZip([{name: 'classes.dex', data: Buffer.from('dex')}]);
  assert.throws(() => verifyPackage(zip), /packaged_javascript_missing/);
});

test('Reader files that were not packaged, or differ, are refused', () => {
  const expected = [{path: 'index.html', sha256: createHash('sha256').update('<html></html>').digest('hex')}];
  const missing = storedZip([{name: 'assets/index.android.bundle', data: bundle}]);
  assert.throws(() => verifyPackage(missing, {readerManifest: expected}), /reader_packaged_differs .*missing=index\.html/);
  const changed = storedZip([{name: 'assets/index.android.bundle', data: bundle}, readerFile('index.html', '<html>other</html>')]);
  assert.throws(() => verifyPackage(changed, {readerManifest: expected}), /reader_packaged_differs .*changed=index\.html/);
  const extra = storedZip([{name: 'assets/index.android.bundle', data: bundle}, readerFile('index.html', '<html></html>'),
    readerFile('assets/stale.js', 'x')]);
  assert.throws(() => verifyPackage(extra, {readerManifest: expected}), /reader_packaged_differs .*extra=assets\/stale\.js/);
});

test('the reader manifest walks the staged directory in path order', async () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'portico-reader-'));
  try {
    fs.mkdirSync(path.join(directory, 'assets'));
    fs.writeFileSync(path.join(directory, 'index.html'), '<html></html>');
    fs.writeFileSync(path.join(directory, 'assets', 'pdf.worker-a.mjs'), 'x');
    const manifest = await directoryManifest(directory);
    assert.deepEqual(manifest.map(file => file.path), ['assets/pdf.worker-a.mjs', 'index.html']);
    assert.equal(manifest[1].sha256, createHash('sha256').update('<html></html>').digest('hex'));
  } finally {
    fs.rmSync(directory, {recursive: true, force: true});
  }
});

test('the whole run re-enters under the fleet lock', () => {
  const command = lockedCommand(['--variant', 'googleRelease'], 60, '/scripts/package.mjs');
  assert.deepEqual(command.slice(0, 5), ['flock', '-w', '60', '-E', '75']);
  assert.equal(command[5], lockFile);
  assert.equal(command[6], process.execPath);
  assert.equal(command[7], '/scripts/package.mjs');
  assert.deepEqual(command.slice(8), ['--variant', 'googleRelease']);
});

test('the artifact is written from the bytes that were verified', async () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'portico-publish-'));
  try {
    const apk = storedZip([{name: 'assets/index.android.bundle', data: bundle}]);
    const artifact = path.join(directory, 'portico.apk');
    const digest = await publishArtifact(apk, artifact);
    assert.equal(Buffer.compare(fs.readFileSync(artifact), apk), 0);
    assert.equal(digest, createHash('sha256').update(apk).digest('hex'));
  } finally {
    fs.rmSync(directory, {recursive: true, force: true});
  }
});

test('a timed-out build takes its SIGTERM-ignoring leader and grandchild with it', async () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'portico-supervise-'));
  const marker = path.join(directory, 'grandchild');
  let leader = 0;
  try {
    // The "build" ignores SIGTERM itself and spawns a child that ignores it too.
    const build = `
      process.on('SIGTERM', () => {});
      const {spawn} = require('node:child_process');
      const fs = require('node:fs');
      const grandchild = spawn('sh', ['-c', 'trap "" TERM; while :; do sleep 5; done'], {stdio: 'ignore'});
      fs.writeFileSync(process.env.MARKER, String(grandchild.pid));
      setInterval(() => {}, 1000);
    `;
    const started = Date.now();
    await assert.rejects(
      supervise([process.execPath, '-e', build], {
        env: {...process.env, MARKER: marker}, stdio: 'ignore', timeoutSeconds: 1, graceSeconds: 1,
        onSpawn: child => { leader = child.pid; },
      }),
      /terminated after 1s/,
    );
    assert.ok(leader > 0);
    assert.throws(() => process.kill(leader, 0), /ESRCH/);
    const grandchild = Number(fs.readFileSync(marker, 'utf8'));
    assert.ok(grandchild > 0);
    assert.throws(() => process.kill(grandchild, 0), /ESRCH/);
    assert.ok(Date.now() - started < 20000);
  } finally {
    fs.rmSync(directory, {recursive: true, force: true});
  }
});

test('a command that cannot start reports the spawn error, not a group probe', async () => {
  await assert.rejects(
    supervise(['/nonexistent/portico-build'], {stdio: 'ignore', timeoutSeconds: 5}),
    /ENOENT/,
  );
});

test('an unacquired fleet lock is reported, not hidden', async () => {
  await assert.rejects(
    supervise([process.execPath, '-e', 'process.exit(75)'], {stdio: 'ignore', timeoutSeconds: 10}),
    /gradle_lock_busy/,
  );
});

test('the build asks Gradle for an embedded bundle and passes --offline through', () => {
  const args = gradleArguments({variant: 'googleDebug', abi: 'x86_64', libmpvDir: null, offline: true}, '/a/android');
  assert.equal(args[0], '/a/android/gradlew');
  assert.equal(args[1], ':app:assembleGoogleDebug');
  assert.ok(args.includes('-PreactNativeArchitectures=x86_64'));
  assert.ok(args.includes('-PporticoEmbeddedJs=true'));
  assert.ok(args.includes('--offline'));
  const release = gradleArguments({variant: 'googleRelease', abi: 'arm64-v8a', libmpvDir: '/engine', offline: false}, '/a/android');
  assert.equal(release[1], ':app:assembleGoogleRelease');
  assert.ok(!release.includes('--offline'));
  assert.ok(release.includes('-PporticoLibmpvDir=/engine'));
});

test('a failed build reports its exit code', async () => {
  await assert.rejects(
    supervise([process.execPath, '-e', 'process.exit(3)'], {stdio: 'ignore', timeoutSeconds: 10}),
    /build_failed exit=3/,
  );
});
