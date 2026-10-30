/** Operator-only P2-65 mixed load supervisor. UNRUN.
 *
 * Runs the direct 80/100-user load child and, after its ramp, the hardware
 * execution child in the same server window. The direct cohort and the 2-3
 * hardware identities are disjoint private manifests with separate mutation
 * stores. The supervisor fails if either child fails, if their windows do not
 * overlap, or if the hardware receipt's during-drain proof is incomplete.
 *
 * The direct child is `load.ts`; this supervisor does not edit it. A small
 * opt-in load change (documented in hardware/README.md and the private notes)
 * is required before this can run: `--observed-hardware
 * --hardware-receipt <paired-window>`. `conversionMode: 'never'` and the
 * direct-only gates remain the default for every other load run.
 *
 * No heavy execution was performed while preparing this file.
 */
import {open, constants, lstat} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {spawn} from 'node:child_process';
import {pathToFileURL} from 'node:url';
import {setTimeout as delay} from 'node:timers/promises';
import {dirname, resolve} from 'node:path';
import {validateHardwareReceipt} from './hardware/hardware-execution.ts';
import {requireDriver, targetOrigin} from './load.ts';

type Document = Record<string, unknown>;
type Identity = Record<string, unknown>;
export type Options = {
  execute: boolean; selfCheck: boolean; rehearsal: boolean;
  target?: string; manifest?: string; hardwareManifest?: string;
  mutationStore?: string; hardwareMutationStore?: string; appdata?: string;
  windowReceipt?: string; out?: string; directOut?: string; hardwareOut?: string;
  label: string; operatorWindow: string; users: number; hardwareUsers: number; seconds: number; ramp: number; warmup: number;
  quality: string; bytes: number; load: string; hardware: string;
};
const maxPrivateBytes = 4 * 1024 * 1024, maxSessions = 3, minSessions = 2;
const id = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_.:-]{1,256}$/.test(value);
const obj = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
function fault(code: string): never {throw Object.assign(new Error(code), {code});}
const failureCode = (error: unknown): string => {
  if (['AbortError', 'TimeoutError'].includes(String((error as {name?: unknown} | null)?.name))) return 'interrupted';
  const code = (error as {code?: unknown} | null)?.code;
  return typeof code === 'string' && /^[a-z0-9_]{1,80}$/.test(code) ? code : 'mixed_fixture_failed';
};
const sleep = async (ms: number) => delay(ms);

export function options(args: string[]): Options {
  const flags = new Map<string, string>();
  const boolean = new Set(['execute', 'self-check', 'rehearsal']);
  const names = new Set(['target', 'manifest', 'hardware-manifest', 'mutation-store', 'hardware-mutation-store', 'appdata',
    'window-receipt', 'out', 'direct-out', 'hardware-out', 'label', 'operator-window', 'users', 'hardware-users',
    'seconds', 'ramp-seconds', 'warmup-seconds', 'quality', 'bytes', 'load', 'hardware', ...boolean]);
  for (let i = 0; i < args.length; i++) {
    const key = args[i].replace(/^--/, '');
    if (!args[i].startsWith('--') || !names.has(key) || flags.has(key)) fault('invalid_arguments');
    const value = boolean.has(key) ? 'true' : args[++i];
    if (!value || value.startsWith('--')) fault('missing_argument');
    flags.set(key, value);
  }
  const number = (key: string, fallback: number, minimum: number, maximum: number) => {
    const value = Number(flags.get(key) ?? fallback);
    if (!Number.isSafeInteger(value) || value < minimum || value > maximum) fault('invalid_numeric_argument');
    return value;
  };
  const execute = flags.has('execute'), selfCheck = flags.has('self-check'), rehearsal = flags.has('rehearsal');
  if (execute && ('CI' in process.env || 'GITHUB_ACTIONS' in process.env)) fault('execute_forbidden_in_ci');
  if (execute && selfCheck) fault('conflicting_modes');
  if (rehearsal && !execute) fault('rehearsal_requires_execute');
  if (selfCheck && ['target', 'manifest', 'hardware-manifest', 'mutation-store', 'hardware-mutation-store', 'appdata', 'window-receipt', 'out']
    .some(name => flags.has(name))) fault('self_check_has_no_private_input');
  const users = number('users', selfCheck ? 2 : rehearsal ? 1 : 80, 1, 100);
  const hardwareUsers = number('hardware-users', 2, minSessions, maxSessions);
  const seconds = number('seconds', selfCheck ? 4 : rehearsal ? 180 : 900, 4, 7200);
  const ramp = number('ramp-seconds', selfCheck ? 1 : rehearsal ? 2 : 30, 1, 600);
  const warmup = number('warmup-seconds', selfCheck ? 1 : rehearsal ? 10 : 20, 0, 300);
  if (selfCheck) {
    if (users !== 2 || seconds !== 4) fault('unsupported_workload');
  } else if (rehearsal) {
    if (users !== 1 || seconds > 300) fault('rehearsal_bounds');
  } else if (![80, 100].includes(users)) fault('unsupported_workload');
  if (![2, 3].includes(hardwareUsers)) fault('invalid_hardware_users');
  if (!selfCheck && seconds < ramp + warmup + 60) fault('mixed_window_too_short');
  const label = flags.get('label') ?? 'baseline';
  const operatorWindow = flags.get('operator-window') ?? 'none';
  if (!['baseline', 'scheduled-backup', 'manual-backup'].includes(label)
    || !/^[A-Za-z0-9_.:-]{1,80}$/.test(operatorWindow)) fault('invalid_receipt_label');
  if (label !== 'baseline' && operatorWindow === 'none') fault('backup_requires_operator_window');
  const quality = flags.get('quality') ?? '720p-high';
  if (!['2160p', '1080p-high', '1080p-low', '720p-high', '720p-low', '480p', '240p'].includes(quality)) fault('invalid_quality');
  const bytes = number('bytes', rehearsal ? 1024 * 1024 : 2 * 1024 * 1024, 256 * 1024, 64 * 1024 * 1024);
  const out = flags.get('out');
  const directOut = flags.get('direct-out') ?? (out ? out + '.direct.json' : undefined);
  const hardwareOut = flags.get('hardware-out') ?? (out ? out + '.hardware.json' : undefined);
  return {execute, selfCheck, rehearsal, target: flags.get('target'), manifest: flags.get('manifest'),
    hardwareManifest: flags.get('hardware-manifest'), mutationStore: flags.get('mutation-store'),
    hardwareMutationStore: flags.get('hardware-mutation-store'), appdata: flags.get('appdata'),
    windowReceipt: flags.get('window-receipt'), out, directOut, hardwareOut, label, operatorWindow,
    users, hardwareUsers, seconds, ramp, warmup, quality, bytes,
    load: resolve(flags.get('load') ?? 'scripts/batch-round/load.ts'),
    hardware: resolve(flags.get('hardware') ?? 'scripts/batch-round/hardware/hardware-execution.ts')};
}

async function privateDocument(path: string): Promise<{raw: Buffer; document: Document}> {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const state = await file.stat();
    if (!state.isFile() || (state.mode & 0o777) !== 0o600 || state.uid !== process.getuid?.() || state.size > maxPrivateBytes)
      fault('private_document_requires_owned_0600_file');
    const raw = await file.readFile();
    let document: unknown;
    try {document = JSON.parse(raw.toString('utf8'));} catch {fault('private_document_invalid_json');}
    if (!obj(document)) fault('private_document_invalid_json');
    return {raw, document};
  } finally {await file.close();}
}

async function privateDirectory(path: string): Promise<void> {
  const state = await lstat(path).catch(() => fault('private_directory_missing'));
  if (!state.isDirectory() || state.isSymbolicLink() || state.uid !== process.getuid?.() || (state.mode & 0o777) !== 0o700)
    fault('private_directory_requires_owned_0700');
}

function manifestIdentities(document: Document): Identity[] {
  const out: Identity[] = [];
  for (const key of ['owner', 'quietMember']) if (obj(document[key])) out.push(document[key] as Identity);
  if (Array.isArray(document.users)) for (const user of document.users) if (obj(user)) out.push(user);
  return out;
}

/** True when any token/account/profile/device/installation/session family appears in both sets. */
export function identitiesOverlap(left: Document, right: Document): boolean {
  const first = manifestIdentities(left), second = manifestIdentities(right);
  for (const key of ['accessToken', 'accountId', 'profileId', 'deviceId', 'installationId', 'sessionFamilyId']) {
    const seen = new Set(first.map(identity => identity[key]).filter(value => typeof value === 'string'));
    if (second.some(identity => typeof identity[key] === 'string' && seen.has(identity[key] as string))) return true;
  }
  return false;
}

/** The 2-3 hardware member identities must be disjoint from the whole direct
 * cohort. The hardware admin/owner may be the same owner as the direct run,
 * because the server has one owner account; reusing a direct member fails. */
export function mixedCohortsOverlap(direct: Document, hardware: Document): boolean {
  if (identitiesOverlap({users: hardware.users}, direct)) return true;
  return identitiesOverlap({owner: hardware.owner}, {quietMember: direct.quietMember, users: direct.users});
}

function manifestShape(document: Document, expectedTarget: string, rehearsal: boolean, users: number): void {
  if (document.version !== 1 || !id(document.serverId) || !Array.isArray(document.users) || document.users.length < users)
    fault('manifest_state_or_user_gap');
  if (targetOrigin(String(document.target ?? ''), rehearsal) !== expectedTarget) fault('manifest_target_mismatch');
  if (!obj(document.owner)) fault('manifest_owner_gap');
}

function hash(raw: Buffer): string {return createHash('sha256').update(raw).digest('hex');}

function gateDocument(target: string, serverId: string, options: Options, directRaw: Buffer, hardwareRaw: Buffer): Document {
  return {kind: 'p2-65-paired-window', version: 1, target, serverId,
    startedAt: new Date().toISOString(), endsAt: new Date(Date.now() + (options.ramp + options.seconds + 180) * 1000).toISOString(),
    directUsers: options.users, hardwareUsers: options.hardwareUsers, label: options.label, operatorWindow: options.operatorWindow,
    directManifestSha256: hash(directRaw), hardwareManifestSha256: hash(hardwareRaw), nonce: createHash('sha256').update(directRaw).update(hardwareRaw).digest('hex').slice(0, 32)};
}

function directReceipt(raw: unknown, options: Options): Document {
  if (!obj(raw) || raw.passed !== true || raw.requestedUsers !== options.users || raw.achievedUsers !== options.users)
    fault('direct_receipt_not_passed');
  const gates = obj(raw.gates) ? raw.gates : {};
  for (const gate of ['users', 'mediaBytes', 'cleanup', 'mutationCustody', 'readback', 'noErrorsOrRefusals']) {
    if (gates[gate] !== true) fault('direct_receipt_gate_' + gate);
  }
  if (typeof raw.startedAt !== 'string' || !Number.isFinite(Date.parse(raw.startedAt))
    || typeof raw.elapsedSeconds !== 'number' || !Number.isFinite(raw.elapsedSeconds)
    || raw.elapsedSeconds < 0 || raw.elapsedSeconds > 86_400)
    fault('direct_receipt_window_invalid');
  return raw;
}

type ChildRun = {child: ReturnType<typeof spawn>; done: boolean; code: number | null; signal: NodeJS.Signals | null; startedAt: number; terminate: () => Promise<void>};
function startChild(label: string, executable: string, args: string[]): ChildRun {
  let child: ReturnType<typeof spawn>;
  try {child = spawn(executable, args, {stdio: 'ignore', env: process.env});}
  catch {fault(label + '_child_spawn_failed');}
  const run: ChildRun = {child, done: false, code: null, signal: null, startedAt: Date.now(), terminate: async () => {}};
  child.once('error', () => {run.done = true; run.code = -1;});
  child.once('exit', (code, signal) => {run.done = true; run.code = code; run.signal = signal;});
  run.terminate = async () => {
    if (run.done) return;
    child.kill('SIGTERM');
    const deadline = Date.now() + 10_000;
    while (!run.done && Date.now() < deadline) await sleep(200);
    if (!run.done) {
      child.kill('SIGKILL');
      const hard = Date.now() + 5000;
      while (!run.done && Date.now() < hard) await sleep(200);
    }
  };
  return run;
}

async function waitForBoth(load: ChildRun, hardware: ChildRun, deadline: number, interrupted: () => boolean): Promise<void> {
  while (!(load.done && hardware.done)) {
    if (interrupted()) fault('interrupted');
    if (load.done && load.code !== 0) fault('direct_child_failed');
    if (load.done && !hardware.done) fault('direct_window_ended_early');
    if (hardware.done && hardware.code !== 0) fault('hardware_child_failed');
    if (Date.now() > deadline) fault('mixed_window_timeout');
    await sleep(500);
  }
  if (load.code !== 0) fault('direct_child_failed');
  if (hardware.code !== 0) fault('hardware_child_failed');
}

async function run(options: Options, target: string): Promise<Document> {
  for (const path of [options.manifest, options.hardwareManifest, options.mutationStore, options.hardwareMutationStore,
    options.appdata, options.windowReceipt, options.out, options.directOut, options.hardwareOut]) {
    if (!path) fault('execute_requires_manifest_target_out_mutation_store');
  }
  const direct = await privateDocument(options.manifest!);
  const hardware = await privateDocument(options.hardwareManifest!);
  manifestShape(direct.document, target, options.rehearsal, options.users);
  manifestShape(hardware.document, target, options.rehearsal, options.hardwareUsers);
  if (direct.document.conversionMode !== 'observed-hardware') fault('direct_manifest_requires_observed_hardware');
  if (direct.document.serverId !== hardware.document.serverId) fault('mixed_server_identity_mismatch');
  if (mixedCohortsOverlap(direct.document, hardware.document)) fault('mixed_identities_overlap');
  if (resolve(options.mutationStore!) === resolve(options.hardwareMutationStore!)) fault('mixed_mutation_stores_must_differ');
  await privateDirectory(options.mutationStore!);
  await privateDirectory(options.hardwareMutationStore!);
  await privateDirectory(dirname(resolve(options.windowReceipt!)));

  const hardwareSeconds = Math.min(90, options.seconds - options.warmup - 20);
  if (hardwareSeconds < 20) fault('mixed_window_too_short');
  const loadArgs = ['--experimental-transform-types', options.load, '--execute', '--users', String(options.users),
    '--seconds', String(options.seconds), '--ramp-seconds', String(options.ramp), '--target', target,
    '--manifest', options.manifest!, '--mutation-store', options.mutationStore!, '--out', options.directOut!,
    '--hardware-users', String(options.hardwareUsers), '--appdata', options.appdata!,
    '--label', options.label, '--operator-window', options.operatorWindow,
    '--observed-hardware', '--hardware-receipt', options.windowReceipt!];
  const hardwareArgs = ['--experimental-transform-types', options.hardware, '--execute', '--sessions', String(options.hardwareUsers),
    '--direct-users', String(options.users), '--quality', options.quality, '--bytes', String(options.bytes),
    '--seconds', String(hardwareSeconds), '--target', target, '--manifest', options.hardwareManifest!,
    '--mutation-store', options.hardwareMutationStore!, '--out', options.hardwareOut!, '--label', options.label];
  if (options.rehearsal) {loadArgs.push('--rehearsal'); hardwareArgs.push('--rehearsal');}

  // Reserve the supervisor receipt and write the paired-window gate before
  // either child starts. Never overwrite previous evidence.
  for (const [path, code] of [[options.out!, 'receipt_already_exists'], [options.windowReceipt!, 'window_receipt_already_exists']] as const) {
    if (await lstat(path).then(() => true, () => false)) fault(code);
  }
  let out;
  try {out = await open(options.out!, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);}
  catch {fault('receipt_already_exists');}
  const gate = gateDocument(target, String(direct.document.serverId), options, direct.raw, hardware.raw);
  let gateFile;
  try {gateFile = await open(options.windowReceipt!, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);}
  catch {await out.close(); fault('window_receipt_already_exists');}
  await gateFile.writeFile(JSON.stringify(gate) + '\n'); await gateFile.close();

  let load: ChildRun | undefined, hardwareRun: ChildRun | undefined;
  let result: Document | undefined;
  let failure: unknown;
  let interrupted = false;
  const stop = () => {interrupted = true;};
  process.once('SIGINT', stop);
  process.once('SIGTERM', stop);
  const startedAt = new Date().toISOString();
  try {
    load = startChild('direct', process.execPath, loadArgs);
    await sleep((options.ramp + options.warmup) * 1000);
    if (interrupted) fault('interrupted');
    if (load.done) fault('direct_child_exited_before_hardware');
    hardwareRun = startChild('hardware', process.execPath, hardwareArgs);
    await waitForBoth(load, hardwareRun, load.startedAt + (options.ramp + options.seconds + 180) * 1000, () => interrupted);
    const directRaw = await privateDocument(options.directOut!);
    const hardwareRaw = await privateDocument(options.hardwareOut!);
    const directReceiptValue = directReceipt(directRaw.document, options);
    const hardwareReceipt = validateHardwareReceipt(hardwareRaw.document, {target, serverId: String(direct.document.serverId),
      sessions: options.hardwareUsers, directUsers: options.users});
    const loadStart = Date.parse(directReceiptValue.startedAt as string);
    const loadEnd = loadStart + Number(directReceiptValue.elapsedSeconds) * 1000;
    const hwStart = Date.parse(hardwareReceipt.startedAt), hwEnd = Date.parse(hardwareReceipt.finishedAt);
    if (!(hwStart >= loadStart && hwEnd <= loadEnd + 5000 && hwEnd > hwStart && loadEnd - hwStart >= 1000))
      fault('mixed_windows_do_not_overlap');
    result = {kind: 'p2-65-mixed-load', version: 1, mode: 'execute', rehearsal: options.rehearsal,
      label: options.label, operatorWindow: options.operatorWindow, target, serverId: direct.document.serverId,
      directUsers: options.users, hardwareUsers: options.hardwareUsers,
      window: {loadStartedAt: directReceiptValue.startedAt, loadFinishedAt: new Date(loadEnd).toISOString(),
        hardwareStartedAt: hardwareReceipt.startedAt, hardwareFinishedAt: hardwareReceipt.finishedAt,
        firstOverlapAt: hardwareReceipt.observed.firstOverlapAt, lastOverlapAt: hardwareReceipt.observed.lastOverlapAt},
      manifests: {directSha256: hash(direct.raw), hardwareSha256: hash(hardware.raw), identitiesDisjoint: true},
      mutationStores: {distinct: true}, gate: {kind: gate.kind, sha256: hash(Buffer.from(JSON.stringify(gate) + '\n'))},
      children: {load: {exit: load.code, durationMs: Date.now() - load.startedAt},
        hardware: {exit: hardwareRun.code, durationMs: Date.now() - hardwareRun.startedAt}},
      direct: {requestedUsers: directReceiptValue.requestedUsers, achievedUsers: directReceiptValue.achievedUsers,
        receiptSha256: hash(directRaw.raw)},
      hardware: {receiptSha256: hash(hardwareRaw.raw), sessions: hardwareReceipt.requestedSessions,
        directObserved: hardwareReceipt.observed.maxDirectSessions, directAccounts: hardwareReceipt.observed.maxDirectAccounts,
        overlapSamples: hardwareReceipt.observed.overlapSamples, maxActiveConversions: hardwareReceipt.observed.maxActiveConversions,
        firstOverlapAt: hardwareReceipt.observed.firstOverlapAt, lastOverlapAt: hardwareReceipt.observed.lastOverlapAt},
      startedAt, finishedAt: new Date().toISOString(), verdict: 'pass', failures: []};
  } catch (error) {
    failure = error;
    result = {kind: 'p2-65-mixed-load', version: 1, mode: 'execute', rehearsal: options.rehearsal,
      label: options.label, operatorWindow: options.operatorWindow, target, serverId: direct.document.serverId,
      directUsers: options.users, hardwareUsers: options.hardwareUsers, startedAt, finishedAt: new Date().toISOString(),
      verdict: 'fail', failures: [{session: '', code: failureCode(error)}]};
  } finally {
    process.removeListener('SIGINT', stop);
    process.removeListener('SIGTERM', stop);
    await load?.terminate();
    await hardwareRun?.terminate();
    await out.writeFile(JSON.stringify(result!, null, 2) + '\n'); await out.close();
  }
  if (failure) throw failure;
  return result!;
}

/** Offline checks: cohort separation and the supervisor's own options. */
export async function selfCheck(): Promise<Document> {
  const checks: {check: string; ok: boolean}[] = [];
  const check = (name: string, ok: boolean) => checks.push({check: name, ok: Boolean(ok)});
  const one = {accessToken: 'a', accountId: 'account-1', profileId: 'profile-1', deviceId: 'device-1', installationId: 'installation-1'};
  const two = {accessToken: 'b', accountId: 'account-2', profileId: 'profile-2', deviceId: 'device-2', installationId: 'installation-2'};
  check('disjoint cohorts pass', !identitiesOverlap({users: [one]}, {users: [two]}));
  check('a shared account fails', identitiesOverlap({users: [one]}, {users: [{...two, accountId: 'account-1'}]}));
  check('a shared token fails', identitiesOverlap({owner: one}, {users: [{...two, accessToken: 'a'}]}));
  const owner = {...one, accountId: 'owner', accessToken: 'owner'};
  const three = {accessToken: 'c', accountId: 'hardware-1', profileId: 'profile-3', deviceId: 'device-3',
    installationId: 'installation-3', sessionFamilyId: 'family-3'};
  check('a shared owner is allowed', !mixedCohortsOverlap({owner, quietMember: two, users: [two]},
    {owner, users: [three]}));
  check('reusing a direct member is refused', mixedCohortsOverlap({owner, quietMember: two, users: [two]},
    {owner, users: [two]}));
  const parsed = options(['--self-check']);
  check('self-check needs no private input', parsed.selfCheck && !parsed.manifest);
  check('self-check remains offline', parsed.execute === false);
  const failures = checks.filter(entry => !entry.ok).map(entry => entry.check);
  return {mode: 'self-check', scope: 'offline cohort/options checks only; no child, server or route was exercised',
    ok: !failures.length, checks: checks.length, failures};
}

export async function main(args = process.argv.slice(2)): Promise<void> {
  const optionsValue = options(args);
  if (optionsValue.selfCheck) {
    if (process.versions.node !== '24.21.0') fault('node_24_21_0_required');
    const report = await selfCheck();
    console.log(JSON.stringify(report, null, 2));
    if (!report.ok) process.exitCode = 1;
    return;
  }
  if (!optionsValue.execute) {
    console.log(JSON.stringify({mode: 'plan', state: 'UNRUN', users: optionsValue.users, hardwareUsers: optionsValue.hardwareUsers,
      seconds: optionsValue.seconds, rampSeconds: optionsValue.ramp, warmupSeconds: optionsValue.warmup,
      target: optionsValue.rehearsal ? '127.0.0.1 (loopback rehearsal)' : '100.120.50.24',
      requirements: ['explicit --execute', 'direct manifest with conversionMode observed-hardware',
        'load opt-in --observed-hardware + paired hardware-receipt gate', 'disjoint 2-3 hardware identities',
        'separate owned 0700 mutation stores', 'owned --appdata readback', 'quiet GPU server'],
      children: ['load.ts direct cohort (after its ramp, hardware starts)', 'hardware-execution.ts 2-3 conversions'],
      nonClaims: ['no heavy execution in preparation', 'load stays direct-only outside the paired opt-in']}));
    return;
  }
  if (process.versions.node !== '24.21.0') fault('node_24_21_0_required');
  if (!optionsValue.target) fault('execute_requires_target');
  const target = targetOrigin(optionsValue.target, optionsValue.rehearsal);
  requireDriver({selfCheck: false, rehearsal: optionsValue.rehearsal, target});
  await run(optionsValue, target);
  console.log(JSON.stringify({mode: 'execute', verdict: 'pass', users: optionsValue.users, hardwareUsers: optionsValue.hardwareUsers}));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    console.error('mixed load supervisor refused: ' + failureCode(error) + ' (see hardware/README.md)');
    process.exitCode = 1;
  });
}
