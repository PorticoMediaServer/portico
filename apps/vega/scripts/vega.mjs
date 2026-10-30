#!/usr/bin/env node
// Builds Portico for Vega and drives a device (the SDK's virtual device or a Fire TV in developer
// mode). `node scripts/vega.mjs help` lists the commands.
//
// Two things about the SDK's tools shape this file:
//
// - They pass paths to a shell unquoted, so they fail in a checkout whose path contains a space.
//   From such a checkout the app, the shared sources and the packages are mirrored to a directory
//   without spaces and built there; the packages come back to `build/`. Elsewhere the build runs
//   in place.
// - Their wrappers start with `env bash`. On an Apple-silicon Mac whose PATH has an Intel bash
//   first, every tool then runs translated and the packaging step looks for an Intel binary the
//   SDK does not ship. The system's bash is put first.
// - The virtual device is a fork of the Android emulator and its device tool (vda) a fork of adb,
//   with the same default ports. Beside Android emulators the two get confused (each tool sees,
//   and may stop, the other's devices), so the virtual device is started on ports of its own and
//   spoken to through a device-tool server of its own.
import {spawnSync} from 'node:child_process';
import {copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync} from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const app = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repository = path.resolve(app, '../..');
const shared = path.resolve(app, '../native');
const packages = path.resolve(repository, 'packages');
const appId = JSON.parse(readFileSync(path.join(app, 'app.json'), 'utf8')).name;
const packageId = /\[package\][\s\S]*?\nid\s*=\s*"([A-Za-z0-9_.-]+)"/.exec(readFileSync(path.join(app, 'manifest.toml'), 'utf8'))?.[1];
if (!packageId) throw new Error('The Vega manifest has no package ID.');
const packageName = JSON.parse(readFileSync(path.join(app, 'package.json'), 'utf8')).name;
// The virtual device's port (its console is one below) and the device-tool server's port.
const devicePort = Number(process.env.PORTICO_VEGA_DEVICE_PORT ?? 5701);
const env = {
  ...process.env,
  PATH: process.platform === 'darwin' ? `/bin:/usr/bin:${process.env.PATH}` : process.env.PATH,
  ANDROID_ADB_SERVER_PORT: process.env.PORTICO_VEGA_ADB_PORT ?? '5039',
};

const fonts = {'manrope-400.ttf': 'Manrope-Regular.ttf', 'manrope-500.ttf': 'Manrope-Medium.ttf', 'manrope-600.ttf': 'Manrope-SemiBold.ttf', 'manrope-700.ttf': 'Manrope-Bold.ttf'};
// The remote's keys, by their Linux input names.
const keys = {up: 'KEY_UP', down: 'KEY_DOWN', left: 'KEY_LEFT', right: 'KEY_RIGHT', select: 'KEY_ENTER', back: 'KEY_BACK', menu: 'KEY_MENU', home: 'KEY_HOMEPAGE', playpause: 'KEY_PLAYPAUSE', rewind: 'KEY_REWIND', fastforward: 'KEY_FASTFORWARD'};

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {stdio: 'inherit', env, ...options});
  if (result.error) throw result.error;
  if (result.status !== 0 && !options.allowFailure) fail(`${command} ${args.join(' ')} exited with ${result.status}`);
  return result;
}
function capture(command, args) {
  const result = spawnSync(command, args, {encoding: 'utf8', env});
  return {status: result.status, text: `${result.stdout ?? ''}${result.stderr ?? ''}`};
}
function fail(message) { console.error(message); process.exit(1); }

/** Where the build runs: here, or a mirror without spaces in its path. */
function workspace() {
  if (!/\s/.test(app)) return {root: repository, app, staged: false};
  const cache = process.env.PORTICO_VEGA_STAGE ?? path.join(process.env.XDG_CACHE_HOME ?? path.join(os.homedir(), '.cache'), 'portico-vega-stage');
  const root = /\s/.test(cache) ? path.join(os.tmpdir(), 'portico-vega-stage') : cache;
  if (/\s/.test(root)) fail(`No directory without spaces is available for the build (tried ${cache}). Set PORTICO_VEGA_STAGE.`);
  return {root, app: path.join(root, 'apps/vega'), staged: true};
}

function mirror(from, to, extra = []) {
  mkdirSync(to, {recursive: true});
  run('rsync', ['-a', '--delete', ...extra, `${from}/`, `${to}/`]);
}

/** Brings the mirror up to date with this checkout (only what changed is copied). */
function stage(work) {
  if (!work.staged) return;
  mirror(app, work.app, ['--exclude', '/build', '--exclude', '/*.log', '--exclude', '/assets/fonts']);
  for (const part of ['src-next', 'platform', 'assets']) mirror(path.join(shared, part), path.join(work.root, 'apps/native', part));
  for (const file of ['App.tsx', 'package.json']) copyFileSync(path.join(shared, file), path.join(work.root, 'apps/native', file));
  mirror(packages, path.join(work.root, 'packages'), ['--exclude', 'node_modules']);
}

/** The shared typeface, under the family names the design system asks for. */
function copyFonts(work) {
  const to = path.join(work.app, 'assets/fonts');
  mkdirSync(to, {recursive: true});
  for (const [file, name] of Object.entries(fonts)) copyFileSync(path.join(shared, 'assets', file), path.join(to, name));
}

function build(type) {
  const work = workspace();
  stage(work);
  copyFonts(work);
  if (work.staged) console.log(`Building in ${work.app} (this checkout's path contains a space).`);
  // The SDK expands needs.module in the source manifest as well as the packaged
  // manifest. Keep that generated dependency list out of the working tree.
  const manifest = path.join(work.app, 'manifest.toml');
  const originalManifest = readFileSync(manifest);
  try {
    const result = run('npx', ['react-native', 'build-vega', '--build-type', type], {cwd: work.app, allowFailure: true});
    if (result.status !== 0) throw new Error(`Vega build exited with ${result.status}`);
  } finally {
    writeFileSync(manifest, originalManifest);
  }
  if (work.staged) {
    // Bring back the packages (not the intermediate files).
    for (const entry of readdirSync(path.join(work.app, 'build'))) {
      if (!/^(aarch64|armv7|x86_64)-(debug|release)$/.test(entry)) continue;
      mirror(path.join(work.app, 'build', entry), path.join(app, 'build', entry));
    }
  }
  console.log(`Built ${packageFile(type, deviceArchitecture() ?? 'aarch64')}`);
}

function packageFile(type, architecture) {
  return path.join(app, 'build', `${architecture}-${type.toLowerCase()}`, `${packageName}_${architecture}.vpkg`);
}

function vega(args, options) { return run('vega', args, options); }

/** aarch64, armv7 or x86_64 for the connected device; undefined when none is connected. */
function deviceArchitecture() {
  const {text} = capture('vega', ['device', 'list']);
  return /\b(aarch64|armv7|x86_64)\b/.exec(text)?.[1];
}

function install(type) {
  const architecture = deviceArchitecture() ?? fail('No device is connected. Start the virtual device: node scripts/vega.mjs device start');
  const file = packageFile(type, architecture);
  if (!existsSync(file)) fail(`${file} has not been built.`);
  // The device tool has the same trouble with spaces as the build tools.
  const work = workspace();
  const from = work.staged ? path.join(work.app, 'build', path.basename(path.dirname(file)), path.basename(file)) : file;
  if (work.staged) { mkdirSync(path.dirname(from), {recursive: true}); copyFileSync(file, from); }
  vega(['device', 'terminate-app', '--appName', appId], {allowFailure: true, stdio: 'ignore'});
  vega(['device', 'install-app', '--packagePath', from]);
}

function launch() { vega(['device', 'launch-app', '--appName', appId]); }

let adaptor;
/** Runs a command in the device's shell, through the SDK's device adaptor (called directly: the
 * `vega exec` wrapper re-splits the command). */
function shell(command) {
  adaptor ??= capture('vega', ['which', 'vda']).text.trim().split('\n').pop();
  if (!adaptor || !existsSync(adaptor)) fail('The SDK\'s device adaptor (vda) was not found.');
  return capture(adaptor, ['-s', `emulator-${devicePort - 1}`, 'shell', command]);
}

function press(names) {
  for (const name of names) {
    const [key, count = '1'] = name.split('*');
    // A Linux input name (KEY_BACKSPACE) may be given as it is.
    const code = keys[key] ?? (/^KEY_[A-Z0-9_]+$/.test(key) ? key : fail(`Unknown key "${key}". Keys: ${Object.keys(keys).join(', ')}, or a KEY_ name`));
    for (let i = 0; i < Number(count); i++) {
      const result = shell(`inputd-cli button_press ${code}`);
      if (result.status !== 0) fail(result.text.trim() || `The key ${key} could not be sent.`);
      spawnSync('sleep', ['0.35']);
    }
  }
}

/** Types text into the focused field (the system keyboard's field included). */
function type(text) {
  const result = shell(`inputd-cli send_text ${JSON.stringify(text)}`);
  if (result.status !== 0) fail(result.text.trim() || 'The text could not be sent.');
}

/**
 * A picture of the virtual device's screen, taken by the emulator it runs in (its console, one
 * port below the device port, which the SDK's own tools authorize with the same token file). The device's
 * `screenshooter` is not used: it ends the developer shell.
 */
async function screenshot(file) {
  const tokenFile = path.join(os.homedir(), '.emulator_console_auth_token');
  if (!existsSync(tokenFile)) fail('The virtual device console has no token file; is the virtual device running?');
  if (!virtualDeviceRunning()) fail('The virtual device is not running: node scripts/vega.mjs device start');
  const directory = path.join(os.tmpdir(), `portico-vega-shot-${process.pid}`);
  mkdirSync(directory, {recursive: true});
  await new Promise((resolve, reject) => {
    const socket = net.connect(devicePort - 1, '127.0.0.1');
    const steps = [`auth ${readFileSync(tokenFile, 'utf8').trim()}`, `screenrecord screenshot ${directory}`, 'quit'];
    let seen = '';
    socket.setTimeout(15000, () => { socket.destroy(); reject(new Error('The virtual device console did not answer.')); });
    socket.on('data', data => {
      seen += data.toString();
      // The console greets with OK, then answers each command with OK or KO.
      while (/\r?\nOK\r?\n|^OK\r?\n|KO:/.test(seen)) {
        if (/KO:/.test(seen)) { socket.destroy(); reject(new Error(seen.split('KO:')[1].trim())); return; }
        seen = seen.replace(/[\s\S]*?OK\r?\n/, '');
        const next = steps.shift();
        if (next) socket.write(next + '\r\n'); else socket.end();
      }
    });
    socket.on('error', reject);
    socket.on('close', resolve);
  });
  const made = readdirSync(directory).filter(name => name.endsWith('.png')).map(name => path.join(directory, name)).sort((a, b) => statSync(b).mtimeMs - statSync(a).mtimeMs)[0];
  if (!made) fail('The virtual device did not write a screenshot.');
  mkdirSync(path.dirname(path.resolve(file)), {recursive: true});
  copyFileSync(made, path.resolve(file));
  rmSync(directory, {recursive: true, force: true});
  console.log(path.resolve(file));
}

function sdkPath() {
  return capture('vega', ['exec', 'printenv', 'KEPLER_SDK_PATH']).text.trim().split('\n').pop();
}

function virtualDeviceRunning() {
  return /"running":\s*true/.test(capture('vega', ['virtual-device', 'status']).text);
}

/** Starts the SDK's virtual device without a window (PORTICO_VEGA_GUI=1 shows one) on its own port. */
function startVirtualDevice() {
  if (virtualDeviceRunning()) { console.log('The virtual device is already running.'); return; }
  const sdk = sdkPath();
  if (!sdk || !existsSync(sdk)) fail('The Vega SDK was not found (is `vega` on the PATH?).');
  const args = ['exec', 'virtualdevice', 'create-and-launch', '--image-path', path.join(sdk, 'vvd/images/tv'), '--log-path', path.join(sdk, 'vvd'), '--instances-path', path.join(sdk, 'vvd/instances'),
    '--name', 'default_instance', '--timeout-sec', '300', '--vda-port', String(devicePort)];
  if (process.env.PORTICO_VEGA_GUI !== '1') args.push('--gui-off');
  if (process.env.PORTICO_VEGA_GL_ACCEL === '0') args.push('--gl-accel-off');
  vega(args);
  // SDK 0.24 may exit zero after its boot deadline and leave no usable guest.
  if (!virtualDeviceRunning() || !deviceArchitecture()) fail('The Vega virtual device did not become ready.');
}

/**
 * The app's recent log lines: what its JavaScript wrote (console output arrives tagged
 * KeplerScript-JavaScript) and what the runtime said about it. `--all` prints every line.
 */
function logs(lines, everything) {
  const result = shell(`loggingctl log -v ${packageId} 2>&1 | tail -${Number(lines) || 4000}`);
  if (result.status !== 0) fail(result.text.trim() || 'The device logs could not be read.');
  const mine = /portico\.vega\[\d+\]/;
  for (const line of result.text.split('\n')) {
    if (everything || (mine.test(line) && !/GWSI_LOG|Inputd-client|SurfaceTelemetryLogger/.test(line))) console.log(line);
  }
}

const [command = 'help', ...rest] = process.argv.slice(2);
const flag = name => rest.includes(`--${name}`);
const buildType = flag('release') ? 'Release' : 'Debug';
const positional = rest.filter(arg => !arg.startsWith('--'));

switch (command) {
  case 'build': build(buildType); break;
  case 'install': install(buildType); break;
  case 'launch': launch(); break;
  case 'stop': vega(['device', 'terminate-app', '--appName', appId], {allowFailure: true}); break;
  case 'run': build(buildType); install(buildType); launch(); break;
  case 'key': press(positional); break;
  case 'type': type(positional.join(' ')); break;
  case 'shot': await screenshot(positional[0] ?? 'screenshot.png'); break;
  case 'logs': logs(positional[0], flag('all')); break;
  case 'start': {
    const work = workspace();
    stage(work);
    run('npx', ['react-native', 'start'], {cwd: work.app});
    break;
  }
  case 'device': {
    const action = positional[0] ?? 'status';
    if (action === 'start') startVirtualDevice();
    else if (action === 'stop') vega(['virtual-device', 'stop']);
    else { vega(['virtual-device', 'status'], {allowFailure: true}); vega(['device', 'list'], {allowFailure: true}); }
    break;
  }
  case 'clean': {
    rmSync(path.join(app, 'build'), {recursive: true, force: true});
    const work = workspace();
    if (work.staged) rmSync(work.root, {recursive: true, force: true});
    break;
  }
  default:
    console.log(`Usage: node scripts/vega.mjs <command>

  build [--release]      Build the packages (Debug unless --release) into build/
  install [--release]    Install the built package on the connected device
  launch | stop          Start or end the app on the device
  run [--release]        build, install and launch
  key <name[*n]>...      Press remote keys: ${Object.keys(keys).join(', ')}
  type <text>            Type into the focused field
  shot <file.png>        Screenshot of the virtual device
  logs [lines] [--all]   The app's recent log lines (--all: every line on the device)
  device start|stop|status   The SDK's virtual device
  start                  Metro, for a Debug build to load from
  clean                  Remove build output (and the build mirror)`);
}
