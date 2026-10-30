/** Operator-only, UNRUN load driver. See load-README.md. Node 24.21.0, no install/build. */
import {hostname, networkInterfaces} from 'node:os';
import {open, constants} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import {setTimeout as delay} from 'node:timers/promises';
import {randomUUID, createHash} from 'node:crypto';
import {type LocalSession} from '../../packages/client-core/src/index.ts';
import {LocalSessionRefresher} from '../../packages/client-core/src/session-refresh.ts';
import {join} from 'node:path';
import {FileMutationStore, lockMutationDirectory, mutationClient, savedBatch, finalStop, FileConnectionStore,
  privateStoreDirectory, seedConnection, refreshBroker, readServerState, readPrepReceipt, type ServerStateReadback,
  type PrepReceipt} from './load-adapters.ts';
import {readBrowsePage, releaseBrowseSelection, readReadingFacet} from '../../packages/client-core/src/browse-preparation.ts';
import {validInstallationId} from '../../packages/client-core/src/installation.ts';
import {capabilityDocumentProblem, type CapabilityDocument} from '../../packages/client-core/src/client-profile.ts';
import {BrowseQueryBuilder, browseFacetsPath, browseCapabilitiesPath, parseBrowseCapabilities} from '../../packages/client-core/src/browse.ts';
import {SearchService, searchPath} from '../../packages/client-core/src/search.ts';
import {HomeService} from '../../packages/client-core/src/home-service.ts';
import {LibraryManagementService} from '../../packages/client-core/src/library-management.ts';
import {ObservationReporter} from '../../packages/client-core/src/playback-v1/observation.ts';
import {CapabilityPublisher} from '../../packages/client-core/src/playback-v1/options.ts';
import {EventsClient} from '../../packages/client-core/src/playback-v1/events.ts';
import {readEventStream} from '../../packages/client-core/src/sse.ts';
import {routePath} from '../../packages/contracts/src/route-paths.ts';

type Identity = {accessToken: string; expiresAt: string; sessionFamilyId: string; tokenGeneration: string; authorizationHorizon: string; authority: 'local' | 'hosted'; accountId: string;
  profileId: string; deviceId: string; installationId: string; role: 'owner' | 'member'; capabilities: CapabilityDocument;
  /** Rotating credential and exact server identity from the real sign-in; required when the access token cannot cover the window. */
  refreshToken?: string; serverIdentity?: {publicKey: string; fingerprint: string}};
type User = Identity & {libraryId: string; pivot: string; search: string; itemIds: string[];
  bytesPerSecond: number; directVerifiedAt: string; reading: {libraryId: string; pivot: string; facet: string}};
export type Manifest = {version: 1; target: string; serverId: string; conversionMode: 'never' | 'observed-hardware';
  stateAdapterReceipt: string; owner: Identity & {libraryId: string}; quietMember: Identity; users: User[]};
export type Options = {execute: boolean; selfCheck: boolean; rehearsal: boolean; ingest: boolean; rotateSessions: boolean; phases: boolean; control?: string;
  observedHardware: boolean; hardwareReceipt?: string;
  trendP50Percent: number; trendP50Ms: number; trendP99Percent: number; trendP99Ms: number;
  minPhaseSamples: number; requiredPhases: string[];
  itemMarkers: number[]; users: number; seconds: number; ramp: number;
  target?: string; manifest?: string; out?: string; mutationStore?: string; appdata?: string; label: string; window: string; hardwareUsers: number;
  readP99: number; writeP99: number; observationGap: number};
function fault(code: string): never {throw Object.assign(new Error(code), {code});}
const id = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_.:-]{1,256}$/.test(v);
const now = () => performance.now();
const sleep = async (ms: number, signal: AbortSignal) => {await delay(Math.max(1, ms), undefined, {signal});};
const aborted = (e: unknown) => ['AbortError', 'TimeoutError'].includes(String((e as any)?.name));

export function options(args: string[]): Options {
  const flags = new Map<string, string>();
  const boolean = new Set(['execute', 'dry-run', 'self-check', 'rehearsal', 'rotate-sessions', 'phases', 'observed-hardware', 'ingest']);
  const names = new Set(['users', 'seconds', 'ramp-seconds', 'target', 'manifest', 'out', 'mutation-store', 'appdata', 'label', 'operator-window',
    'hardware-users', 'hardware-receipt', 'read-p99-ms', 'write-p99-ms', 'observation-gap-ms', 'control', 'item-markers',
    'trend-p50-percent', 'trend-p50-ms', 'trend-p99-percent', 'trend-p99-ms', 'min-phase-samples', 'require-phases', ...boolean]);
  for (let i = 0; i < args.length; i++) {
    const key = args[i].replace(/^--/, '');
    if (!args[i].startsWith('--') || !names.has(key) || flags.has(key)) fault('invalid_arguments');
    const value = boolean.has(key) ? 'true' : args[++i];
    if (!value || value.startsWith('--')) fault('missing_argument');
    flags.set(key, value);
  }
  const num = (key: string, fallback: number, min: number, max: number) => {
    const value = Number(flags.get(key) ?? fallback);
    if (!Number.isSafeInteger(value) || value < min || value > max) fault('invalid_numeric_argument');
    return value;
  };
  const selfCheck = flags.has('self-check'), execute = flags.has('execute'), rehearsal = flags.has('rehearsal');
  const observedHardware = flags.has('observed-hardware'), ingest = flags.has('ingest');
  // Presence, including CI=false, is deliberately sufficient. No override exists.
  if (execute && ('CI' in process.env || 'GITHUB_ACTIONS' in process.env)) fault('execute_forbidden_in_ci');
  if (execute && (selfCheck || flags.has('dry-run'))) fault('conflicting_modes');
  if (rehearsal && (selfCheck || !execute)) fault('rehearsal_requires_execute');
  if (selfCheck && (flags.has('manifest') || flags.has('target') || flags.has('mutation-store') || flags.has('appdata') || flags.has('control'))) fault('self_check_has_no_private_input');
  const users = num('users', selfCheck ? 2 : rehearsal ? 1 : 80, 1, 100), seconds = num('seconds', selfCheck ? 4 : rehearsal ? 60 : 600, 4, 7200);
  if (selfCheck) {if (users !== 2 || seconds !== 4) fault('unsupported_workload');}
  else if (rehearsal) {if (users > 3 || seconds > 300) fault('rehearsal_bounds');}
  else if (ingest) {if (users < 1 || users > 20) fault('ingest_bounds_one_to_twenty');}
  else if (![2, 80, 100].includes(users) || users === 2 && seconds !== 4) fault('unsupported_workload');
  const label = flags.get('label') ?? 'baseline', window = flags.get('operator-window') ?? 'none';
  if (!['baseline', 'scheduled-backup', 'manual-backup'].includes(label) || !/^[A-Za-z0-9_.:-]{1,80}$/.test(window)) fault('invalid_receipt_label');
  if (label !== 'baseline' && window === 'none') fault('backup_requires_operator_window');
  // An explicit paired window: the mixed supervisor authorizes it with a private gate file and its
  // hardware receipt is the proof. It never becomes the default and never implies software fallback.
  if (observedHardware && (!execute || selfCheck)) fault('observed_hardware_requires_execute');
  if (observedHardware && !flags.get('hardware-receipt')) fault('observed_hardware_requires_receipt');
  if (observedHardware && flags.has('dry-run')) fault('conflicting_modes');
  // Ingest runs alongside a growing catalogue: real users, but never a capacity claim.
  if (ingest && (!execute || selfCheck)) fault('ingest_requires_execute');
  if (ingest && rehearsal) fault('conflicting_modes');
  const phases = flags.has('phases');
  if (phases && execute && !flags.get('control')) fault('phases_require_control_file');
  const markers = (flags.get('item-markers') ?? '50000,150000,300000').split(',').map(Number);
  if (!markers.length || markers.some(m => !Number.isSafeInteger(m) || m < 1) || new Set(markers).size !== markers.length) fault('invalid_item_markers');
  if (ingest && !flags.get('control')) fault('ingest_requires_marker_file');
  if (!(flags.get('require-phases') ?? 'baseline,during,after').split(',').every(value => /^[A-Za-z0-9_.:-]{1,32}$/.test(value))) fault('invalid_required_phases');
  return {execute, selfCheck, rehearsal, ingest, observedHardware, hardwareReceipt: flags.get('hardware-receipt'),
    rotateSessions: rehearsal || flags.has('rotate-sessions'), phases: phases || ingest,
    control: flags.get('control'), itemMarkers: markers.sort((a, b) => a - b), users, seconds,
    ramp: num('ramp-seconds', selfCheck ? 1 : rehearsal ? 2 : 30, 1, 600),
    target: flags.get('target'), manifest: flags.get('manifest'), out: flags.get('out'), mutationStore: flags.get('mutation-store'),
    appdata: flags.get('appdata'), label, window,
    hardwareUsers: num('hardware-users', 1, 1, 3), readP99: num('read-p99-ms', 2000, 10, 60000),
    trendP50Percent: num('trend-p50-percent', 20, 0, 1000), trendP50Ms: num('trend-p50-ms', 10, 0, 60000),
    trendP99Percent: num('trend-p99-percent', 20, 0, 1000), trendP99Ms: num('trend-p99-ms', 50, 0, 60000),
    minPhaseSamples: num('min-phase-samples', 20, 1, 100000),
    // Ingest runs the six 50k buckets the runner writes; a campaign run keeps baseline/during/after.
    requiredPhases: (flags.get('require-phases') ?? (ingest
      ? 'baseline,phase-1,phase-2,phase-3,phase-4,phase-5,phase-6,after'
      : 'baseline,during,after')).split(',').map(v => v.trim()).filter(Boolean),
    writeP99: num('write-p99-ms', 2000, 10, 60000), observationGap: num('observation-gap-ms', 2500, 1500, 10000)};
}

export function requireDriver(o: Pick<Options, 'selfCheck' | 'rehearsal' | 'target'> & {ingest?: boolean}) {
  const host = hostname().toLowerCase();
  const addresses = Object.values(networkInterfaces()).flat().map(x => x?.address);
  // Loopback-only modes never touch another host: the offline check and the tiny 127.0.0.1 rehearsal
  // may run on mini-1. Every measured 80/100-user campaign stays on mini-2 or the runner.
  if (o.selfCheck || o.rehearsal || o.ingest && o.target?.startsWith('http://127.0.0.1:')) {
    if (o.rehearsal || o.ingest) loopbackTarget(o.target ?? '');
    return;
  }
  if (/mini[-_]?1|ehler-personal/.test(host) || addresses.includes('100.120.50.24')) fault('driver_forbidden_on_mini1');
  if (!addresses.includes('100.113.197.98') && !(host === 'portico-runner' && !o.selfCheck)) fault('driver_requires_mini2_or_runner');
}
/** The rehearsal's only target is a loopback listener the operator started on this machine. */
export function loopbackTarget(value: string) {
  let u: URL; try {u = new URL(value);} catch {fault('invalid_target');}
  if (u.protocol !== 'http:' || u.hostname !== '127.0.0.1' || u.username || u.password
    || u.pathname !== '/' || u.search || u.hash || Number(u.port) < 1024) fault('rehearsal_target_must_be_loopback');
  return u.origin;
}
export function targetOrigin(value: string, rehearsal = false, ingest = false) {
  if (rehearsal || ingest && value.startsWith('http://127.0.0.1:')) return loopbackTarget(value);
  let u: URL; try {u = new URL(value);} catch {fault('invalid_target');}
  if (!['http:', 'https:'].includes(u!.protocol) || u!.hostname !== '100.120.50.24' || u!.username || u!.password
    || u!.pathname !== '/' || u!.search || u!.hash) fault('target_not_allowlisted');
  return u!.origin;
}
export function validateManifest(m: Manifest, o: Options) {
  // The default stays direct-only. `observed-hardware` is accepted only when the caller asked for
  // the paired window; nothing else widens what a session plan may contain.
  const modeAllowed = o.observedHardware ? m?.conversionMode === 'observed-hardware' : m?.conversionMode === 'never';
  if (m?.version !== 1 || !modeAllowed || !id(m.serverId) || !id(m.stateAdapterReceipt)
    || !Array.isArray(m.users) || m.users.length < o.users || m.users.length > 100) fault('manifest_state_or_user_gap');
  if (targetOrigin(m.target, o.rehearsal, o.ingest) !== targetOrigin(o.target ?? '', o.rehearsal, o.ingest)) fault('manifest_target_mismatch');
  const windowEnd = Date.now() + (o.ramp + o.seconds + 90) * 1000;
  const all = [m.owner, m.quietMember, ...m.users];
  for (const p of all) {
    if (!p) fault('manifest_identity_or_capability_gap');
    const refresh = refreshMaterialProblem(p);
    if (!['local', 'hosted'].includes(p.authority) || !['owner', 'member'].includes(p.role)
      || ![p.accountId, p.profileId, p.deviceId, p.sessionFamilyId, p.tokenGeneration].every(id) || !validInstallationId(p.installationId)
      || typeof p.accessToken !== 'string' || !p.accessToken || /\s/.test(p.accessToken)
      || !Number.isFinite(Date.parse(p.expiresAt))
      || !Number.isFinite(Date.parse(p.authorizationHorizon)) || Date.parse(p.authorizationHorizon) < Date.parse(p.expiresAt)
      || capabilityDocumentProblem(p.capabilities) || refresh) fault(refresh ? 'manifest_refresh_credentials_gap' : 'manifest_identity_or_capability_gap');
    // A token that cannot cover the window is only usable through the real rotation path.
    if (Date.parse(p.expiresAt) < windowEnd && !(p.refreshToken && p.serverIdentity)) fault('manifest_refresh_credentials_required');
    // The rehearsal exists to exercise the real rotation adapter, so every identity must carry it.
    if (o.rehearsal && !(p.refreshToken && p.serverIdentity)) fault('rehearsal_requires_refresh_material');
  }
  for (const key of ['accessToken', 'accountId', 'profileId', 'deviceId', 'installationId', 'sessionFamilyId'] as const)
    if (new Set(all.map(p => p[key])).size !== all.length) fault('manifest_identities_must_be_distinct');
  if (new Set(all.map(p => p.refreshToken).filter(Boolean)).size !== all.filter(p => p.refreshToken).length) fault('manifest_refresh_credentials_gap');
  if (m.owner.role !== 'owner' || m.quietMember.role !== 'member' || !id(m.owner.libraryId)) fault('manifest_owner_or_quiet_gap');
  for (const u of m.users) if (!u.reading || ![u.reading.libraryId, u.reading.pivot, u.reading.facet].every(id) || u.role !== 'member' || !id(u.libraryId) || !id(u.pivot) || typeof u.search !== 'string'
    || !u.search.trim() || u.search.length > 80 || !Array.isArray(u.itemIds) || !u.itemIds.length || u.itemIds.length > 20
    || !u.itemIds.every(id) || new Set(u.itemIds).size !== u.itemIds.length || !Number.isSafeInteger(u.bytesPerSecond)
    || u.bytesPerSecond < 1024 || u.bytesPerSecond > 16 * 1024 * 1024 || !Number.isFinite(Date.parse(u.directVerifiedAt))) fault('manifest_playable_item_gap');
}
/** Both rotation fields or neither, and only the canonical shapes. */
function refreshMaterialProblem(p: Identity): boolean {
  if (p.refreshToken === undefined && p.serverIdentity === undefined) return false;
  if (typeof p.refreshToken !== 'string' || !/^[-_A-Za-z0-9]{16,512}$/.test(p.refreshToken)) return true;
  if (!p.serverIdentity || typeof p.serverIdentity.publicKey !== 'string' || typeof p.serverIdentity.fingerprint !== 'string'
    || !/^[-_A-Za-z0-9]{43}$/.test(p.serverIdentity.publicKey) || !/^[-_A-Za-z0-9]{43}$/.test(p.serverIdentity.fingerprint)) return true;
  return !validInstallationId(p.installationId);
}
async function privateManifest(path: string): Promise<Manifest> {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const s = await file.stat();
    if (!s.isFile() || (s.mode & 0o777) !== 0o600 || s.uid !== process.getuid?.() || s.size > 4 * 1024 * 1024) fault('manifest_requires_owned_0600_file');
    try {return JSON.parse(await file.readFile('utf8'));} catch {fault('manifest_invalid_json');}
  } finally {await file.close();}
}

// Fixed logarithmic buckets, including an overflow bucket. Quantiles are conservative upper bounds.
const bounds = Array.from({length: 48}, (_, i) => Math.ceil(1.3 ** i));
class Histogram {
  bins = new Array(bounds.length + 1).fill(0); count = 0; maxMs = 0;
  add(ms: number) {this.count++; this.maxMs = Math.max(this.maxMs, ms); const i = bounds.findIndex(b => ms <= b); this.bins[i < 0 ? bounds.length : i]++;}
  percentile(fraction: number) {let n = 0; for (let i = 0; i < this.bins.length; i++) if ((n += this.bins[i]) >= Math.ceil(this.count * fraction)) return bounds[i] ?? this.maxMs; return 0;}
  p50() {return this.percentile(.5);}
  p99() {return this.percentile(.99);}
  receipt() {return {count: this.count, p50UpperMs: this.p50(), p95UpperMs: this.percentile(.95), p99UpperMs: this.p99(), maxMs: Math.ceil(this.maxMs), bins: this.bins};}
}
export class Metrics {
  ops: Record<string, {attempts: number; ok: number; errors: number; refusals: number; cancelled: number; latency: Histogram; codes: Record<string, number>}> = {};
  /** Optional observation-window scoping for orchestrated runs (ingest baseline/during/after). */
  phase: string | undefined;
  readonly phaseOps: Record<string, Record<string, {attempts: number; ok: number; errors: number; refusals: number; cancelled: number; latency: Histogram; codes: Record<string, number>}>> = {};
  /** Monotonic values stay internal; the epoch ISO forms are what a runner can join against. */
  readonly phaseTimes: Record<string, {startedAt: number; endedAt?: number; startedAtEpoch: string; endedAtEpoch?: string}> = {};
  counters: Record<string, number> = {};
  beginPhase(name: string) {
    if (this.phase === name) return;
    const previous = this.phase && this.phaseTimes[this.phase];
    if (previous && previous.endedAt === undefined) {previous.endedAt = now(); previous.endedAtEpoch = new Date().toISOString();}
    this.phase = name;
    this.phaseTimes[name] ??= {startedAt: now(), startedAtEpoch: new Date().toISOString()};
  }
  private bucket(name: string) {
    if (!this.phase) return this.ops[name] ??= {attempts: 0, ok: 0, errors: 0, refusals: 0, cancelled: 0, latency: new Histogram(), codes: {}};
    const scoped = this.phaseOps[this.phase] ??= {};
    return scoped[name] ??= {attempts: 0, ok: 0, errors: 0, refusals: 0, cancelled: 0, latency: new Histogram(), codes: {}};
  }
  phaseReceipt() {
    return Object.fromEntries(Object.entries(this.phaseOps).map(([phase, ops]) => [phase, {
      startedAtEpoch: this.phaseTimes[phase]?.startedAtEpoch, endedAtEpoch: this.phaseTimes[phase]?.endedAtEpoch ?? null,
      startedAtMonotonic: this.phaseTimes[phase]?.startedAt, endedAtMonotonic: this.phaseTimes[phase]?.endedAt ?? null,
      operations: Object.fromEntries(Object.entries(ops).map(([k, v]) => [k, {...v, latency: v.latency.receipt()}]))}]));
  }
  inc(k: string, n = 1) {this.counters[k] = (this.counters[k] ?? 0) + n;}
  /** `cancelIf` classifies work that failed *because* its own lifetime ended: a request in flight
   * when the window closes is a cancellation, never a load error. */
  async measure<T>(name: string, fn: () => Promise<T>, cancelIf?: () => boolean): Promise<T> {
    const s = this.bucket(name), global = this.ops[name] ??= {attempts: 0, ok: 0, errors: 0, refusals: 0, cancelled: 0, latency: new Histogram(), codes: {}};
    const scoped = s !== global;
    const count = (apply: (target: typeof s) => void) => {apply(s); if (scoped) apply(global);};
    count(t => t.attempts++); const at = now();
    try {const value = await fn(); count(t => t.ok++); return value;}
    catch (e) {
      if (aborted(e) || cancelIf?.()) count(t => t.cancelled++); else {
        count(t => t.errors++); const status = Number((e as any)?.status);
        // The server's own code, bounded to its alphabet: no message, path, grant or body.
        const code = (e as {code?: unknown})?.code;
        if (typeof code === 'string' && /^[a-z_]{1,48}$/.test(code)) this.inc('refusalCode:' + code);
        if ([401, 403, 409, 412, 429, 503].includes(status)) count(t => t.refusals++);
        // Never echo server messages, arbitrary codes, paths, query text, grants or bearer values.
        const key = [400, 401, 403, 404, 409, 410, 412, 429, 500, 502, 503, 504].includes(status) ? String(status) : 'other';
        count(t => {t.codes[key] = (t.codes[key] ?? 0) + 1;});
      } throw e;
    } finally {count(t => t.latency.add(now() - at));}
  }
  receipt() {return Object.fromEntries(Object.entries(this.ops).map(([k, v]) => [k, {...v, latency: v.latency.receipt()}]));}
}
type UserReceipt = {index: number; bytes: number; observations: number; maxObservationGapMs: number;
  mediaStarted: boolean; mediaActive: boolean; startedAt: number; lastAck: number; expectedBytes: number; stopped: boolean};

/** Orchestration contract, two accepted shapes in one private 0600 file:
 * 1. a single JSON object `{"phase":"baseline"|"during"|"after","ingestedItems":<int>}`; or
 * 2. the ingest runner's append-only JSONL, one object per line, e.g.
 *    `{"kind":"p2-65-ingest","version":1,"event":"phase-start","phase":50000,"totalFiles":51234,...}`.
 * The driver only reads the file and never writes it. For JSONL the last line wins: a
 * `phase-start` opens phase `<n>`, a `resident-read`/`phase-start` whose `totalFiles` passes a
 * configured marker triggers the driver's own resident read once, and `fixture-end` closes the
 * window. A missing or invalid file is counted, never fatal. */
export type ControlState = {phase?: string; ingestedItems?: number; event?: string; markerItems?: number; at?: string};
export function parseControlText(text: string): ControlState | undefined {
  const trimmed = text.trim();
  if (!trimmed) return undefined;
  const lines = trimmed.split('\n').filter(line => line.trim());
  const jsonl = lines.length > 1 || (() => {try {return JSON.parse(trimmed)?.kind === 'p2-65-ingest';} catch {return false;}})();
  if (!jsonl) {
    try {
      const parsed = JSON.parse(trimmed);
      if (!parsed || typeof parsed !== 'object') return undefined;
      const state: ControlState = {};
      // Bounded phase vocabulary of the final atomic contract. `during` stays accepted for older
      // single-window callers; nothing outside this set is a phase.
      const phase = String(parsed.phase ?? '');
      if (/^(?:baseline|after|during|phase-[1-6])$/.test(phase)) state.phase = phase;
      const accepted = Number.isSafeInteger(parsed.ingestedItems) && parsed.ingestedItems >= 0 ? parsed.ingestedItems : undefined;
      if (accepted !== undefined) state.ingestedItems = accepted;
      const events = ['phase-start', 'phase-end', 'resident-read', 'fixture-end'];
      if (typeof parsed.event === 'string' && events.includes(parsed.event)) {
        state.event = parsed.event;
        // A resident read is the marker event; the final event closes the window. `ingestedItems` is
        // the API-read-back accepted count, never a file-intent count.
        if (parsed.event === 'resident-read' || parsed.event === 'fixture-end') state.markerItems = accepted;
      }
      if (typeof parsed.at === 'string' && !Number.isNaN(Date.parse(parsed.at))) state.at = parsed.at;
      return state;
    } catch {return undefined;}
  }
  for (let index = lines.length - 1; index >= 0; index--) {
    let entry: any;
    try {entry = JSON.parse(lines[index]);} catch {continue;}
    if (!entry || typeof entry !== 'object') continue;
    // Markers come from the API-read back ACCEPTED count, never the generator's totalFiles: a
    // generator can have written files the server has not accepted yet.
    const accepted = Number.isSafeInteger(entry.accepted) && entry.accepted >= 0 ? entry.accepted : undefined;
    const total = Number.isSafeInteger(entry.totalFiles) && entry.totalFiles >= 0 ? entry.totalFiles : undefined;
    if (entry.event === 'phase-start' && Number.isSafeInteger(entry.phase)) return {phase: 'phase-' + entry.phase, ingestedItems: accepted ?? total, event: 'phase-start', markerItems: accepted};
    // A phase-end closes the window of the phase it ends; it must not fall back to the previous start.
    if (entry.event === 'phase-end' && Number.isSafeInteger(entry.phase)) return {phase: 'phase-' + entry.phase, ingestedItems: accepted ?? total, event: 'phase-end', markerItems: accepted};
    if (entry.event === 'resident-read') return {phase: 'phase-' + entry.phase, ingestedItems: accepted ?? total, event: 'resident-read', markerItems: accepted};
    if (entry.event === 'fixture-end') return {phase: 'after', ingestedItems: accepted ?? total, event: 'fixture-end', markerItems: accepted};
  }
  return undefined;
}
export function phaseWindows(controlPath: string, markers: readonly number[], onMarker: (marker: number, items: number) => void,
  onPhase: (phase: string) => void, pollMs = 250) {
  let stopped = false, errors = 0;
  const pending = new Set<number>();
  const read = async (): Promise<ControlState | undefined> => {
    let file;
    try {file = await open(controlPath, constants.O_RDONLY | constants.O_NOFOLLOW);} catch {return undefined;}
    try {
      const stat = await file.stat();
      if (!stat.isFile() || stat.uid !== process.getuid?.() || (stat.mode & 0o777) !== 0o600 || stat.size > 4096) return undefined;
      return parseControlText(await file.readFile('utf8'));
    } catch {return undefined;} finally {await file.close();}
  };
  const tick = async () => {
    if (stopped) return;
    const state = await read();
    if (!state) {errors++; return;}
    if (state.phase) onPhase(state.phase);
    // A resident-read/fixture-end marker fires once per accepted count; repeated polls are deduped
    // by the pending set, so one marker read is never issued twice.
    if ((state.event === 'resident-read' || state.event === 'fixture-end') && state.markerItems !== undefined) {
      if (markers.includes(state.markerItems) && !pending.has(state.markerItems)) {pending.add(state.markerItems); onMarker(state.markerItems, state.markerItems);}
    }

  };
  const timer = setInterval(() => {void tick();}, pollMs);
  void tick();
  return {stop() {stopped = true; clearInterval(timer);}, get errors() {return errors;}};
}

export async function run(m: Manifest, o: Options, transport: typeof fetch = globalThis.fetch) {
  const metrics = new Metrics(), controller = new AbortController(), signal = controller.signal;
  const started = now(), wallStarted = new Date().toISOString(), end = started + (o.ramp + o.seconds) * 1000;
  let interrupted = false, plateauUsers = 0, stateReadback: ServerStateReadback | undefined, prepReceipt: PrepReceipt | undefined;
  const markerReads: {marker: number; items: number; at: string}[] = [];
  let markerControl: ReturnType<typeof phaseWindows> | undefined, markerErrors = 0;
  let plateauMethod = 'fixed-post-ramp';
  const interrupt = () => {interrupted = true; controller.abort();};
  process.once('SIGINT', interrupt); process.once('SIGTERM', interrupt);
  const deadline = setTimeout(() => controller.abort(), end - now());
  const receipts: UserReceipt[] = [], retired: (() => void)[] = [], streams = new Set<Promise<unknown>>();
  const scopedFetch = (p: Identity): typeof fetch => async (input, init = {}) => {
    const url = new URL(String(input));
    if (url.origin !== m.target || url.username || url.password) fault('cross_origin_request_refused');
    const method = init.method ?? 'GET';
    const headers = new Headers(init.headers);
    // Installation globals are app-wide: inject the captured installation per transport, never swap globals.
    if (method === 'POST' && /\/v1\/(?:auth|direct)\//.test(url.pathname)) headers.set('X-Portico-Installation-Id', p.installationId);
    const response = await transport(input, {...init, headers, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer'});
    metrics.inc('wireRequests');
    if (!response.ok) metrics.inc('wireHTTP' + (response.status >= 400 && response.status <= 599 ? response.status : 'Other'));
    return response;
  };
  const clients: (Awaited<ReturnType<typeof mutationClient>> & {store: FileMutationStore; token: () => string;
    refresher?: LocalSessionRefresher; connection?: FileConnectionStore})[] = [];
  const refreshedAccounts = new Set<string>();
  let refreshRequired = 0, refreshFailures = 0;
  const refresherOptions = (p: Identity) => ({target: m.target, directory: o.mutationStore!, serverId: m.serverId,
    family: p.sessionFamilyId, installationId: p.installationId});
  const client = async (p: Identity) => {
    if (!o.mutationStore) fault('persistent_mutation_store_required');
    const session: LocalSession = {accessToken: p.accessToken, expiresAt: p.expiresAt, sessionFamilyId: p.sessionFamilyId,
      tokenGeneration: p.tokenGeneration, authorizationHorizon: p.authorizationHorizon, deviceId: p.deviceId, installationId: p.installationId,
      ...(p.serverIdentity ? {serverIdentity: p.serverIdentity} : {}), ...(p.refreshToken ? {refreshToken: p.refreshToken} : {}),
      viewer: {serverId: m.serverId, authority: p.authority, accountId: p.accountId, profileId: p.profileId, role: p.role}};
    const name = createHash('sha256').update(JSON.stringify([m.target, m.serverId, p.authority, p.accountId, p.profileId, p.sessionFamilyId, p.deviceId])).digest('hex');
    const store = new FileMutationStore(join(o.mutationStore, name + '.json'));
    const c = await mutationClient(m.target, session, store, scopedFetch(p));
    const bound = {request: <T>(path: string, method = 'GET', body?: unknown, other?: AbortSignal) =>
      c.api.request<T>(path, method, body, AbortSignal.any([signal, ...(other ? [other] : []), AbortSignal.timeout(5000)])),
      requestRaw: c.api.requestRaw.bind(c.api), acknowledgeUserMutation: c.api.acknowledgeUserMutation.bind(c.api)};
    let refresher: LocalSessionRefresher | undefined, connection: FileConnectionStore | undefined;
    let token = () => p.accessToken;
    if (p.refreshToken && p.serverIdentity) {
      // Rotation needs the canonical protected record; the driver never stores the credential anywhere else.
      await privateStoreDirectory(o.mutationStore);
      const storePath = join(o.mutationStore, name + '.connection.json');
      connection = new FileConnectionStore(storePath);
      await seedConnection(connection, m.target, session, p.serverIdentity);
      refresher = new LocalSessionRefresher({env: {storage: connection, fetcher: scopedFetch(p)}, api: c.api, session,
        refresh: refreshBroker({...refresherOptions(p), storePath}),
        onRotated: rotated => {token = () => rotated.accessToken; refreshedAccounts.add(p.accountId); metrics.inc('sessionRefreshes');},
        onSignedOut: () => {refreshFailures++; metrics.inc('sessionRefreshSignedOut');}});
      refreshRequired++; token = () => refresher!.current().accessToken;
      retired.push(() => refresher?.stop());
    }
    const result = {...c, store, bound, fetch: scopedFetch(p), token, refresher, connection}; clients.push(result); return result;
  };
  const scope = (p: Identity) => ({serverId: m.serverId, viewerId: JSON.stringify([p.authority, p.accountId, p.profileId])});
  const verify = async (p: Identity, c: Awaited<ReturnType<typeof client>>) => metrics.measure('identity', async () => {
    let me: {viewer?: any};
    try {me = await c.api.me(AbortSignal.any([signal, AbortSignal.timeout(5000)]));}
    catch (error) {
      // A family retired by an owner move answers 401 and cannot be renewed. Say that, once,
      // instead of counting it as load pressure: the manifest must carry the current owner.
      if (c.refresher?.getSnapshot().phase === 'ended') {controller.abort(); fault('manifest_identity_retired');}
      throw error;
    }
    const {viewer} = me;
    if (!viewer || viewer.serverId !== m.serverId || viewer.accountId !== p.accountId || viewer.profileId !== p.profileId
      || viewer.authority !== p.authority || viewer.role !== p.role) fault('actual_identity_mismatch');
    const devices: any = await c.bound.request(routePath('get_devices'));
    const current = devices?.items?.filter((d: any) => d.current);
    if (current?.length !== 1 || current[0].id !== p.deviceId || current[0].installationId !== p.installationId) fault('actual_device_mismatch');
    // One forced rotation through the canonical reactive path: the real adapter, not a claim.
    if (o.rotateSessions && c.refresher) await metrics.measure('sessionRotate', async () => {
      const before = c.token();
      const replacement = await c.refresher!.recover(before);
      if (!replacement || c.token() === before) fault('session_rotation_failed');
    });
  });
  async function user(p: User, index: number) {
    const c = await client(p), home = new HomeService(c.bound, {timeoutMs: 5000, initialLimit: 20});
    const search = new SearchService({api: c.bound, scope: scope(p), timeoutMs: 5000, pageSize: 20});
    const life = new AbortController(), local = AbortSignal.any([signal, life.signal]);
    let stop: ReturnType<typeof finalStop> | undefined, reporter: ObservationReporter | undefined, sampleTimer: ReturnType<typeof setInterval> | undefined;
    let draining: Promise<unknown> | undefined;
    const r: UserReceipt = {index, bytes: 0, observations: 0, maxObservationGapMs: 0, mediaStarted: false, mediaActive: false,
      startedAt: 0, lastAck: 0, expectedBytes: 0, stopped: false}; receipts.push(r);
    try {
      await sleep(index * o.ramp * 1000 / o.users, local);
      await verify(p, c);
      await metrics.measure('capabilities', async () => {
        if (await new CapabilityPublisher(c.http, () => p.deviceId).publish(p.capabilities, local) !== 'accepted') fault('capability_publication_failed');
      });
      const capabilities = await metrics.measure('browseCapabilities', async () => parseBrowseCapabilities(await c.bound.request(browseCapabilitiesPath(p.libraryId, p.pivot))));
      if (capabilities.library.id !== p.libraryId) fault('browse_library_mismatch');
      const body = new BrowseQueryBuilder(capabilities, p.pivot).limit(20).build();
      const itemId = p.itemIds[index % p.itemIds.length];
      const session = await metrics.measure('start', () => c.sessions.start({itemId}, {quality: {mode: 'original'}, subtitles: {trackId: null}, startFrom: 'beginning'}, randomUUID(), {
        signal: local, capturePreparing: s => {stop = finalStop(c.sessions, s.id, s.presentation.generation); return () => stop!(undefined, AbortSignal.timeout(5000));}}));
      stop = finalStop(c.sessions, session.id, session.presentation.generation);
      const presentation = session.presentation, decisions = Object.values(presentation.decision).filter(Boolean);
      // Evidence of the actual plan, bounded to shapes: a direct plan may legitimately have no
      // conversion decisions at all, and no decision may authorize anything but a copy.
      metrics.inc('planMode:' + String(presentation.mode));
      for (const action of new Set(decisions.map(d => String(d!.action)))) metrics.inc('planAction:' + action);
      // Direct play and stream copy are both non-conversion; only transcode/burn needs an encoder.
      if (session.itemId !== itemId || session.state !== 'playing' || presentation.mode !== 'direct'
        || decisions.some(d => ['transcode', 'burn'].includes(String(d!.action)))) fault('direct_only_plan_required');
      const mediaURL = new URL(presentation.url, m.target);
      if (mediaURL.origin !== m.target || mediaURL.username || mediaURL.password || !mediaURL.pathname.startsWith('/v1/media/')) fault('media_origin_or_path_refused');
      reporter = new ObservationReporter({everyMs: 1000,
        claim: b => metrics.measure('claim', () => c.sessions.claimObserver(session.id, b, local)),
        send: sample => metrics.measure('observation', async () => {
          const ack = await c.sessions.observe(session.id, sample, local);
          if (ack.acceptedSeq !== sample.seq) fault('observation_sequence_mismatch');
          const at = now(); r.maxObservationGapMs = Math.max(r.maxObservationGapMs, at - r.lastAck);
          r.lastAck = at; r.observations++; metrics.inc('observationAcks'); return ack;
        })});
      reporter.setGeneration(presentation.generation);
      let lastByte = 0;
      draining = metrics.measure('media', async () => {
        const response = await c.fetch(mediaURL.href, {headers: {Authorization: 'Bearer ' + c.token()}, signal: local});
        if (response.status !== 200 || !response.body || /(?:json|mpegurl|text\/)/i.test(response.headers.get('content-type') ?? '')) {
          await response.body?.cancel(); throw Object.assign(new Error('media_body_refused'), {status: response.status});
        }
        const reader = response.body.getReader(); const cancel = () => {void reader.cancel().catch(() => {});};
        local.addEventListener('abort', cancel, {once: true});
        r.startedAt = r.lastAck = now(); r.expectedBytes = p.bytesPerSecond * Math.max(0, end - r.startedAt) / 1000;
        r.mediaStarted = r.mediaActive = true; metrics.inc('mediaBodies');
        try {
          while (!local.aborted) {
            const readAt = now();
            const stall = setTimeout(() => life.abort(), 5000);
            let chunk: ReadableStreamReadResult<Uint8Array>;
            try {chunk = await reader.read();} finally {clearTimeout(stall);}
            if (local.aborted) break;
            if (chunk.done) fault('media_ended_before_window');
            metrics.inc('mediaReadMs', Math.ceil(now() - readAt));
            for (let offset = 0; offset < chunk.value.length && !local.aborted; offset += 16384) {
              const size = Math.min(16384, chunk.value.length - offset);
              await sleep(r.startedAt + (r.bytes + size) / p.bytesPerSecond * 1000 - now(), local);
              r.bytes += size; lastByte = now(); metrics.inc('mediaBytes', size);
            }
          }
        } finally {r.mediaActive = false; local.removeEventListener('abort', cancel); await reader.cancel().catch(() => {}); reader.releaseLock();}
      });
      // Observe the simulated byte consumer, never assert decoder/renderer facts.
      sampleTimer = setInterval(() => reporter?.observe({generation: presentation.generation,
        state: lastByte && now() - lastByte < 2000 ? 'playing' : 'buffering',
        positionMs: Math.floor(r.bytes / p.bytesPerSecond * 1000), rate: 1}), 250);
      // A failed drain retires this user; keep its rejection handled until joined below.
      void draining.catch(() => life.abort());
      let cycle = 0;
      let pendingSaved: {operationId: string; items: {itemId: string; watchlisted: boolean}[]} | undefined;
      while (!local.aborted) {
        for (const [name, action] of [
          ['browse', async () => {
            const result = await readBrowsePage(c.bound, p.libraryId, body, {signal: local, isCurrent: () => !local.aborted});
            releaseBrowseSelection(c.bound, p.libraryId, result.response);
          }],
          ['reading', async () => {
            const capabilities = parseBrowseCapabilities(await c.bound.request(browseCapabilitiesPath(p.reading.libraryId, p.reading.pivot)));
            if (capabilities.library.id !== p.reading.libraryId) fault('reading_library_mismatch');
            const query = new BrowseQueryBuilder(capabilities, p.reading.pivot).limit(20).build();
            const lifetime = {signal: local, isCurrent: () => !local.aborted};
            const result = await readBrowsePage(c.bound, p.reading.libraryId, query, lifetime);
            releaseBrowseSelection(c.bound, p.reading.libraryId, result.response);
            const path = browseFacetsPath(p.reading.libraryId) + '?' + new URLSearchParams({field: p.reading.facet, pivot: p.reading.pivot, limit: '20'});
            await readReadingFacet(c.bound, p.reading.libraryId, path, p.reading.facet, lifetime);
          }],
          ['search', async () => {
            // SearchService builds searchPath and validates the echoed query, groups and scope.
            searchPath({q: p.search, record: false}, 20);
            await search.select(p.search, {record: false}); const s = search.getSnapshot();
            if (s.phase !== 'ready') throw s.error ?? new Error('search_not_ready');
            if (s.groups.some(g => g.status !== 'success')) fault('search_group_failed');
          }],
          ['home', async () => {await home.load(); const s = home.getSnapshot(); if (s.phase !== 'ready') throw s.error ?? new Error('home_not_ready');}],
          // The member's own settings snapshot, every cycle: a sustained Settings read, not the
          // owner's one-off readback.
          ['settings', async () => {
            const snapshot: any = await c.bound.request(routePath('get_direct_account'));
            if (!snapshot || typeof snapshot !== 'object' || !('profiles' in snapshot)) fault('settings_snapshot_invalid');
          }],
        ] as const) {
          if (local.aborted) break;
          await metrics.measure(name, action, () => local.aborted).catch(e => {if (aborted(e) || local.aborted) throw e;});
          await sleep(o.selfCheck ? 80 : 700 + (index * 97 + cycle * 31) % 500, local);
        }
        if (!local.aborted && cycle++ % 3 === 0) await metrics.measure('saved', async () => {
          pendingSaved ??= {operationId: randomUUID(), items: p.itemIds.slice(0, 3).map(itemId => ({itemId, watchlisted: cycle % 2 === 1}))};
          const receipt = await savedBatch(c.api, m.serverId, pendingSaved.operationId, pendingSaved.items, local);
          pendingSaved = undefined;
          if (receipt.updated !== undefined) metrics.inc('savedItems', receipt.updated);
          if (receipt.recovered) metrics.inc('savedRecoveredReceipts');
        }).catch(e => {if (aborted(e)) throw e;});
        await sleep(o.selfCheck ? 100 : 1000, local);
      }
    } catch (e) {
      // Record the code only: no message, path, grant or body ever reaches the receipt.
      if (!aborted(e) && !signal.aborted) {metrics.inc('userFailures'); metrics.inc('userFailure:' + String((e as {code?: unknown})?.code ?? 'other'));}
    }
    finally {
      if (sampleTimer) clearInterval(sampleTimer);
      reporter?.stop(); home.dispose(); search.dispose(); life.abort();
      await draining?.catch(() => {});
      if (r.startedAt) r.maxObservationGapMs = Math.max(r.maxObservationGapMs, Math.min(end, now()) - r.lastAck);
      if (stop) await metrics.measure('stop', async () => {
        await stop!(Math.floor(r.bytes / p.bytesPerSecond * 1000), AbortSignal.timeout(5000)); r.stopped = true;
      }).catch(() => {});
    }
  }
  async function scanOwner(c: Awaited<ReturnType<typeof client>>) {
    const service = new LibraryManagementService({api: c.bound, scope: scope(m.owner), readRetries: 0, timeoutMs: 5000});
    retired.push(() => service.dispose()); await verify(m.owner, c);
    // Server-side proof of the settings the direct-only claim depends on, and of the appdata under test.
    stateReadback = await metrics.measure('stateReadback', () => readServerState(c.api, {serverId: m.serverId, ...(o.appdata ? {appdataRoot: o.appdata} : {})}));
    await service.selectLibrary(m.owner.libraryId);
    while (!signal.aborted) {
      await sleep(o.selfCheck ? 600 : o.rehearsal ? 20000 : 60000, signal);
      if (o.ingest) continue; // The ingest orchestrator exclusively owns its six source scans.
      await metrics.measure('scan', async () => {
        await service.scan(); const s = service.getSnapshot();
        if (s.mutation.phase !== 'complete') throw s.mutation.error ?? new Error('scan_receipt_failed');
        metrics.inc('scanReceipts');
      }).catch(e => {if (aborted(e)) throw e;});
    }
  }
  async function quietMember() {
    const p = m.quietMember, c = await client(p); await verify(p, c);
    const events = new EventsClient({http: c.http, openStream: async (after, receive, s) => {
      const local = AbortSignal.any([s, signal]);
      const task = metrics.measure('sse', async () => {
        // Headers have a deadline; the quiet stream deliberately survives without events.
        const timeout = setTimeout(() => controller.abort(), 5000);
        let response: Response;
        try {response = await c.fetch(m.target + routePath('get_events'), {signal: local,
          headers: {Authorization: 'Bearer ' + c.token(), Accept: 'text/event-stream', ...(after ? {'Last-Event-ID': after} : {})}});} finally {clearTimeout(timeout);}
        if (response.status !== 200 || !response.headers.get('content-type')?.includes('text/event-stream')) {
          await response.body?.cancel(); throw Object.assign(new Error('sse_refused'), {status: response.status});
        }
        metrics.inc('sseOpened');
        await readEventStream(response, frame => {metrics.inc('sseFrames'); receive(frame);}, local);
      });
      streams.add(task); try {await task;} finally {streams.delete(task);}
    }});
    retired.push(() => events.stop()); events.onAny(() => metrics.inc('sseEvents')); events.start();
  }
  // Bounded offline-preparation readback: the manifest must name the exact receipt the operator wrote,
  // and the prepared media must outlast the measured window. A manifest label alone proves nothing.
  if (o.appdata) {
    prepReceipt = await readPrepReceipt(o.appdata);
    if (prepReceipt.receipt !== m.stateAdapterReceipt) fault('state_adapter_receipt_mismatch');
    if (prepReceipt.serverId && prepReceipt.serverId !== m.serverId) fault('state_adapter_server_mismatch');
    if (prepReceipt.mediaMinDurationSeconds < o.ramp + o.seconds + 60) fault('prepared_media_too_short');
    // A declared duration is not sustained-media evidence; only a probed file is.
    if (prepReceipt.mediaDurationSource !== 'probe') fault('prepared_media_duration_unproved');
    metrics.inc('prepReceiptVerified');
  }
  try {
    // Direct consumers remain direct; the paired hardware supervisor owns conversion proof.
    metrics.inc('hardwareOnlyRequested', o.hardwareUsers); metrics.inc('hardwareOnlyLocallyRefused', o.hardwareUsers);
    // The campaign keeps its original fixed post-ramp sample. Only the tiny rehearsal, which is not
    // a heavy-run coverage claim, reports peak concurrency and says so in the receipt.
    plateauMethod = o.rehearsal ? 'peak-sampled-rehearsal-only'
      : o.ingest ? 'peak-sampled-ingest-noncapacity' : 'fixed-post-ramp';
    if (o.rehearsal || o.ingest) {
      const plateauTimer = setInterval(() => {
        plateauUsers = Math.max(plateauUsers, receipts.filter(r => r.mediaActive && r.bytes > 0).length);
      }, 500);
      retired.push(() => clearInterval(plateauTimer));
    } else {
      const plateau = setTimeout(() => {plateauUsers = receipts.filter(r => r.mediaActive && r.bytes > 0).length;}, o.ramp * 1000 + 1000);
      retired.push(() => clearTimeout(plateau));
    }
    // The owner path is preparation, not workload: a missing server-side proof refuses before any window.
    const ownerClient = await client(m.owner).catch(e => {metrics.inc('ownerFailures'); throw e;});
    // Phase windows + item markers for an orchestrator that is ingesting while this runs. The
    // driver only reads the control file; the runner owns it.
    if (o.phases) {
      metrics.beginPhase('baseline');
      const resident = {home: new HomeService(ownerClient.bound, {timeoutMs: 5000, initialLimit: 20}),
        search: new SearchService({api: ownerClient.bound, scope: scope(m.owner), timeoutMs: 5000, pageSize: 20})};
      retired.push(() => {resident.home.dispose(); resident.search.dispose();});
      let browseBody: unknown | undefined;
      const residentRead = async (marker: number, items: number) => {
        const lifetime = {signal: AbortSignal.any([signal, AbortSignal.timeout(10_000)]), isCurrent: () => !signal.aborted};
        markerReads.push({marker, items, at: new Date().toISOString()});
        try {
          const capabilities = await parseBrowseCapabilities(await ownerClient.bound.request(browseCapabilitiesPath(m.owner.libraryId, 'movies')));
          browseBody ??= new BrowseQueryBuilder(capabilities, 'movies').limit(20).build();
        } catch {browseBody ??= undefined;}
        if (browseBody) await metrics.measure(`resident:${marker}:browse`, async () => {
          const page = await readBrowsePage(ownerClient.bound, m.owner.libraryId, browseBody!, lifetime);
          releaseBrowseSelection(ownerClient.bound, m.owner.libraryId, page.response);
        }).catch(() => {});
        await metrics.measure(`resident:${marker}:search`, async () => {await resident.search.select('item', {record: false});}).catch(() => {});
        await metrics.measure(`resident:${marker}:home`, async () => {await resident.home.load();}).catch(() => {});
      };
      markerControl = phaseWindows(o.control!, o.itemMarkers, (marker, items) => {void residentRead(marker, items);},
        phase => metrics.beginPhase(phase));
      retired.push(() => {markerControl?.stop(); metrics.beginPhase('done');});
    }
    await Promise.allSettled([scanOwner(ownerClient).catch(e => {if (!aborted(e)) metrics.inc('ownerFailures');}),
      quietMember().catch(e => {if (!aborted(e)) metrics.inc('quietMemberFailures');}), ...m.users.slice(0, o.users).map(user)]);
  } finally {
    markerErrors = markerControl?.errors ?? markerErrors;
    controller.abort(); clearTimeout(deadline); retired.forEach(f => f());
    await Promise.allSettled([...streams]);
    clients.forEach(c => c.coordinator.dispose());
    process.removeListener('SIGINT', interrupt); process.removeListener('SIGTERM', interrupt);
  }
  const achieved = receipts.filter(r => r.bytes > 0 && r.observations > 0).length;
  const enough = (key: string) => (metrics.ops[key]?.ok ?? 0) >= o.users;
  // The direct-only capacity campaign requires an absent conversion engine. Ingest instead
  // records the available engine and proves direct delivery on every measured session.
  // A rehearsal records the same readback without refusing a settings choice it is not qualifying.
  if (!o.rehearsal && !o.ingest && !o.observedHardware && stateReadback && stateReadback.conversionsConfigured)
    fault('server_conversions_are_configured');
  const rotationsProved = refreshFailures === 0 && refreshedAccounts.size === refreshRequired;
  const gaps: string[] = [];
  if (!refreshRequired) gaps.push('refresh_storage_adapter_not_supplied');
  if (!stateReadback) gaps.push('conversion_mode_readback_missing');
  if (stateReadback?.databaseUnderExpectedRoot === null || stateReadback?.databaseUnderExpectedRoot === false) gaps.push('appdata_preparation_readback_missing');
  if (o.rehearsal) gaps.push('rehearsal_is_not_campaign_acceptance');
  if (o.ingest) gaps.push('ingest_noncapacity_not_80_100_campaign');
  if (o.observedHardware) gaps.push('hardware_proof_held_by_mixed_supervisor_receipt');
  // 100 measured users + the quiet member + the owner is 102 validated identities. A manifest
  // capped at 100 users can never reach that, so the gap is expressed on the total.
  const validatedIdentities = m.users.length + 2;
  if (!o.rehearsal && (o.users !== 100 || validatedIdentities < 102)) gaps.push('102_identity_provisioning_unproved');
  // Trend evidence, not a tiny-run pass: every required phase must exist with enough samples for
  // every measured read operation, and every configured item marker must have been observed.
  const trend = (() => {
    if (!o.phases) return null;
    const phases = Object.keys(metrics.phaseOps);
    const compareOps = ['browse', 'reading', 'search', 'home', 'settings'];
    const missingPhases = o.requiredPhases.filter(phase => !metrics.phaseOps[phase]);
    const shortPhases: Record<string, string[]> = {};
    for (const phase of o.requiredPhases) for (const op of compareOps) {
      const samples = metrics.phaseOps[phase]?.[op]?.latency.count ?? 0;
      if (samples < o.minPhaseSamples) (shortPhases[phase] ??= []).push(`${op}:${samples}`);
    }
    const missingMarkers = o.itemMarkers.filter(marker => !markerReads.some(read => read.marker === marker));
    const pct = (op: any, f: number) => op?.latency.percentile(f) ?? null;
    const comparisons = [];
    const base = metrics.phaseOps['baseline'];
    for (const phase of o.requiredPhases.filter(name => name !== 'baseline')) {
      const later = metrics.phaseOps[phase];
      if (!base || !later) continue;
      for (const op of compareOps) {
        const b50 = pct(base[op], .5), b99 = pct(base[op], .99), l50 = pct(later[op], .5), l99 = pct(later[op], .99);
        if (b50 === null || l50 === null) continue;
        comparisons.push({op, phase, baselineP50: b50, p50: l50, baselineP99: b99, p99: l99,
          p50Within: l50 <= b50 * (1 + o.trendP50Percent / 100) + o.trendP50Ms,
          p99Within: l99 !== null && b99 !== null && l99 <= b99 * (1 + o.trendP99Percent / 100) + o.trendP99Ms && l99 <= o.readP99});
      }
    }
    const evidence = !missingPhases.length && !Object.keys(shortPhases).length && !missingMarkers.length;
    return {thresholds: {p50Percent: o.trendP50Percent, p50Ms: o.trendP50Ms, p99Percent: o.trendP99Percent,
        p99Ms: o.trendP99Ms, absoluteP99Ms: o.readP99, minPhaseSamples: o.minPhaseSamples},
      requiredPhases: o.requiredPhases, observedPhases: phases, missingPhases, shortPhases, missingMarkers,
      comparisons, evidence,
      // A tiny rehearsal is never presented as a qualified trend. An ingest run qualifies its own
      // daily-use trend bar (evidence + measurements) and still never claims 80/100 capacity.
      qualified: !o.rehearsal && evidence,
      qualification: o.ingest ? 'ingest-daily-use' : 'campaign',
      qualifiedReason: o.rehearsal ? 'rehearsal' : o.ingest ? 'ingest-daily-use' : evidence ? 'campaign' : 'missing-evidence'};
  })();
  const gates = {
    uninterrupted: !interrupted, users: achieved === o.users, plateauUsers: plateauUsers === o.users,
    mediaBytes: receipts.length === o.users && receipts.every(r => r.mediaStarted && r.bytes >= r.expectedBytes * .8),
    observations: receipts.every(r => r.observations >= Math.floor(o.seconds * .7) && r.maxObservationGapMs <= o.observationGap),
    reads: ['browse', 'reading', 'search', 'home'].every(enough), savedReceipts: enough('saved'),
    scanReceipts: o.ingest || (metrics.counters.scanReceipts ?? 0) >= (o.selfCheck ? 1 : Math.floor((o.ramp + o.seconds) / 60)),
    quietSSE: (metrics.counters.sseOpened ?? 0) > 0,
    cleanup: receipts.every(r => !r.mediaStarted || r.stopped),
    mutationCustody: (await Promise.all(clients.map(c => c.store.read()))).every(state => state.entries.length === 0),
    // Real rotation evidence: when tokens cannot cover the window, every identity must have rotated once.
    sessionRefresh: !refreshRequired || rotationsProved,
    sessionRotate: !o.rotateSessions || rotationsProved,
    readback: !!stateReadback && stateReadback.directConfigured
      && (o.observedHardware
        ? stateReadback.conversionsConfigured && stateReadback.databaseUnderExpectedRoot !== false
        : o.rehearsal || o.ingest || !stateReadback.conversionsConfigured && stateReadback.databaseUnderExpectedRoot !== false),
    // A 401 is a wire error only when it is not the one bounded recovery per identity; anything
    // that survives recovery becomes a user/owner failure anyway.
    noErrorsOrRefusals: !Object.values(metrics.ops).some(s => s.errors)
      && !Object.keys(metrics.counters).some(k => /Failures$/.test(k) || /^wireHTTP(?!401)/.test(k))
      && (metrics.counters.wireHTTP401 ?? 0) <= refreshRequired,
    readLatency: ['browse', 'reading', 'search', 'home', 'settings'].every(k => !!metrics.ops[k]?.latency.count && metrics.ops[k].latency.p99() <= o.readP99),
    trendEvidence: !o.phases || (trend != null && trend.evidence),
    trendSlowdown: !o.phases || !!trend && trend.evidence && trend.comparisons.length > 0
      && trend.comparisons.every(row => row.p50Within && row.p99Within),
    writeLatency: (o.ingest ? ['saved', 'claim', 'stop', 'observation'] : ['saved', 'scan', 'claim', 'stop', 'observation']).every(k => !!metrics.ops[k]?.latency.count && metrics.ops[k].latency.p99() <= o.writeP99),
  };
  const ingestTrendQualified = o.ingest ? ingestTrendQualification(trend, gates) : false;
  return {kind: o.selfCheck ? 'mock-transport-only' : o.rehearsal ? 'rehearsal-only'
      : o.ingest ? 'ingest-noncapacity' : 'simulated-client-load', label: o.label, operatorWindow: o.window,
    observedHardware: o.observedHardware ? {gate: 'paired-window', conversionsConfigured: stateReadback?.conversionsConfigured ?? null} : null,
    startedAt: wallStarted, elapsedSeconds: (now() - started) / 1000, requestedUsers: o.users, achievedUsers: achieved, plateauUsers, plateauMethod,
    validatedIdentities: m.users.length + 2, readP99BudgetMs: o.readP99,
    decoderAcceptance: false, deviceAcceptance: false, hardwareAcceptance: false, completeCampaignAcceptance: false,
    ingestTrendQualified, ingestCapacityAcceptance: false,
    sessionRefresh: {required: refreshRequired, rotated: refreshedAccounts.size, failures: refreshFailures},
    ...(o.phases ? {phases: metrics.phaseReceipt(), markers: markerReads, controlErrors: markerErrors, controlFile: 'private-0600', trend} : {}),
    readback: stateReadback ?? null, prepReceipt: prepReceipt ?? null,
    gaps,
    thresholds: {readP99Ms: o.readP99, writeP99Ms: o.writeP99, observationGapMs: o.observationGap, minimumByteFraction: .8},
    gates, passed: Object.values(gates).every(Boolean), counters: metrics.counters, operations: metrics.receipt(),
    histogramUpperBoundsMs: bounds, users: receipts.map(({startedAt, lastAck, ...r}) => r)};
}

export type TrendEvidence = null | {evidence: boolean; qualified: boolean; missingPhases: string[]; missingMarkers: number[];
  shortPhases: Record<string, string[]>; [key: string]: unknown};
/** Ingest has its own daily-use bar: complete trend evidence plus the measurements that make that
 *  evidence meaningful. It never becomes an 80/100 campaign or capacity claim. */
export function ingestTrendQualification(trend: TrendEvidence, gates: Record<string, boolean>) {
  return !!trend && trend.evidence === true && trend.qualified === true && Object.values(gates).every(value => value === true);
}

/** The mixed supervisor's paired-window authorization. It is not hardware proof: it only says the
 *  two windows are the same target/server/cohorts and are happening now. The supervisor's own
 *  hardware receipt, validated by `scripts/batch-round/hardware/hardware-execution.ts`, is the proof. */
export async function observedHardwareGate(path: string, m: Manifest, o: Options) {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const s = await file.stat();
    if (!s.isFile() || (s.mode & 0o777) !== 0o600 || s.uid !== process.getuid?.() || s.size > 65536)
      fault('hardware_receipt_requires_owned_0600_file');
    let gate: any;
    try {gate = JSON.parse(await file.readFile('utf8'));} catch {fault('hardware_receipt_gate_invalid');}
    if (gate?.kind !== 'p2-65-paired-window' || gate.version !== 1
      || gate.target !== targetOrigin(m.target, o.rehearsal, o.ingest) || gate.serverId !== m.serverId
      || gate.directUsers !== o.users || gate.hardwareUsers !== o.hardwareUsers
      || !Number.isFinite(Date.parse(gate.startedAt)) || !Number.isFinite(Date.parse(gate.endsAt))
      || Date.now() < Date.parse(gate.startedAt) - 60_000 || Date.now() > Date.parse(gate.endsAt))
      fault('hardware_receipt_gate_mismatch');
  } finally {await file.close();}
}

export async function main(args = process.argv.slice(2)) {
  const o = options(args);
  if (!o.execute && !o.selfCheck) {
    console.log(JSON.stringify({state: 'UNRUN', mode: 'dry-run', users: o.users, seconds: o.seconds, rampSeconds: o.ramp, rehearsal: o.rehearsal,
      targetHost: o.rehearsal ? '127.0.0.1 (loopback rehearsal)' : '100.120.50.24', driver: o.rehearsal ? 'this machine, loopback only' : 'mini2-or-portico-runner',
      label: o.label, operatorWindow: o.window,
      requirements: ['explicit --execute', 'owned 0600 manifest', 'owned 0700 persistent mutation-store directory', 'full appstate adapter', 'conversionMode=never', 'separate host observer',
        'per-identity refreshToken + serverIdentity when the access token cannot cover the window', 'optional --appdata prep-receipt + storage readback',
        o.observedHardware ? 'conversionMode=observed-hardware (paired window, gate + supervisor receipt)' : 'conversionMode=never'],
      hardwareOnly: 'separate hardware/hardware-execution.ts receipt; software/unknown/fallback fail'})); return;
  }
  if (process.versions.node !== '24.21.0') fault('node_24_21_0_required');
  requireDriver(o);
  let receipt: {passed: boolean};
  if (o.selfCheck) {
    const {selfCheck} = await import('./load-selfcheck.mjs'); receipt = await selfCheck(o);
  } else {
    if (!o.manifest || !o.target || !o.out || !o.mutationStore) fault('execute_requires_manifest_target_out_mutation_store');
    o.target = targetOrigin(o.target, o.rehearsal, o.ingest);
    const m = await privateManifest(o.manifest); m.target = targetOrigin(m.target, o.rehearsal, o.ingest); validateManifest(m, o);
    if (o.observedHardware) await observedHardwareGate(o.hardwareReceipt!, m, o);
    // Reserve evidence before starting any request. Never overwrite a previous run.
    const out = await open(o.out, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
    let unlock: (() => Promise<void>) | undefined;
    try {unlock = await lockMutationDirectory(o.mutationStore); receipt = await run(m, o); await out.writeFile(JSON.stringify(receipt, null, 2) + '\n');} finally {await out.close(); await unlock?.();}
  }
  console.log(JSON.stringify(receipt)); if (!receipt.passed) process.exitCode = 1;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(() => {console.error('load harness refused or failed; no private details emitted. See load-README.md prerequisites.'); process.exitCode = 1;});
}
