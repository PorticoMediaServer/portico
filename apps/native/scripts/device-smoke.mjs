#!/usr/bin/env node
/**
 * Repeatable on-device UI smoke runner for Portico Native (phone & TV).
 *
 * Drives UI strictly through the T3-provided `AGENT_DEVICE_BIN` CLI
 * (`open`, `snapshot -i`, `press`, `swipe`, `tv-remote press`). Never invokes
 * raw `adb shell input`, `xcrun simctl ui`, or `idb` for UI interaction.
 * `adb` is used ONLY for optional post-run logcat JS/fatal error scanning when
 * `ANDROID_SERIAL` is explicitly set.
 *
 * Usage:
 *   AGENT_DEVICE_BIN="agent-device --device ..." node apps/native/scripts/device-smoke.mjs --mode phone
 *   AGENT_DEVICE_BIN="agent-device --device ..." node apps/native/scripts/device-smoke.mjs --mode tv
 *   node apps/native/scripts/device-smoke.mjs --dry-run --mode phone
 *   node apps/native/scripts/device-smoke.mjs --dry-run --mode tv
 */
import {spawnSync} from 'node:child_process';
import process from 'node:process';

const DEFAULT_PACKAGE = 'tv.getportico.app';
const FATAL_LOGCAT_PATTERN = /FATAL EXCEPTION|JavascriptException|Fatal signal \d+|Unhandled JS Exception|ANR in tv\.getportico/i;

function usage() {
  return [
    'Usage: node apps/native/scripts/device-smoke.mjs --mode <phone|tv> [options]',
    '',
    'Options:',
    '  --mode <phone|tv>   Required. Target device form factor.',
    `  --package <id>      App bundle/package id (default: ${DEFAULT_PACKAGE}).`,
    '  --dry-run           Print the planned smoke steps without invoking a device.',
    '  --help, -h          Show this help message.',
    '',
    'Environment:',
    '  AGENT_DEVICE_BIN    Required (unless --dry-run/--help). Bound agent-device CLI from device_open.',
    '  ANDROID_SERIAL      Optional. When set, scans `adb -s <serial> logcat -d` for JS/native fatals.',
  ].join('\n');
}

export function parseArgs(argv) {
  const options = {mode: undefined, pkg: DEFAULT_PACKAGE, dryRun: false, help: false};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--help' || arg === '-h') {
      options.help = true;
    } else if (arg === '--dry-run') {
      options.dryRun = true;
    } else if (arg === '--mode') {
      options.mode = argv[++i];
    } else if (arg.startsWith('--mode=')) {
      options.mode = arg.slice('--mode='.length);
    } else if (arg === '--package') {
      options.pkg = argv[++i];
    } else if (arg.startsWith('--package=')) {
      options.pkg = arg.slice('--package='.length);
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  if (!options.help) {
    if (options.mode !== 'phone' && options.mode !== 'tv') {
      throw new Error('Missing or invalid --mode (expected "phone" or "tv").');
    }
    if (!options.pkg) throw new Error('--package requires a non-empty value.');
  }
  return options;
}

/** Splits an AGENT_DEVICE_BIN command string into [executable, ...prefixArgs] without invoking a shell. */
function splitCommand(command) {
  const parts = [];
  let current = '', quote = '';
  for (let i = 0; i < command.length; i++) {
    const ch = command[i];
    if (quote) {
      if (ch === quote) quote = '';
      else current += ch;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
    } else if (/\s/.test(ch)) {
      if (current) {parts.push(current); current = '';}
    } else {
      current += ch;
    }
  }
  if (current) parts.push(current);
  if (!parts.length) throw new Error('AGENT_DEVICE_BIN is empty.');
  return {bin: parts[0], prefixArgs: parts.slice(1)};
}

function sleepMs(ms) {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

function createDriver(agentDeviceBin, dryRun) {
  const parsed = dryRun ? {bin: 'agent-device', prefixArgs: []} : splitCommand(agentDeviceBin);
  const run = (args) => {
    if (dryRun) return {stdout: '', stderr: ''};
    const result = spawnSync(parsed.bin, [...parsed.prefixArgs, ...args], {encoding: 'utf8', timeout: 30_000});
    if (result.error) throw result.error;
    if (result.status !== 0) {
      throw new Error(`agent-device failed (${result.status}): ${args.join(' ')}\n${result.stderr || result.stdout}`);
    }
    return {stdout: result.stdout ?? '', stderr: result.stderr ?? ''};
  };
  return {
    open: (pkg) => run(['open', pkg, '--relaunch']),
    snapshot: () => run(['snapshot', '-i']).stdout,
    waitFor: (pattern, description, attempts = 8, delayMs = 750) => {
      let snap = '';
      for (let attempt = 0; attempt < attempts; attempt++) {
        snap = run(['snapshot', '-i']).stdout;
        if (pattern.test(snap)) return snap;
        if (attempt + 1 < attempts) sleepMs(delayMs);
      }
      throw new Error(`Smoke assertion failed (${description}): pattern ${pattern} not found in snapshot.`);
    },
    press: (ref) => run(['press', ref, '--settle']),
    swipe: (x1, y1, x2, y2) => run(['swipe', String(x1), String(y1), String(x2), String(y2)]),
    tvRemotePress: (key) => {
      run(['tv-remote', 'press', key]);
      if (!dryRun) sleepMs(350);
    },
  };
}

/** Extracts an interactive element ref (e.g. `@e12`) matching `pattern` from `snapshot -i` output. */
function findRef(snapshot, pattern, label) {
  const lines = snapshot.split('\n');
  for (const preferred of [/\[button\]/, /\[tab\]|\[group\]/, /./]) {
    for (const line of lines) {
      if (!preferred.test(line) || !pattern.test(line)) continue;
      if (/\[scroll-area\]|\[horizontalscrollview\]/.test(line)) continue;
      const match = line.match(/@[A-Za-z0-9_-]+/);
      if (match) return match[0];
    }
  }
  throw new Error(`Could not locate interactive ref for "${label}" in snapshot.`);
}

function assertNoGuideEllipsisStall(snapshot) {
  // Check that channel/programme rows rendered real titles rather than placeholder `…` stalls.
  const lines = snapshot.split('\n').map(l => l.trim()).filter(Boolean);
  const stalled = lines.filter(line => /["']\s*(?:…|\.\.\.)\s*["']/.test(line));
  if (stalled.length > 0) {
    throw new Error(`EPG grid reported ${stalled.length} bare ellipsis stall(s): ${stalled.slice(0, 3).join(' | ')}`);
  }
}

function scanAndroidLogcat(serial, pkg, dryRun) {
  if (!serial) return {checked: false, reason: 'ANDROID_SERIAL not set'};
  if (dryRun) return {checked: true, dryRun: true, serial};
  const result = spawnSync('adb', ['-s', serial, 'logcat', '-d', '-t', '500'], {encoding: 'utf8', timeout: 15_000});
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`adb logcat failed (${result.status}): ${result.stderr}`);
  const hits = result.stdout.split('\n').filter(line => FATAL_LOGCAT_PATTERN.test(line) || line.includes(`ANR in ${pkg}`));
  if (hits.length > 0) {
    throw new Error(`Logcat fatal/JS errors detected (${hits.length}):\n${hits.slice(0, 10).join('\n')}`);
  }
  return {checked: true, serial, fatalCount: 0};
}

export function buildPlan(mode, pkg) {
  if (mode === 'phone') {
    return [
      {id: 'relaunch', label: `Relaunch ${pkg}`, command: ['open', pkg, '--relaunch'], run: (d) => d.open(pkg)},
      {
        id: 'verify-home',
        label: 'Verify Home rendered',
        command: ['snapshot', '-i'],
        run: (d) => {
          d.waitFor(/Continue Watching|Recently added|Libraries/i, 'phone Home visible');
        },
      },
      {
        id: 'scroll-home',
        label: 'Scroll Home down and back up',
        command: ['swipe', '540', '1600', '540', '600'],
        run: (d) => {
          d.swipe(540, 1600, 540, 600);
          d.swipe(540, 600, 540, 1800);
        },
      },
      {
        id: 'open-libraries',
        label: 'Open Libraries tab',
        command: ['press', '@libraries'],
        run: (d) => {
          const snap = d.waitFor(/"Libraries"/i, 'Libraries tab visible');
          d.press(findRef(snap, /"Libraries"/i, 'Libraries tab'));
        },
      },
      {
        id: 'open-films',
        label: 'Open Films library',
        command: ['press', '@films'],
        run: (d) => {
          const snap = d.waitFor(/\[button\] "(?:Films|Movies)|Switch library:|Sort by/i, 'Films library or overview visible');
          if (!/Switch library:|Sort by/i.test(snap)) {
            d.press(findRef(snap, /"(?:Films|Movies)/i, 'Films library'));
          }
        },
      },
      {
        id: 'fling-grid',
        label: 'Fling Films grid',
        command: ['swipe', '540', '1600', '540', '500'],
        run: (d) => {
          d.waitFor(/Filters|Sort by/i, 'Films grid toolbar visible');
          d.swipe(540, 1600, 540, 500);
          d.snapshot();
        },
      },
      {
        id: 'open-live-tv',
        label: 'Open Live TV tab and verify EPG grid has zero ellipsis stalls',
        command: ['press', '@live-tv'],
        run: (d) => {
          const snap = d.waitFor(/"Live TV"/i, 'Live TV tab visible');
          d.press(findRef(snap, /"Live TV"/i, 'Live TV tab'));
          const guide = d.waitFor(/Guide|Channels|Now|On now/i, 'EPG guide rendered');
          assertNoGuideEllipsisStall(guide);
        },
      },
      {
        id: 'open-settings',
        label: 'Return to Home and open Settings',
        command: ['press', '@settings'],
        run: (d) => {
          const snap = d.waitFor(/"Home"/i, 'Home tab visible');
          d.press(findRef(snap, /"Home"/i, 'Home tab'));
          const home = d.waitFor(/"Settings"/i, 'Settings button visible');
          d.press(findRef(home, /"Settings"/i, 'Settings button'));
          d.waitFor(/Switch profile|Account/i, 'Settings screen rendered');
        },
      },
      {
        id: 'switch-profile',
        label: 'Open Switch profile flow',
        command: ['press', '@switch-profile'],
        run: (d) => {
          const snap = d.waitFor(/Switch profile/i, 'Switch profile button visible');
          d.press(findRef(snap, /Switch profile/i, 'Switch profile'));
          d.waitFor(/Who.?s watching|Profiles|Add profile/i, 'Profile switcher rendered');
        },
      },
    ];
  }

  return [
    {id: 'relaunch', label: `Relaunch ${pkg}`, command: ['open', pkg, '--relaunch'], run: (d) => d.open(pkg)},
    {
      id: 'verify-tv-home-focus',
      label: 'Verify TV Home start focus',
      command: ['snapshot', '-i'],
      run: (d) => {
        d.waitFor(/\[focused\]/i, 'TV Home start focus');
      },
    },
    {
      id: 'dpad-row-down-up',
      label: 'D-pad Down through short row and back Up',
      command: ['tv-remote', 'press', 'down'],
      run: (d) => {
        d.tvRemotePress('down');
        d.waitFor(/\[focused\]/i, 'TV Home row 2 focused');
        d.tvRemotePress('up');
        d.waitFor(/\[focused\]/i, 'TV Home row 1 focused');
      },
    },
    {
      id: 'rail-open-and-return',
      label: 'Left to Rail, then Right back to page',
      command: ['tv-remote', 'press', 'left'],
      run: (d) => {
        d.tvRemotePress('left');
        d.waitFor(/"Home" \[selected\] \[focused\]/i, 'TV Rail opened on Home');
        d.tvRemotePress('right');
        d.waitFor(/\[focused\]/i, 'TV Rail returned to page');
      },
    },
    {
      id: 'rail-to-films',
      label: 'Rail -> Films',
      command: ['tv-remote', 'press', 'left'],
      run: (d) => {
        d.tvRemotePress('left');
        d.tvRemotePress('down');
        d.tvRemotePress('select');
        d.waitFor(/Switch library:.*(?:Films|Movies)/i, 'TV Films library rendered');
        d.waitFor(/\[button\] "(?!Films"|Movies"|Home"|Search")[^"]+" \[focused\]/i, 'TV Films card focused');
      },
    },
    {
      id: 'open-title-and-back-restore',
      label: 'Open title -> Back restore',
      command: ['tv-remote', 'press', 'select'],
      run: (d) => {
        d.tvRemotePress('select');
        d.waitFor(/"(?:Play|Resume)" \[focused\]/i, 'TV title detail focused on Play/Resume');
        d.tvRemotePress('back');
        d.waitFor(/Switch library:.*(?:Films|Movies)/i, 'TV Back returned to Films grid');
        d.waitFor(/\[button\] "(?!Films"|Movies"|Home"|Search"|Play"|Resume")[^"]+" \[focused\]/i, 'TV Back restored Films grid focus');
      },
    },
    {
      id: 'rail-to-search',
      label: 'Rail -> Search',
      command: ['tv-remote', 'press', 'left'],
      run: (d) => {
        d.tvRemotePress('left');
        d.waitFor(/"(?:Films|Movies)" \[selected\] \[focused\]/i, 'TV Rail opened on Films');
        d.tvRemotePress('up');
        d.tvRemotePress('up');
        d.waitFor(/"Search" \[focused\]/i, 'TV Rail focused on Search');
        d.tvRemotePress('select');
        d.waitFor(/native-tv-search-screen|Movies, shows/i, 'TV Search rendered');
      },
    },
    {
      id: 'search-type-and-back-memory',
      label: 'Type "a" -> Right into results -> Back to remembered key',
      command: ['tv-remote', 'press', 'select'],
      run: (d) => {
        d.tvRemotePress('select');
        d.waitFor(/Movies|Shows|Top result/i, 'TV Search results rendered');
        for (let i = 0; i < 6; i++) d.tvRemotePress('right');
        d.waitFor(/\[focused\]/i, 'TV Search results card focused');
        d.tvRemotePress('back');
        d.waitFor(/native-tv-keyboard|Search/i, 'TV Search Back returned to keyboard');
      },
    },
  ];
}

function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    console.log(usage());
    return;
  }

  const agentDeviceBin = process.env.AGENT_DEVICE_BIN?.trim() ?? '';
  if (!options.dryRun && !agentDeviceBin) {
    throw new Error('AGENT_DEVICE_BIN environment variable is required (pass --dry-run to preview steps without a device).');
  }

  const driver = createDriver(agentDeviceBin, options.dryRun);
  const plan = buildPlan(options.mode, options.pkg);
  const executed = [];

  for (const step of plan) {
    if (options.dryRun) {
      executed.push({id: step.id, label: step.label, command: step.command, status: 'dry-run'});
    } else {
      step.run(driver);
      executed.push({id: step.id, label: step.label, status: 'pass'});
    }
  }

  const logcat = scanAndroidLogcat(process.env.ANDROID_SERIAL?.trim() ?? '', options.pkg, options.dryRun);
  const summary = {
    mode: options.mode,
    package: options.pkg,
    dryRun: options.dryRun,
    steps: executed,
    logcat,
  };
  console.log(JSON.stringify(summary, null, 2));
}

if (import.meta.url === `file://${process.argv[1]}`) {
  try {
    main();
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
