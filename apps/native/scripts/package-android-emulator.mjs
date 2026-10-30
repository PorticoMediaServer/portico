#!/usr/bin/env node
/**
 * Package an Android APK that runs on an emulator with no Metro server.
 *
 * The Gradle build is the native path, not a second pipeline: `-PporticoEmbeddedJs=true` takes the
 * debug variants out of React Native's `debuggableVariants`, so its own bundle task writes
 * `assets/index.android.bundle` into the APK (android/app/build.gradle). Release variants already
 * carry the bundle. The Reader guest is staged by the build's own `porticoReaderAssets` task from
 * `PORTICO_READER_BUNDLE_DIR`, the same stager the Apple builds use.
 *
 * Fleet rules are part of the script so a build cannot quietly break them: the whole run — build,
 * verification and publication — holds /tmp/portico-gradle.lock, builds one ABI with one Gradle
 * worker, and its full output goes to <output>/build.log (only a summary reaches the terminal).
 * The process runs in its own process group: a timeout or a signal is sent to the group, then
 * SIGKILL after a grace period, and the script waits for the group to be gone before it returns.
 *
 * Usage:
 *   node scripts/package-android-emulator.mjs [--variant googleDebug] [--abi x86_64]
 *     [--output <dir>] [--reader-bundle <PorticoReader.bundle>] [--libmpv-dir <dir>] [--offline]
 *     [--timeout <seconds>]
 *
 * The last line on stdout is a JSON summary; status lines go to stderr.
 */
import {createHash} from 'node:crypto';
import {execFileSync, spawn} from 'node:child_process';
import * as fs from 'node:fs';
import * as fsp from 'node:fs/promises';
import * as os from 'node:os';
import * as path from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {inflateRawSync} from 'node:zlib';

export const lockFile = '/tmp/portico-gradle.lock';
const lockedEnvironment = 'PORTICO_PACKAGING_LOCKED';
const variants = new Map([
  ['googleDebug', {flavour: 'google', buildType: 'debug'}],
  ['googleRelease', {flavour: 'google', buildType: 'release'}],
  ['firetvDebug', {flavour: 'firetv', buildType: 'debug'}],
  ['firetvRelease', {flavour: 'firetv', buildType: 'release'}],
]);
const readerAssetRoot = 'assets/PorticoReader.bundle/';
const bundleAsset = 'assets/index.android.bundle';

export function parseArguments(argv) {
  const options = {
    variant: 'googleDebug',
    abi: 'x86_64',
    output: null,
    readerBundle: process.env.PORTICO_READER_BUNDLE_DIR || null,
    libmpvDir: process.env.PORTICO_LIBMPV_DIR || null,
    offline: false,
    timeoutSeconds: 3600,
    help: false,
  };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    const value = () => {
      const next = argv[++index];
      if (next === undefined) throw new Error(`missing_value ${argument}`);
      return next;
    };
    switch (argument) {
      case '--variant': options.variant = value(); break;
      case '--abi': options.abi = value(); break;
      case '--output': options.output = value(); break;
      case '--reader-bundle': options.readerBundle = value(); break;
      case '--libmpv-dir': options.libmpvDir = value(); break;
      case '--timeout': options.timeoutSeconds = Number(value()); break;
      case '--offline': options.offline = true; break;
      case '--help': case '-h': options.help = true; break;
      default: throw new Error(`unknown_argument ${argument}`);
    }
  }
  if (!variants.has(options.variant)) throw new Error(`unknown_variant ${options.variant}`);
  if (!/^[a-z0-9_-]+$/.test(options.abi)) throw new Error(`unknown_abi ${options.abi}`);
  if (!Number.isFinite(options.timeoutSeconds) || options.timeoutSeconds <= 0) throw new Error('invalid_timeout');
  return options;
}

/** The APK path Gradle writes for one variant (no ABI splits: the build passes one architecture). */
export function apkFile(androidDir, variant) {
  const parts = variants.get(variant);
  if (!parts) throw new Error(`unknown_variant ${variant}`);
  return path.join(androidDir, 'app', 'build', 'outputs', 'apk', parts.flavour, parts.buildType,
    `app-${parts.flavour}-${parts.buildType}.apk`);
}

export function artifactFileName(variant, abi, revision) {
  const parts = variants.get(variant);
  if (!parts) throw new Error(`unknown_variant ${variant}`);
  // The retained carriers Release hands out use this shape: portico-google-debug-x86_64-<sha8>.apk.
  return `portico-${parts.flavour}-${parts.buildType}-${abi}-${revision.slice(0, 8)}.apk`;
}

/** The digest Release and the Apple hand-off quote: sha256 of each file, sorted by path, hashed again. */
export function treeDigest(files) {
  const lines = files
    .map(file => ({line: Buffer.from(`${file.sha256}  ./${file.path}\n`), key: Buffer.from(file.path)}))
    .sort((left, right) => Buffer.compare(left.key, right.key));
  const digest = createHash('sha256');
  for (const {line} of lines) digest.update(line);
  return digest.digest('hex');
}

export async function directoryManifest(directory) {
  const root = path.resolve(directory);
  const files = [];
  async function walk(current, relative) {
    for (const name of (await fsp.readdir(current)).sort()) {
      const absolute = path.join(current, name);
      const info = await fsp.lstat(absolute);
      if (info.isSymbolicLink()) throw new Error(`reader_symlink ${path.join(relative, name)}`);
      if (info.isDirectory()) { await walk(absolute, relative ? `${relative}/${name}` : name); continue; }
      if (!info.isFile()) throw new Error(`reader_not_file ${path.join(relative, name)}`);
      const bytes = await fsp.readFile(absolute);
      files.push({path: relative ? `${relative}/${name}` : name, size: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex')});
    }
  }
  await walk(root, '');
  return files;
}

/** Entry names and payload locations from a ZIP central directory. No external unzip dependency. */
export function readZip(buffer) {
  let eocd = -1;
  for (let at = buffer.length - 22; at >= Math.max(0, buffer.length - 22 - 0xffff); at -= 1) {
    if (buffer.readUInt32LE(at) === 0x06054b50) { eocd = at; break; }
  }
  if (eocd < 0) throw new Error('zip_eocd_missing');
  const count = buffer.readUInt16LE(eocd + 10);
  const size = buffer.readUInt32LE(eocd + 12);
  const offset = buffer.readUInt32LE(eocd + 16);
  if (count === 0xffff || size === 0xffffffff || offset === 0xffffffff) throw new Error('zip64_unsupported');
  const entries = [];
  let cursor = offset;
  for (let index = 0; index < count; index += 1) {
    if (buffer.readUInt32LE(cursor) !== 0x02014b50) throw new Error('zip_central_directory_invalid');
    const method = buffer.readUInt16LE(cursor + 10);
    const compressedSize = buffer.readUInt32LE(cursor + 20);
    const uncompressedSize = buffer.readUInt32LE(cursor + 24);
    const nameLength = buffer.readUInt16LE(cursor + 28);
    const extraLength = buffer.readUInt16LE(cursor + 30);
    const commentLength = buffer.readUInt16LE(cursor + 32);
    const localHeaderOffset = buffer.readUInt32LE(cursor + 42);
    const name = buffer.toString('utf8', cursor + 46, cursor + 46 + nameLength);
    entries.push({name, method, compressedSize, uncompressedSize, localHeaderOffset});
    cursor += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}

export function entryData(buffer, entry) {
  const header = entry.localHeaderOffset;
  if (buffer.readUInt32LE(header) !== 0x04034b50) throw new Error('zip_local_header_invalid');
  const start = header + 30 + buffer.readUInt16LE(header + 26) + buffer.readUInt16LE(header + 28);
  const payload = buffer.subarray(start, start + entry.compressedSize);
  if (entry.method === 0) return payload;
  if (entry.method === 8) return inflateRawSync(payload);
  throw new Error(`zip_compression_unsupported ${entry.method}`);
}

/** Checks the packaged APK against the build's promise: embedded JavaScript, and the same Reader tree. */
export function verifyPackage(apk, {readerManifest = null} = {}) {
  const entries = readZip(apk);
  const names = new Set(entries.map(entry => entry.name));
  if (!names.has(bundleAsset)) throw new Error('packaged_javascript_missing');
  const bundleEntry = entries.find(entry => entry.name === bundleAsset);
  const bundle = entryData(apk, bundleEntry);
  const result = {
    embeddedJavaScript: true,
    javascriptBytes: bundle.length,
    hermes: bundle.length > 4 && bundle.readUInt32LE(0) === 0x03bc1fc6,
    reader: {present: false},
  };
  if (readerManifest) {
    const packaged = entries.filter(entry => entry.name.startsWith(readerAssetRoot) && !entry.name.endsWith('/'));
    const files = packaged.map(entry => ({
      path: entry.name.slice(readerAssetRoot.length),
      sha256: createHash('sha256').update(entryData(apk, entry)).digest('hex'),
    }));
    const expected = new Map(readerManifest.map(file => [file.path, file.sha256]));
    const actual = new Map(files.map(file => [file.path, file.sha256]));
    const missing = [...expected.keys()].filter(file => !actual.has(file)).sort();
    const extra = [...actual.keys()].filter(file => !expected.has(file)).sort();
    const changed = [...expected.keys()].filter(file => actual.has(file) && actual.get(file) !== expected.get(file)).sort();
    if (missing.length || extra.length || changed.length) {
      throw new Error(`reader_packaged_differs missing=${missing.join(',') || '-'} extra=${extra.join(',') || '-'} changed=${changed.join(',') || '-'}`);
    }
    result.reader = {present: true, files: files.length, treeDigest: treeDigest(files)};
  } else if (names.has(`${readerAssetRoot}index.html`)) {
    // Staged by the build from the environment: report it, but there is nothing to compare against.
    const files = entries.filter(entry => entry.name.startsWith(readerAssetRoot) && !entry.name.endsWith('/'));
    result.reader = {present: true, files: files.length, treeDigest: null};
  }
  return result;
}

/** Writes exactly the bytes that were verified, and reads them back before the caller records them. */
export async function publishArtifact(apk, artifact) {
  const expected = createHash('sha256').update(apk).digest('hex');
  await fsp.writeFile(artifact, apk);
  const written = await fsp.readFile(artifact);
  if (written.length !== apk.length || createHash('sha256').update(written).digest('hex') !== expected) {
    throw new Error(`artifact_write_mismatch ${artifact}`);
  }
  return expected;
}

/** The run under the fleet lock: the lock covers the build, verification and publication. */
export function lockedCommand(argv, timeoutSeconds, scriptPath) {
  return ['flock', '-w', String(timeoutSeconds), '-E', '75', lockFile, process.execPath, scriptPath, ...argv];
}

function groupAlive(pid) {
  try {
    process.kill(-pid, 0);
    return true;
  } catch (error) {
    return error.code !== 'ESRCH';
  }
}

const sleep = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));

/**
 * Runs one command as the leader of a new process group and never leaves the group behind.
 *
 * Termination is driven by timers, not by the leader exiting: a leader that ignores SIGTERM would
 * otherwise keep the wait open forever. The first timeout or forwarded signal sends its own signal
 * to the group, SIGKILL follows after `graceSeconds`, and after `graceSeconds + 10s` the wait stops
 * even if the leader has not reported its exit. Cleanup then removes anything left and the call
 * only settles once the group is gone.
 */
export async function supervise(argv, {cwd, env, stdio = 'inherit', timeoutSeconds, graceSeconds = 10, onSpawn} = {}) {
  const child = spawn(argv[0], argv.slice(1), {cwd, env, stdio, detached: true});
  onSpawn?.(child);
  const leader = child.pid;
  if (!leader) {
    // Spawn failed before a process existed: no group to signal, only the error to report.
    throw await new Promise(resolve => child.once('error', resolve));
  }
  let termination = null;
  const timers = [];
  const signalGroup = signal => {
    try {
      process.kill(-leader, signal);
    } catch (error) {
      if (error.code !== 'ESRCH') throw error;
    }
  };
  let settleStuck;
  const stuck = new Promise(resolve => { settleStuck = resolve; });
  const beginTermination = (reason, signal) => {
    if (termination) return;
    termination = reason;
    signalGroup(signal);
    timers.push(setTimeout(() => signalGroup('SIGKILL'), graceSeconds * 1000));
    timers.push(setTimeout(settleStuck, (graceSeconds + 10) * 1000));
  };
  const forwarded = new Map(['SIGINT', 'SIGTERM', 'SIGHUP'].map(signal => [signal, () => beginTermination('signal', signal)]));
  for (const [signal, handler] of forwarded) process.on(signal, handler);
  const deadline = setTimeout(() => beginTermination('timeout', 'SIGTERM'), timeoutSeconds * 1000);

  const exit = new Promise(resolve => {
    child.once('exit', (code, signal) => resolve({code, signal}));
    child.once('error', error => resolve({error}));
  });
  const outcome = await Promise.race([
    exit.then(result => ({kind: 'exit', result})),
    stuck.then(() => ({kind: 'stuck'})),
  ]);

  clearTimeout(deadline);
  for (const timer of timers) clearTimeout(timer);
  for (const [signal, handler] of forwarded) process.off(signal, handler);

  // Anything the group left behind goes too, and we do not return until it is gone.
  if (groupAlive(leader)) {
    signalGroup('SIGTERM');
    const leftover = Date.now() + 2000;
    while (groupAlive(leader) && Date.now() < leftover) await sleep(50);
  }
  if (groupAlive(leader)) {
    signalGroup('SIGKILL');
    const hard = Date.now() + 5000;
    while (groupAlive(leader) && Date.now() < hard) await sleep(50);
  }
  if (groupAlive(leader)) throw new Error('process_group_survived');

  if (termination === 'timeout') throw new Error(`terminated after ${timeoutSeconds}s`);
  if (termination === 'signal') throw new Error('interrupted');
  if (outcome.kind !== 'exit') throw new Error('process_group_survived');
  const {result} = outcome;
  if (result.error) throw result.error;
  if (result.code === 0) return result;
  if (result.code === 75) throw new Error('gradle_lock_busy');
  throw new Error(result.code === null ? `build_signal ${result.signal}` : `build_failed exit=${result.code}`);
}

/** The Gradle command for one variant: one ABI, one worker, embedded JavaScript, no daemon. */
export function gradleArguments(options, androidDir) {
  const task = `:app:assemble${options.variant[0].toUpperCase()}${options.variant.slice(1)}`;
  const command = [path.join(androidDir, 'gradlew'), task, '--console=plain', '--max-workers=1', '--no-daemon',
    `-PreactNativeArchitectures=${options.abi}`, '-PporticoEmbeddedJs=true'];
  if (options.libmpvDir) command.push(`-PporticoLibmpvDir=${path.resolve(options.libmpvDir)}`);
  if (options.offline) command.push('--offline');
  return command;
}

function javaHome() {
  // The React Native Gradle plugin pins JDK 17; a host's default may be newer.
  const pinned = '/usr/lib/jvm/java-17-openjdk-amd64';
  if (fs.existsSync(path.join(pinned, 'bin', 'java'))) return pinned;
  const current = process.env.JAVA_HOME;
  return current && fs.existsSync(path.join(current, 'bin', 'java')) ? current : null;
}

/** Builds, verifies and publishes the artifact. The caller must hold /tmp/portico-gradle.lock. */
export async function packageAndroidEmulator(options, {log = line => console.error(line)} = {}) {
  const nativeDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
  const repositoryRoot = path.resolve(nativeDir, '..', '..');
  const androidDir = path.join(nativeDir, 'android');
  const revision = execFileSync('git', ['rev-parse', 'HEAD'], {cwd: repositoryRoot, encoding: 'utf8'}).trim();
  const dirty = execFileSync('git', ['status', '--porcelain'], {cwd: repositoryRoot, encoding: 'utf8'}).trim().length > 0;
  const output = path.resolve(options.output || path.join(os.tmpdir(), 'portico-android-package', revision.slice(0, 12)));
  await fsp.mkdir(output, {recursive: true});

  if (!fs.existsSync(path.join(nativeDir, 'node_modules'))) {
    throw new Error('node_modules_missing: run npm install --legacy-peer-deps in apps/native first');
  }
  let readerManifest = null;
  if (options.readerBundle) {
    if (!fs.existsSync(path.join(options.readerBundle, 'index.html'))) {
      throw new Error(`reader_bundle_invalid ${options.readerBundle}`);
    }
    readerManifest = await directoryManifest(options.readerBundle);
    log(`reader bundle: ${readerManifest.length} files, tree ${treeDigest(readerManifest)}`);
  } else {
    log('reader bundle: none (ReadNativeHost will report reader_assets_unavailable)');
  }

  const gradle = gradleArguments(options, androidDir);
  let libmpvDir = null;
  if (options.libmpvDir) {
    libmpvDir = path.resolve(options.libmpvDir);
    if (!fs.existsSync(path.join(libmpvDir, 'lib', options.abi, 'libmpv.so'))) {
      throw new Error(`libmpv_missing ${path.join(libmpvDir, 'lib', options.abi)}`);
    }
    log(`media engine: ${libmpvDir}`);
  }
  const home = javaHome();
  const environment = {...process.env, CI: '1', NODE_BINARY: process.execPath};
  if (home) environment.JAVA_HOME = home;
  if (options.readerBundle) environment.PORTICO_READER_BUNDLE_DIR = path.resolve(options.readerBundle);
  else delete environment.PORTICO_READER_BUNDLE_DIR;

  const logPath = path.join(output, 'build.log');
  const logHandle = fs.openSync(logPath, 'w');
  log(`building ${options.variant} ${options.abi} at ${revision.slice(0, 12)}${dirty ? ' (dirty tree)' : ''}; log ${logPath}`);
  try {
    const child = spawn(gradle[0], gradle.slice(1), {
      cwd: androidDir, env: environment, stdio: ['ignore', logHandle, logHandle],
    });
    const exit = await new Promise(resolve => {
      child.on('exit', (code, signal) => resolve({code, signal}));
      child.on('error', error => resolve({error}));
    });
    if (exit.error) throw exit.error;
    if (exit.code !== 0) throw new Error(exit.code === null ? `build_signal ${exit.signal}` : `build_failed exit=${exit.code}`);
  } finally {
    fs.closeSync(logHandle);
  }

  const built = apkFile(androidDir, options.variant);
  if (!fs.existsSync(built)) throw new Error(`apk_missing ${built}`);
  const apk = await fsp.readFile(built);
  const verification = verifyPackage(apk, {readerManifest});
  const artifact = path.join(output, artifactFileName(options.variant, options.abi, revision));
  const sha256 = await publishArtifact(apk, artifact);
  const summary = {
    variant: options.variant,
    abi: options.abi,
    revision,
    dirty,
    embeddedJavaScript: verification.embeddedJavaScript,
    hermes: verification.hermes,
    javascriptBytes: verification.javascriptBytes,
    reader: verification.reader,
    libmpvDir,
    apk: artifact,
    bytes: apk.length,
    sha256,
    log: logPath,
  };
  await fsp.writeFile(path.join(output, 'packaging.json'), `${JSON.stringify(summary, null, 2)}\n`);
  return summary;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const scriptPath = fileURLToPath(import.meta.url);
  const options = parseArguments(process.argv.slice(2));
  const fail = error => {
    process.stderr.write(`failed: ${error.message}\n`);
    if (/build_failed|build_timeout|terminated|artifact_write_mismatch/.test(error.message)) {
      const output = options.output ? path.resolve(options.output) : null;
      const logPath = output ? path.join(output, 'build.log') : null;
      if (logPath && fs.existsSync(logPath)) process.stderr.write(`${fs.readFileSync(logPath, 'utf8').split('\n').slice(-40).join('\n')}\n`);
    }
    process.exit(error.message === 'gradle_lock_busy' ? 75 : 1);
  };
  if (options.help) {
    process.stderr.write('usage: package-android-emulator.mjs [--variant googleDebug] [--abi x86_64] [--output <dir>] [--reader-bundle <dir>] [--libmpv-dir <dir>] [--offline] [--timeout <seconds>]\n');
    process.exit(0);
  }
  if (process.env[lockedEnvironment] === '1') {
    packageAndroidEmulator(options).then(summary => {
      process.stdout.write(`${JSON.stringify(summary)}\n`);
    }).catch(fail);
  } else {
    // Re-enter under the fleet lock: it is held until the build, verification and publication end.
    supervise(lockedCommand(process.argv.slice(2), options.timeoutSeconds, scriptPath), {
      env: {...process.env, [lockedEnvironment]: '1'},
      stdio: 'inherit',
      timeoutSeconds: options.timeoutSeconds * 2,
    }).then(() => process.exit(0)).catch(fail);
  }
}
