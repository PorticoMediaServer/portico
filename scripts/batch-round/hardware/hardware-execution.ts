/** Operator-only P2-65 hardware execution fixture. UNRUN.
 *
 * Starts two or three real Playback Protocol v1 sessions through client-core's
 * `SessionsClient` (never a parallel HTTP protocol), streams their actual HLS
 * bytes, and reads `GET /v1/admin/sessions` for the admin-only
 * `delivery.hardware.execution` block. It passes only when one admin page shows
 * every session converting on hardware with a DRM render node, and each
 * session served real media.
 *
 * This is a readiness fixture, not the 80/100-user load campaign: it holds at
 * most three conversions, so it proves execution and overlap, not capacity.
 * The recorded prerequisite runs left this file unexecuted.
 */
import {open, constants, lstat, mkdtemp, rm, writeFile, chmod, symlink} from 'node:fs/promises';
import {createHash, randomUUID} from 'node:crypto';
import {pathToFileURL} from 'node:url';
import {setTimeout as delay} from 'node:timers/promises';
import {dirname, join, resolve} from 'node:path';
import {type LocalSession} from '../../../packages/client-core/src/index.ts';
import {validInstallationId} from '../../../packages/client-core/src/installation.ts';
import {capabilityDocumentProblem, type CapabilityDocument} from '../../../packages/client-core/src/client-profile.ts';
import {CapabilityPublisher} from '../../../packages/client-core/src/playback-v1/options.ts';
import {ObservationReporter} from '../../../packages/client-core/src/playback-v1/observation.ts';
import {call, enc} from '../../../packages/client-core/src/playback-v1/http.ts';
import type {Session} from '../../../packages/client-core/src/playback-v1/types.ts';
import {parseHardwareExecution, type HardwareExecution} from '../../../packages/client-core/src/delivery.ts';
import type {QualityId} from '../../../packages/client-core/src/playback-v1/quality.ts';
import {routePath, encodedRoutePath} from '../../../packages/contracts/src/route-paths.ts';
import {FileMutationStore, lockMutationDirectory, mutationClient, finalStop} from '../load-adapters.ts';
// Shared rehearsal/target/driver rules only; load.ts's main is guarded by
// import.meta.url, so importing it starts nothing.
import {requireDriver, targetOrigin} from '../load.ts';

type Identity = {
  accessToken: string; expiresAt: string; sessionFamilyId: string; tokenGeneration: string; authorizationHorizon: string;
  authority: 'local' | 'hosted'; accountId: string; profileId: string; deviceId: string; installationId: string;
  role: 'owner' | 'member'; capabilities: CapabilityDocument;
};
type User = Identity & {itemIds: string[]};
export type Manifest = {version: 1; target: string; serverId: string; owner: Identity; users: User[]};
export type Options = {
  execute: boolean; selfCheck: boolean; rehearsal: boolean; manifest?: string; target?: string; out?: string; mutationStore?: string;
  sessions: number; directUsers: number; quality: string; itemId?: string; bytes: number; seconds: number; renderNode?: string; label: string; scratch?: string;
};

const qualities: Readonly<Record<string, number>> = Object.freeze({
  '2160p': 2160, '1080p-high': 1080, '1080p-low': 1080, '720p-high': 720, '720p-low': 720, '480p': 480, '240p': 240,
});
const hardwareBackends = new Set(['videotoolbox', 'vaapi', 'qsv', 'nvenc', 'amf']);
const minSessions = 2, maxSessions = 3, maxManifestBytes = 4 * 1024 * 1024;
const maxPlaylistBytes = 1024 * 1024, maxSegmentBytes = 16 * 1024 * 1024;
const renderNodePattern = /^\/dev\/dri\/renderD[0-9]+$/;
const masterPathPattern = /^\/v1\/media\/[A-Za-z0-9_-]+\/master\.m3u8$/;
const id = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_.:-]{1,256}$/.test(value);
const obj = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const count = (value: unknown, maximum = Number.MAX_SAFE_INTEGER): value is number => Number.isSafeInteger(value) && Number(value) >= 0 && Number(value) <= maximum;

function fault(code: string): never {throw Object.assign(new Error(code), {code});}
const failureCode = (error: unknown): string => {
  if (['AbortError', 'TimeoutError'].includes(String((error as {name?: unknown} | null)?.name))) return 'interrupted';
  const code = (error as {code?: unknown} | null)?.code;
  return typeof code === 'string' && /^[a-z0-9_]{1,80}$/.test(code) ? code : 'fixture_failed';
};
const sleep = async (ms: number, signal?: AbortSignal) => delay(Math.max(1, ms), undefined, {signal});

export function options(args: string[]): Options {
  const flags = new Map<string, string>();
  const boolean = new Set(['execute', 'self-check', 'rehearsal']);
  const names = new Set(['manifest', 'target', 'out', 'mutation-store', 'sessions', 'direct-users', 'quality', 'item-id', 'bytes', 'seconds',
    'render-node', 'label', 'scratch', ...boolean]);
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
  // Presence is sufficient, including CI=false. No override exists.
  if (execute && ('CI' in process.env || 'GITHUB_ACTIONS' in process.env)) fault('execute_forbidden_in_ci');
  if (execute && selfCheck) fault('conflicting_modes');
  if (rehearsal && !execute) fault('rehearsal_requires_execute');
  if (selfCheck && (flags.has('manifest') || flags.has('target') || flags.has('out') || flags.has('mutation-store'))) fault('self_check_has_no_private_input');
  const quality = flags.get('quality') ?? '720p-high';
  if (!(quality in qualities)) fault('invalid_quality');
  const sessions = number('sessions', 2, minSessions, maxSessions);
  const directUsers = number('direct-users', rehearsal ? 1 : 0, 0, 100);
  // The mixed run watches an existing direct cohort; the fixture never starts it.
  if (rehearsal ? ![0, 1].includes(directUsers) : ![0, 80, 100].includes(directUsers)) fault('invalid_direct_users');
  const bytes = number('bytes', rehearsal ? 1024 * 1024 : 2 * 1024 * 1024, 256 * 1024, 64 * 1024 * 1024);
  const seconds = number('seconds', rehearsal ? 60 : 90, 10, 300);
  const label = flags.get('label') ?? 'hardware-execution';
  if (!/^[A-Za-z0-9_.:-]{1,80}$/.test(label)) fault('invalid_receipt_label');
  const itemId = flags.get('item-id');
  if (itemId !== undefined && !id(itemId)) fault('invalid_item_id');
  const renderNode = flags.get('render-node');
  if (renderNode !== undefined && !renderNodePattern.test(renderNode)) fault('invalid_render_node');
  return {execute, selfCheck, rehearsal, manifest: flags.get('manifest'), target: flags.get('target'), out: flags.get('out'),
    mutationStore: flags.get('mutation-store'), sessions, directUsers, quality, itemId, bytes, seconds, renderNode, label, scratch: flags.get('scratch')};
}

async function privateManifest(path: string): Promise<Manifest> {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const state = await file.stat();
    if (!state.isFile() || (state.mode & 0o777) !== 0o600 || state.uid !== process.getuid?.() || state.size > maxManifestBytes)
      fault('manifest_requires_owned_0600_file');
    try {return JSON.parse(await file.readFile('utf8')) as Manifest;} catch {fault('manifest_invalid_json');}
  } finally {await file.close();}
}

function identityProblem(identity: Identity, deadline: number): boolean {
  if (!identity || !['local', 'hosted'].includes(identity.authority) || !['owner', 'member'].includes(identity.role)) return true;
  if (![identity.accountId, identity.profileId, identity.deviceId, identity.sessionFamilyId, identity.tokenGeneration].every(id)) return true;
  if (!validInstallationId(identity.installationId)) return true;
  if (typeof identity.accessToken !== 'string' || !identity.accessToken || /\s/.test(identity.accessToken)) return true;
  if (!Number.isFinite(Date.parse(identity.expiresAt)) || Date.parse(identity.expiresAt) < deadline) return true;
  if (!Number.isFinite(Date.parse(identity.authorizationHorizon)) || Date.parse(identity.authorizationHorizon) < Date.parse(identity.expiresAt)) return true;
  return capabilityDocumentProblem(identity.capabilities) !== null;
}

export function validateManifest(m: Manifest, o: Options, target: string): void {
  if (m?.version !== 1 || !id(m.serverId) || !Array.isArray(m.users) || m.users.length < o.sessions || m.users.length > 100)
    fault('manifest_state_or_user_gap');
  if (targetOrigin(m.target, o.rehearsal) !== target) fault('manifest_target_mismatch');
  if (!m.owner || m.owner.role !== 'owner') fault('manifest_owner_gap');
  const all = [m.owner, ...m.users];
  // Streaming and evidence each have their own --seconds budget.
  const deadline = Date.now() + (2 * o.seconds + 120) * 1000;
  for (const identity of all) if (identityProblem(identity, deadline)) fault('manifest_identity_or_capability_gap');
  for (const key of ['accessToken', 'accountId', 'profileId', 'deviceId', 'installationId', 'sessionFamilyId'] as const)
    if (new Set(all.map(identity => identity[key])).size !== all.length) fault('manifest_identities_must_be_distinct');
  for (const user of m.users) {
    if (user.role !== 'member') fault('manifest_session_user_role_gap');
    if (!Array.isArray(user.itemIds) || !user.itemIds.length || user.itemIds.length > 20 || !user.itemIds.every(id)
      || new Set(user.itemIds).size !== user.itemIds.length) fault('manifest_playable_item_gap');
  }
}

function localSession(m: Manifest, identity: Identity): LocalSession {
  return {accessToken: identity.accessToken, expiresAt: identity.expiresAt, sessionFamilyId: identity.sessionFamilyId,
    tokenGeneration: identity.tokenGeneration, authorizationHorizon: identity.authorizationHorizon,
    deviceId: identity.deviceId, installationId: identity.installationId,
    viewer: {serverId: m.serverId, authority: identity.authority, accountId: identity.accountId, profileId: identity.profileId, role: identity.role}};
}

/** One transport per identity: no cross-origin read, no redirect, no ambient credentials. */
function scopedFetch(target: string, identity: Identity): typeof fetch {
  return async (input, init = {}) => {
    const url = new URL(String(input));
    if (url.origin !== target || url.username || url.password) fault('cross_origin_request_refused');
    const headers = new Headers(init.headers);
    if ((init.method ?? 'GET') === 'POST' && /\/v1\/(?:auth|direct)\//.test(url.pathname)) headers.set('X-Portico-Installation-Id', identity.installationId);
    return fetch(input, {...init, headers, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer'});
  };
}

type Client = Awaited<ReturnType<typeof mutationClient>> & {identity: Identity; store: FileMutationStore; fetch: typeof fetch};
const liveClients = new Set<Client>();

async function clientFor(m: Manifest, o: Options, identity: Identity, target: string, signal: AbortSignal): Promise<Client> {
  if (!o.mutationStore) fault('mutation_store_required');
  const scope = createHash('sha256').update(JSON.stringify([target, m.serverId, identity.authority, identity.accountId,
    identity.profileId, identity.sessionFamilyId, identity.deviceId])).digest('hex');
  const store = new FileMutationStore(join(o.mutationStore, scope + '.json'));
  const transport = scopedFetch(target, identity);
  const client = await mutationClient(target, localSession(m, identity), store, transport);
  if (signal.aborted) {client.coordinator.dispose(); signal.throwIfAborted();}
  const result = {...client, identity, store, fetch: transport};
  liveClients.add(result);
  return result;
}

async function verifyIdentity(client: Client, m: Manifest, signal: AbortSignal): Promise<void> {
  const {viewer} = await client.api.me(AbortSignal.any([signal, AbortSignal.timeout(5000)]));
  const identity = client.identity;
  if (!viewer || viewer.serverId !== m.serverId || viewer.accountId !== identity.accountId || viewer.profileId !== identity.profileId
    || viewer.authority !== identity.authority || viewer.role !== identity.role) fault('actual_identity_mismatch');
  const devices = await client.api.request<unknown>(routePath('get_devices'), 'GET', undefined, AbortSignal.any([signal, AbortSignal.timeout(5000)]));
  const items = obj(devices) && Array.isArray(devices.items) ? devices.items.filter(obj) : [];
  const current = items.filter(device => device.current === true);
  if (current.length !== 1 || current[0].id !== identity.deviceId || current[0].installationId !== identity.installationId)
    fault('actual_device_mismatch');
}

async function adminPage(owner: Client, serverId: string, signal: AbortSignal): Promise<Record<string, unknown>[]> {
  const raw = await owner.api.request<unknown>(routePath('list_admin_sessions') + '?limit=200', 'GET', undefined,
    AbortSignal.any([signal, AbortSignal.timeout(10000)]));
  if (!obj(raw) || !obj(raw.scope) || raw.scope.serverId !== serverId || !Array.isArray(raw.items) || raw.items.length >= 200)
    fault('admin_sessions_page_invalid');
  return raw.items.filter(obj);
}

async function diagnostics(owner: Client, serverId: string, signal: AbortSignal): Promise<Record<string, unknown>> {
  const raw = await owner.api.request<unknown>(routePath('get_admin_playback_diagnostics'), 'GET', undefined,
    AbortSignal.any([signal, AbortSignal.timeout(10000)]));
  if (!obj(raw) || !obj(raw.scope) || raw.scope.serverId !== serverId || !obj(raw.diagnostics)) fault('diagnostics_envelope_invalid');
  return raw.diagnostics;
}

function hardwareStatus(document: Record<string, unknown>): {effective: string; supported: boolean; limit: number | null; active: number; fallbacks: number; refused: number} {
  const hardware = obj(document.hardware) ? document.hardware : fault('diagnostics_hardware_missing');
  const effective = hardware.effective;
  if (typeof effective !== 'string') fault('diagnostics_backend_unresolved');
  const probes = Array.isArray(hardware.probes) ? hardware.probes.filter(obj) : [];
  const supported = probes.some(probe => probe.backend === effective && probe.supported === true);
  // A null limit is the server publishing no host ceiling. Record it and let
  // the start admission enforce capacity; this fixture still starts only 2-3.
  const limit = document.conversionSessionLimit === null || document.conversionSessionLimit === undefined ? null : document.conversionSessionLimit;
  if (limit !== null && !count(limit, 4096)) fault('diagnostics_counters_missing');
  if (!count(document.activeConversionSessions, 4096) || !count(document.hardwareFallbacks) || !count(document.conversionsRefused))
    fault('diagnostics_counters_missing');
  return {effective, supported, limit, active: document.activeConversionSessions,
    fallbacks: document.hardwareFallbacks, refused: document.conversionsRefused};
}

async function preflight(owner: Client, m: Manifest, o: Options, signal: AbortSignal): Promise<Record<string, unknown>> {
  const system = await owner.api.system();
  if (system.id !== m.serverId) fault('server_identity_mismatch');
  const document = await diagnostics(owner, m.serverId, signal);
  if (document.lifecycle !== 'running') fault('delivery_not_running');
  if (document.hlsConfigured !== true) fault('hls_not_configured');
  const status = hardwareStatus(document);
  if (status.effective === 'software' || !hardwareBackends.has(status.effective) || !status.supported) fault('hardware_backend_unavailable');
  if (status.limit !== null && status.limit < o.sessions) fault('conversion_capacity_unavailable');
  if (status.active !== 0) fault('server_not_quiet');
  const page = await adminPage(owner, m.serverId, signal);
  return {systemId: system.id, backend: status.effective, supported: status.supported, limit: status.limit,
    finiteHlsConfigured: document.finiteHlsConfigured === true, active: status.active,
    hardwareFallbacks: status.fallbacks, conversionsRefused: status.refused, openSessions: page.length};
}

async function capability(client: Client, signal: AbortSignal): Promise<string> {
  const result = await new CapabilityPublisher(client.http, () => client.identity.deviceId).publish(client.identity.capabilities, signal);
  if (result !== 'accepted' && result !== 'current') fault('capability_publication_failed');
  return result;
}

type Stream = {
  client: Client; session: Session; itemId: string; reporter: ObservationReporter<Session>; generation: number;
  bytes: number; initBytes: number; segments: number; positionMs: number; evidence?: HardwareExecution; lastRow?: Record<string, unknown>;
  stopped: boolean; stopOk?: boolean; done: boolean;
};

function parsePlaylist(raw: Uint8Array): {map?: string; variants: string[]; segments: {duration: number; path: string}[]} {
  if (raw.byteLength > maxPlaylistBytes) fault('hls_playlist_too_large');
  let text: string;
  try {text = new TextDecoder('utf-8', {fatal: true}).decode(raw);} catch {fault('hls_playlist_invalid');}
  if (text.includes('\0') || !text.startsWith('#EXTM3U')) fault('hls_playlist_invalid');
  const lines = text.replace(/\r\n/g, '\n').split('\n').map(line => line.trim());
  let map: string | undefined;
  const variants: string[] = [], segments: {duration: number; path: string}[] = [];
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index];
    if (line.startsWith('#EXT-X-MAP:')) {
      const match = /URI="([^"]{1,1024})"/.exec(line);
      if (!match) fault('hls_playlist_invalid');
      map = match[1];
    }
    if (line.startsWith('#EXT-X-STREAM-INF:')) {
      const reference = lines.slice(index + 1).find(candidate => candidate && !candidate.startsWith('#'));
      if (!reference) fault('hls_playlist_invalid');
      variants.push(reference);
    }
    if (line.startsWith('#EXTINF:')) {
      const duration = Number(line.slice('#EXTINF:'.length).split(',')[0]);
      if (!Number.isFinite(duration) || duration <= 0 || duration > 3600) fault('hls_playlist_invalid');
      const reference = lines.slice(index + 1).find(candidate => candidate && !candidate.startsWith('#'));
      if (!reference) fault('hls_playlist_invalid');
      segments.push({duration, path: reference});
    }
  }
  if (variants.length && segments.length) fault('hls_playlist_invalid');
  return {map, variants, segments};
}

/** Resolve one playlist reference inside the session's own media grant. */
function mediaPath(base: string, reference: string): string {
  if (!reference || /^[a-z][a-z0-9+.-]*:/i.test(reference) || reference.startsWith('//') || reference.includes('\\')) fault('hls_reference_refused');
  const [path, query] = reference.split('?', 2);
  const resolved = new URL(path, 'http://fixture.invalid' + base).pathname;
  if (path.split('/').includes('..') || !resolved.startsWith('/v1/media/') || resolved.split('/').length < 5)
    fault('hls_reference_refused');
  return resolved + (query ? '?' + query : '');
}

async function readBounded(response: Response, limit: number): Promise<Uint8Array> {
  if (!response.body) fault('media_body_refused');
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      total += chunk.value.byteLength;
      if (!chunk.value.byteLength || total > limit) fault('media_size_refused');
      chunks.push(chunk.value);
    }
  } finally {await reader.cancel().catch(() => {});}
  const body = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {body.set(chunk, offset); offset += chunk.byteLength;}
  return body;
}

async function mediaBytes(client: Client, path: string, limit: number, contentType: 'playlist' | 'segment', signal: AbortSignal): Promise<Uint8Array> {
  if (!path.startsWith('/v1/media/')) fault('media_path_refused');
  const response = await client.fetch(new URL(path, client.api.baseUrl).href, {
    headers: {Authorization: 'Bearer ' + client.identity.accessToken, Accept: '*/*'},
    signal: AbortSignal.any([signal, AbortSignal.timeout(20000)])});
  if (response.status !== 200) {void response.body?.cancel().catch(() => {}); fault('media_body_refused');}
  const type = response.headers.get('content-type')?.toLowerCase() ?? '';
  if (contentType === 'playlist' && !type.includes('mpegurl')) {void response.body?.cancel().catch(() => {}); fault('hls_playlist_refused');}
  const length = Number(response.headers.get('content-length') ?? NaN);
  if (Number.isFinite(length) && (length <= 0 || length > limit)) {void response.body?.cancel().catch(() => {}); fault('media_size_refused');}
  return readBounded(response, limit);
}

async function startAll(states: {client: Client; itemId: string}[], o: Options, signal: AbortSignal, streams: Stream[]): Promise<void> {
  for (const state of states) {
    const session = await state.client.sessions.start({itemId: state.itemId}, {
      quality: {mode: 'fixed', id: o.quality as QualityId}, subtitles: {trackId: null}, startFrom: 'beginning', state: 'playing',
    }, randomUUID(), {signal: AbortSignal.any([signal, AbortSignal.timeout(30000)])});
    try {
      const presentation = session.presentation;
      if (session.itemId !== state.itemId || session.state !== 'playing') fault('session_start_unexpected');
      if (presentation.mode !== 'stream' || presentation.decision.video?.action !== 'transcode' || !masterPathPattern.test(presentation.url)
        || !['fmp4_hls', 'mpegts_hls'].includes(presentation.container ?? '')) fault('conversion_not_planned');
      const generation = presentation.generation;
      if (!count(generation, 1_000_000)) fault('session_generation_invalid');
      // The ordinary viewer's session read must never carry the owner's execution evidence.
      const raw = await call(state.client.http, {method: 'GET', path: encodedRoutePath('get_playback_session', {id: enc(session.id)}),
        signal: AbortSignal.any([signal, AbortSignal.timeout(5000)])});
      const wire = JSON.stringify(raw.body ?? '');
      if (['launchCount', 'lastLaunch', 'renderNode', 'softwareLaunchCount'].some(secret => wire.includes(secret)))
        fault('viewer_execution_leaked');
      const reporter = new ObservationReporter<Session>({everyMs: 1000,
        claim: body => state.client.sessions.claimObserver(session.id, body, signal),
        send: sample => state.client.sessions.observe(session.id, sample, signal)});
      reporter.setGeneration(generation);
      reporter.observe({generation, state: 'playing', positionMs: 0, rate: 1});
      streams.push({client: state.client, session, itemId: state.itemId, reporter, generation,
        bytes: 0, initBytes: 0, segments: 0, positionMs: 0, stopped: false, done: false});
    } catch (error) {
      // The start may have created a session that this fixture will never stream.
      await state.client.sessions.stop(session.id, 0, AbortSignal.timeout(5000)).catch(() => {});
      throw error;
    }
  }
}

async function stopStreams(streams: Stream[]): Promise<void> {
  for (const stream of streams) {
    if (stream.stopped) continue;
    stream.stopped = true;
    stream.reporter.stop();
    const stop = finalStop(stream.client.sessions, stream.session.id, stream.generation);
    try {await stop(stream.positionMs, AbortSignal.timeout(5000)); stream.stopOk = true;}
    catch {stream.stopOk = false;}
  }
}

async function streamSession(stream: Stream, o: Options, signal: AbortSignal): Promise<void> {
  try {
  const began = Date.now(), deadline = began + o.seconds * 1000;
  let base = stream.session.presentation.url;
  let playlist = parsePlaylist(await mediaBytes(stream.client, base, maxPlaylistBytes, 'playlist', signal));
  if (playlist.variants.length) {
    if (playlist.variants.length !== 1) fault('hls_multiple_variants');
    base = mediaPath(base, playlist.variants[0]);
    playlist = parsePlaylist(await mediaBytes(stream.client, base, maxPlaylistBytes, 'playlist', signal));
  }
  if (playlist.map) stream.initBytes += (await mediaBytes(stream.client, mediaPath(base, playlist.map), maxSegmentBytes, 'segment', signal)).byteLength;
  let wait = 100;
  while ((stream.bytes < o.bytes || stream.segments < 2) && Date.now() < deadline) {
    playlist = parsePlaylist(await mediaBytes(stream.client, base, maxPlaylistBytes, 'playlist', signal));
    if (playlist.segments.length > stream.segments) {
      const segment = playlist.segments[stream.segments];
      // Consume at media time: buffering two segments instantly must not erase the overlap window.
      if (stream.segments) await sleep(Math.max(0, stream.positionMs - (Date.now() - began)), signal);
      stream.bytes += (await mediaBytes(stream.client, mediaPath(base, segment.path), maxSegmentBytes, 'segment', signal)).byteLength;
      stream.segments++;
      stream.positionMs += Math.round(segment.duration * 1000);
      stream.reporter.observe({generation: stream.generation, state: 'playing', positionMs: stream.positionMs, rate: 1});
      wait = 100;
    } else await sleep(wait, signal);
    wait = Math.min(1000, Math.round(wait * 1.5));
  }
  if (stream.bytes < o.bytes) fault('hls_bytes_not_observed');
  if (stream.segments < 2) fault('hls_segments_not_observed');
  } finally {stream.done = true;}
}

function executionFailures(execution: HardwareExecution, expectedRenderNode?: string): string[] {
  const failures: string[] = [];
  if (execution.launchCount < 1) failures.push('no_successful_launch');
  if (execution.softwareLaunchCount) failures.push('software_launch_observed');
  if (execution.unknownLaunchCount) failures.push('unknown_launch_observed');
  if (execution.fallbackCount) failures.push('hardware_fallback_observed');
  if (execution.lastLaunch.encoder.execution !== 'hardware') failures.push('encoder_not_hardware');
  if (execution.lastLaunch.decoder.execution !== 'hardware') failures.push('decoder_not_hardware');
  const renderNode = execution.lastLaunch.renderNode;
  if (!renderNode || !renderNodePattern.test(renderNode)) failures.push('render_node_missing');
  else if (expectedRenderNode && renderNode !== expectedRenderNode) failures.push('render_node_mismatch');
  return failures;
}

/** The dashboard row proves conversion execution, an encode stage, and a converted video stream. */
function rowEvidence(row: Record<string, unknown>, o: Options): HardwareExecution {
  if (row.state !== 'playing') fault('dashboard_state_not_playing');
  const delivery = obj(row.delivery) ? row.delivery : fault('delivery_missing');
  const hardware = obj(delivery.hardware) ? delivery.hardware : fault('delivery_hardware_missing');
  if (typeof hardware.backend !== 'string' || !hardwareBackends.has(hardware.backend)) fault('dashboard_backend_not_hardware');
  const execution = parseHardwareExecution(hardware.execution);
  if (!execution) fault('execution_missing');
  const failures = executionFailures(execution, o.renderNode);
  if (failures.length) fault(failures[0]);
  const stages = Array.isArray(hardware.stages) ? hardware.stages.filter(obj) : [];
  if (!stages.some(stage => stage.operation === 'encode' && stage.execution === 'hardware')) fault('dashboard_encode_stage_not_hardware');
  const streams = Array.isArray(delivery.streams) ? delivery.streams.filter(obj) : [];
  if (!streams.some(item => item.kind === 'video' && item.action === 'convert')) fault('dashboard_video_not_converted');
  if (!count(delivery.videoHeight, 4320) || delivery.videoHeight < 1 || delivery.videoHeight > qualities[o.quality])
    fault('dashboard_height_unexpected');
  const media = obj(row.media) ? row.media : undefined;
  if (!media || media.videoHardware !== true) fault('dashboard_video_hardware_not_true');
  return execution;
}

type ObservedHistory = {
  samples: number; overlapSamples: number; firstOverlapAt?: string; lastOverlapAt?: string;
  maxActiveConversions: number; maxDirectSessions: number; maxDirectAccounts: number; maxHardwareSessions: number;
  drainOverlap: boolean; lastFailure: string; sampleHistory?: Record<string, unknown>[];
};

/** A direct row is playing through no transcode: direct play or copy/remux only. */
function directRow(row: Record<string, unknown>): Readonly<{session: string; account: string}> | undefined {
  if (row.state !== 'playing' || row.protocol !== 'v1') return undefined;
  const decision = obj(row.decision) ? row.decision : undefined;
  if (!decision) return undefined;
  const action = (family: unknown): string | undefined => obj(family) && typeof family.action === 'string' ? family.action : undefined;
  const video = action(decision.video), audio = action(decision.audio), subtitles = action(decision.subtitles);
  if (video === 'transcode' || video === 'burn' || audio === 'transcode' || subtitles === 'burn') return undefined;
  if (![video, audio].some(value => value === 'direct' || value === 'copy')) return undefined;
  if (obj(row.delivery) && obj(row.delivery.hardware) && row.delivery.hardware.execution !== undefined) return undefined;
  const user = obj(row.user) ? row.user : undefined;
  if (!id(row.id) || !user || !id(user.id)) return undefined;
  return Object.freeze({session: row.id, account: user.id});
}

function executionOf(row: Record<string, unknown>): HardwareExecution | undefined {
  const delivery = obj(row.delivery) ? row.delivery : undefined;
  const hardware = delivery && obj(delivery.hardware) ? delivery.hardware : undefined;
  if (!hardware || hardware.execution === undefined) return undefined;
  const execution = parseHardwareExecution(hardware.execution);
  if (!execution) fault('execution_invalid');
  return execution;
}

/** Sample the admin page and conversion diagnostics while HLS is actually draining. */
async function sampleDuringDrain(owner: Client, serverId: string, streams: Stream[], o: Options,
                                 baseline: {fallbacks: number; refused: number}, history: ObservedHistory,
                                 active: () => boolean, draining: () => boolean, signal: AbortSignal): Promise<void> {
  while (active()) {
    const at = new Date().toISOString();
    let page: Record<string, unknown>[], diagnostic: Record<string, unknown>;
    try {
      [page, diagnostic] = await Promise.all([adminPage(owner, serverId, signal), diagnostics(owner, serverId, signal)]);
    } catch (error) {
      history.lastFailure = failureCode(error);
      await sleep(500, signal);
      continue;
    }
    const status = hardwareStatus(diagnostic);
    if (status.fallbacks !== baseline.fallbacks) fault('hardware_fallback_observed');
    if (status.refused !== baseline.refused) fault('conversion_refused_during_fixture');
    const direct = new Map<string, string>();
    for (const row of page) {
      const entry = directRow(row);
      if (entry) direct.set(entry.session, entry.account);
    }
    history.maxDirectSessions = Math.max(history.maxDirectSessions, direct.size);
    history.maxDirectAccounts = Math.max(history.maxDirectAccounts, new Set(direct.values()).size);
    let hardwareComplete = true;
    let hardwareRows = 0;
    const byId = new Map<string, Record<string, unknown>>();
    for (const stream of streams) {
      const row = page.find(item => item.id === stream.session.id);
      if (!row) {hardwareComplete = false; history.lastFailure = 'dashboard_row_missing'; continue;}
      const execution = executionOf(row);
      if (!execution) {hardwareComplete = false; history.lastFailure = 'execution_missing'; continue;}
      const failures = executionFailures(execution, o.renderNode);
      if (failures.length) fault(failures[0]);
      try {rowEvidence(row, o);} catch (error) {hardwareComplete = false; history.lastFailure = failureCode(error); continue;}
      byId.set(stream.session.id, row);
      hardwareRows++;
    }
    history.maxHardwareSessions = Math.max(history.maxHardwareSessions, hardwareRows);
    const activeConversions = count(status.active, 4096) ? status.active : undefined;
    if (activeConversions !== undefined) history.maxActiveConversions = Math.max(history.maxActiveConversions, activeConversions);
    const directEnough = o.directUsers === 0 || direct.size >= o.directUsers && new Set(direct.values()).size >= o.directUsers;
    if (draining() && hardwareComplete && byId.size === streams.length && directEnough
        && activeConversions !== undefined && activeConversions >= streams.length) {
      history.overlapSamples++;
      history.firstOverlapAt ??= at;
      history.lastOverlapAt = at;
      history.drainOverlap = true;
      history.lastFailure = 'none';
      for (const stream of streams) {
        const row = byId.get(stream.session.id)!;
        stream.evidence = rowEvidence(row, o);
        stream.lastRow = row;
      }
    }
    (history.sampleHistory ??= []).push({at, completedAt: new Date().toISOString(), activeConversions, hardwareRows, hardwareComplete, draining: draining(), lastFailure: history.lastFailure});
    history.samples++;
    // Keep every session's lease alive while the overlap window is sampled.
    for (const stream of streams) stream.reporter.observe({generation: stream.generation, state: 'playing', positionMs: stream.positionMs, rate: 1});
    await sleep(500, signal);
  }
  if (history.samples === 0) fault('admin_sampling_unavailable');
  if (history.overlapSamples < 2 || !history.firstOverlapAt || !history.lastOverlapAt || !history.drainOverlap)
    fault('overlap_evidence_insufficient');
  const span = Date.parse(history.lastOverlapAt) - Date.parse(history.firstOverlapAt);
  if (!Number.isFinite(span) || span < 500) fault('overlap_evidence_insufficient');
}

async function run(m: Manifest, o: Options, target: string, signal: AbortSignal): Promise<Record<string, unknown>> {
  const startedAt = new Date().toISOString();
  const owner = await clientFor(m, o, m.owner, target, signal);
  await verifyIdentity(owner, m, signal);
  const member = await clientFor(m, o, m.users[0], target, signal);
  const clients: Client[] = [owner, member];
  const streams: Stream[] = [];
  const failures: {session: string; code: string}[] = [];
  const history: ObservedHistory = {samples: 0, overlapSamples: 0, maxActiveConversions: 0,
    maxDirectSessions: 0, maxDirectAccounts: 0, maxHardwareSessions: 0, drainOverlap: false, lastFailure: 'none'};
  let pre: Record<string, unknown> = {};
  let after: ReturnType<typeof hardwareStatus> | undefined;
  let overlap = false;
  try {
    pre = await preflight(owner, m, o, signal);
    await verifyIdentity(member, m, signal);
    // The dashboard and its execution evidence are owner/admin only.
    await member.api.request(routePath('list_admin_sessions'), 'GET').then(
      () => fault('member_admin_sessions_allowed'),
      error => (error as {status?: unknown}).status === 403 ? undefined : fault('member_admin_sessions_not_403'));

    const states: {client: Client; itemId: string}[] = [];
    for (let index = 0; index < o.sessions; index++) {
      const identity = m.users[index];
      const client = index === 0 ? member : await clientFor(m, o, identity, target, signal);
      if (index) {clients.push(client); await verifyIdentity(client, m, signal);}
      await capability(client, signal);
      states.push({client, itemId: o.itemId ?? identity.itemIds[0]});
    }
    await startAll(states, o, signal, streams);
    const work = new AbortController();
    let sampling = true;
    const baseline = {fallbacks: Number(pre.hardwareFallbacks), refused: Number(pre.conversionsRefused)};
    if (!Number.isFinite(baseline.fallbacks) || !Number.isFinite(baseline.refused)) fault('diagnostics_counters_missing');
    const sampler = sampleDuringDrain(owner, m.serverId, streams, o, baseline, history,
      () => sampling, () => streams.some(stream => !stream.done), signal)
      .catch(error => {work.abort(); throw error;});
    const settled = await Promise.allSettled(streams.map(stream => streamSession(stream, o, AbortSignal.any([signal, work.signal]))
      .catch(error => {work.abort(); throw error;})));
    sampling = false;
    let samplerError: unknown;
    try {await sampler;} catch (error) {samplerError = error;}
    if (samplerError) failures.push({session: '', code: failureCode(samplerError)});
    for (let index = 0; index < settled.length; index++) {
      const result = settled[index];
      if (result.status === 'rejected') failures.push({session: streams[index].session.id, code: failureCode(result.reason)});
    }
    overlap = history.drainOverlap && history.overlapSamples >= 2 && !samplerError;
    if (!failures.length) {
      // A final page is supplementary confirmation, never the overlap proof.
      try {
        const page = await adminPage(owner, m.serverId, signal);
        for (const stream of streams) {
          const row = page.find(item => item.id === stream.session.id);
          if (!row) fault('final_dashboard_row_missing');
          const execution = executionOf(row);
          if (!execution) fault('final_execution_missing');
          const observedFailures = executionFailures(execution, o.renderNode);
          if (observedFailures.length) fault(observedFailures[0]);
          rowEvidence(row, o);
        }
        const finalStatus = hardwareStatus(await diagnostics(owner, m.serverId, signal));
        if (finalStatus.fallbacks !== baseline.fallbacks) fault('hardware_fallback_observed');
        if (finalStatus.refused !== baseline.refused) fault('conversion_refused_during_fixture');
      } catch (error) {failures.push({session: '', code: failureCode(error)});}
    }
  } catch (error) {
    failures.push({session: '', code: failureCode(error)});
  } finally {
    await stopStreams(streams);
    for (const client of clients) {client.coordinator.dispose(); liveClients.delete(client);}
  }
  // Read the counters after the sessions are stopped: fallback/refusal changes
  // belong to this fixture, and the active count must be back to its baseline.
  try {
    after = hardwareStatus(await diagnostics(owner, m.serverId, AbortSignal.timeout(10000)));
    if (typeof pre.hardwareFallbacks === 'number' && after.fallbacks !== pre.hardwareFallbacks)
      failures.push({session: '', code: 'hardware_fallback_observed'});
    if (typeof pre.conversionsRefused === 'number' && after.refused !== pre.conversionsRefused)
      failures.push({session: '', code: 'conversion_refused_during_fixture'});
  } catch (error) {failures.push({session: '', code: failureCode(error)});}
  for (const stream of streams) if (stream.stopOk === false) failures.push({session: stream.session.id, code: 'session_stop_failed'});
  return {
    kind: 'p2-65-hardware-execution', version: 1, mode: 'execute', rehearsal: o.rehearsal, label: o.label,
    target, serverId: m.serverId, quality: o.quality, byteGoal: o.bytes, requestedSessions: o.sessions,
    directUsers: o.directUsers, startedAt, finishedAt: new Date().toISOString(),
    observed: history,
    preflight: pre, postflight: after ? {backend: after.effective, hardwareFallbacks: after.fallbacks,
      conversionsRefused: after.refused, activeConversionSessions: after.active} : null,
    overlappingConversions: overlap, verdict: failures.length ? 'fail' : 'pass', failures,
    sessions: streams.map(stream => ({
      sessionId: stream.session.id, itemId: stream.itemId, generation: stream.generation,
      accountId: stream.client.identity.accountId, profileId: stream.client.identity.profileId, deviceId: stream.client.identity.deviceId,
      presentation: {container: stream.session.presentation.container, path: '/v1/media/{grant}/master.m3u8'},
      hls: {bytes: stream.bytes, initBytes: stream.initBytes, segments: stream.segments, deliveredMs: stream.positionMs},
      execution: stream.evidence ?? null,
      dashboard: stream.lastRow ? {state: stream.lastRow.state,
        videoHeight: obj(stream.lastRow.delivery) ? stream.lastRow.delivery.videoHeight : undefined} : null,
      stopped: stream.stopOk === true,
    })),
  };
}

export type HardwareReceipt = Readonly<{
  kind: 'p2-65-hardware-execution'; version: 1; mode: 'execute'; rehearsal: boolean; label: string;
  target: string; serverId: string; quality: string; byteGoal: number; requestedSessions: number; directUsers: number;
  startedAt: string; finishedAt: string; observed: Readonly<{
    samples: number; overlapSamples: number; firstOverlapAt?: string; lastOverlapAt?: string;
    maxActiveConversions: number; maxDirectSessions: number; maxDirectAccounts: number; maxHardwareSessions: number;
    drainOverlap: boolean; lastFailure: string; sampleHistory?: Record<string, unknown>[];
  }>;
  overlappingConversions: boolean; verdict: 'pass' | 'fail'; failures: readonly unknown[];
  preflight: unknown; postflight: unknown;
  sessions: readonly Readonly<{
    sessionId: string; hls: Readonly<{bytes: number; initBytes: number; segments: number; deliveredMs: number}>;
    execution: HardwareExecution | null; stopped: boolean;
  }>[];
}>;

/** Consume a hardware child's receipt without trusting it: every accepted fact
 * is re-parsed and the overlap/direct evidence must be present as time data. */
export function validateHardwareReceipt(raw: unknown, expected: {target: string; serverId: string; sessions: number; directUsers: number}): HardwareReceipt {
  if (!obj(raw) || raw.kind !== 'p2-65-hardware-execution' || raw.version !== 1 || raw.mode !== 'execute' || raw.verdict !== 'pass')
    fault('hardware_receipt_invalid');
  if (raw.target !== expected.target || raw.serverId !== expected.serverId) fault('hardware_receipt_identity_mismatch');
  if (raw.requestedSessions !== expected.sessions || raw.directUsers !== expected.directUsers) fault('hardware_receipt_window_mismatch');
  if (!count(raw.byteGoal, 64 * 1024 * 1024) || !Array.isArray(raw.failures) || raw.failures.length) fault('hardware_receipt_failed');
  if (raw.overlappingConversions !== true) fault('hardware_receipt_overlap_missing');
  const observed = obj(raw.observed) ? raw.observed : fault('hardware_receipt_observation_missing');
  if (!count(observed.samples, 1_000_000) || observed.samples < 2 || !count(observed.overlapSamples, 1_000_000)
    || observed.overlapSamples < 2 || observed.drainOverlap !== true) fault('hardware_receipt_overlap_missing');
  if (!count(observed.maxActiveConversions, 4096) || observed.maxActiveConversions < expected.sessions
    || !count(observed.maxHardwareSessions, maxSessions) || observed.maxHardwareSessions < expected.sessions)
    fault('hardware_receipt_overlap_missing');
  if (!count(observed.maxDirectSessions, 100) || observed.maxDirectSessions < expected.directUsers
    || !count(observed.maxDirectAccounts, 100) || observed.maxDirectAccounts < expected.directUsers)
    fault('hardware_receipt_direct_missing');
  if (typeof observed.firstOverlapAt !== 'string' || typeof observed.lastOverlapAt !== 'string'
    || !Number.isFinite(Date.parse(observed.firstOverlapAt)) || !Number.isFinite(Date.parse(observed.lastOverlapAt))
    || Date.parse(observed.lastOverlapAt) - Date.parse(observed.firstOverlapAt) < 500)
    fault('hardware_receipt_overlap_missing');
  if (typeof raw.startedAt !== 'string' || typeof raw.finishedAt !== 'string'
    || !Number.isFinite(Date.parse(raw.startedAt)) || !Number.isFinite(Date.parse(raw.finishedAt))
    || Date.parse(raw.finishedAt) < Date.parse(raw.startedAt)) fault('hardware_receipt_window_mismatch');
  if (!Array.isArray(raw.sessions) || raw.sessions.length !== expected.sessions) fault('hardware_receipt_session_gap');
  for (const session of raw.sessions) {
    if (!obj(session) || typeof session.sessionId !== 'string' || !session.sessionId) fault('hardware_receipt_session_gap');
    const hls = obj(session.hls) ? session.hls : fault('hardware_receipt_session_gap');
    if (!count(hls.bytes, 64 * 1024 * 1024) || hls.bytes < raw.byteGoal || !count(hls.segments, 4096) || hls.segments < 2)
      fault('hardware_receipt_session_gap');
    const execution = parseHardwareExecution(session.execution);
    if (!execution) fault('hardware_receipt_execution_missing');
    const failures = executionFailures(execution);
    if (failures.length) fault(failures[0]);
    if (session.stopped !== true) fault('hardware_receipt_stop_missing');
  }
  const pre = obj(raw.preflight) ? raw.preflight : undefined, post = obj(raw.postflight) ? raw.postflight : undefined;
  if (!pre || !post || !count(pre.hardwareFallbacks) || !count(pre.conversionsRefused)
    || pre.hardwareFallbacks !== post.hardwareFallbacks || pre.conversionsRefused !== post.conversionsRefused)
    fault('hardware_receipt_counters_mismatch');
  return raw as HardwareReceipt;
}

/** An explicit scratch directory, the operator's environment, or the nearest
 * private task scratch above the working directory. Never /tmp or /dev/shm. */
async function scratchDirectory(o: Options): Promise<string> {
  const configured = o.scratch ?? process.env.P2_65_SCRATCH ?? process.env.TMPDIR;
  let base: string | undefined;
  if (configured && configured.trim()) base = resolve(configured);
  else {
    let current = resolve(process.cwd());
    for (let depth = 0; depth < 8; depth++) {
      const candidate = join(current, '.private', 'P2-65', 'scratch');
      const state = await lstat(candidate).catch(() => undefined);
      if (state?.isDirectory() && !state.isSymbolicLink()) {base = candidate; break;}
      const parent = dirname(current);
      if (parent === current) break;
      current = parent;
    }
  }
  if (!base) fault('scratch_directory_required');
  // Scratch must be an operator-owned real directory, never a RAM-backed /tmp
  // or /dev/shm. The caller removes the unique child structurally.
  if (base === '/tmp' || base.startsWith('/tmp/') || base === '/dev/shm' || base.startsWith('/dev/shm/')
    || base === '/run' || base.startsWith('/run/') || base === '/var/run' || base.startsWith('/var/run/'))
    fault('scratch_must_not_be_memory');
  let state: Awaited<ReturnType<typeof lstat>>;
  try {state = await lstat(base);} catch {fault('scratch_directory_unavailable');}
  if (!state.isDirectory() || state.isSymbolicLink() || state.uid !== process.getuid?.()) fault('scratch_directory_invalid');
  return base;
}

/** Offline only: parser, route constants, target rules and manifest custody. Not a server check. */
async function selfCheck(o: Options): Promise<Record<string, unknown>> {
  const checks: {check: string; ok: boolean}[] = [];
  const check = (name: string, ok: boolean) => checks.push({check: name, ok: Boolean(ok)});
  const execution = {launchCount: 1, softwareLaunchCount: 0, unknownLaunchCount: 0, fallbackCount: 0,
    lastLaunch: {sequence: 1, generation: 1, startedAt: '2026-10-28T17:41:09.418Z',
      encoder: {name: 'h264_vaapi', execution: 'hardware'}, decoder: {name: 'hevc_vaapi', execution: 'hardware'},
      renderNode: '/dev/dri/renderD128'}};
  const parsed = parseHardwareExecution(execution);
  check('execution parser accepts a hardware launch', parsed?.lastLaunch.renderNode === '/dev/dri/renderD128');
  check('software launch fails acceptance', !!parsed && executionFailures({...parsed, softwareLaunchCount: 1}, undefined).includes('software_launch_observed'));
  check('unknown launch fails acceptance', !!parsed && executionFailures({...parsed, unknownLaunchCount: 1}, undefined).includes('unknown_launch_observed'));
  check('fallback fails acceptance', !!parsed && executionFailures({...parsed, fallbackCount: 1}, undefined).includes('hardware_fallback_observed'));
  check('missing render node fails acceptance', !!parsed && executionFailures({...parsed, lastLaunch: {...parsed.lastLaunch, renderNode: undefined}}, undefined).includes('render_node_missing'));
  check('render node must match the requested node', !!parsed && executionFailures(parsed, '/dev/dri/renderD129').includes('render_node_mismatch'));
  check('uppercase execution is refused, never repaired', parseHardwareExecution({...execution,
    lastLaunch: {...execution.lastLaunch, encoder: {name: 'x', execution: 'HARDWARE'}}}) === undefined);
  const receiptSession = (sessionId: string) => ({sessionId, hls: {bytes: 2 * 1024 * 1024, initBytes: 1024, segments: 3, deliveredMs: 12000},
    execution, stopped: true});
  const passReceipt = {kind: 'p2-65-hardware-execution', version: 1, mode: 'execute', rehearsal: false, label: 'self-check',
    target: 'http://100.120.50.24:8096', serverId: 'server-test', quality: '720p-high', byteGoal: 2 * 1024 * 1024,
    requestedSessions: 2, directUsers: 80, startedAt: '2026-10-28T18:00:00Z', finishedAt: '2026-10-28T18:05:00Z',
    observed: {samples: 10, overlapSamples: 3, firstOverlapAt: '2026-10-28T18:03:00Z', lastOverlapAt: '2026-10-28T18:03:02Z',
      maxActiveConversions: 2, maxDirectSessions: 80, maxDirectAccounts: 80, maxHardwareSessions: 2, drainOverlap: true, lastFailure: 'none'},
    overlappingConversions: true, verdict: 'pass', failures: [], preflight: {hardwareFallbacks: 0, conversionsRefused: 0},
    postflight: {hardwareFallbacks: 0, conversionsRefused: 0}, sessions: [receiptSession('session-a'), receiptSession('session-b')]};
  const receiptOK = (value: unknown) => {try {validateHardwareReceipt(value, {target: passReceipt.target, serverId: passReceipt.serverId, sessions: 2, directUsers: 80}); return true;} catch {return false;}};
  check('hardware receipt validator accepts a complete pass', receiptOK(passReceipt));
  check('hardware receipt validator refuses a failed verdict', !receiptOK({...passReceipt, verdict: 'fail'}));
  check('hardware receipt validator requires direct evidence', !receiptOK({...passReceipt, observed: {...passReceipt.observed, maxDirectSessions: 0}}));
  check('hardware receipt validator requires overlap time', !receiptOK({...passReceipt, observed: {...passReceipt.observed, overlapSamples: 1}}));
  check('hardware receipt validator refuses software execution', !receiptOK({...passReceipt, sessions: [{...receiptSession('session-a'),
    execution: {...execution, softwareLaunchCount: 1, launchCount: 2}}, receiptSession('session-b')]}));
  check('admin sessions route is the current one', routePath('list_admin_sessions') === '/v1/admin/sessions');
  check('session routes come from the contract table', routePath('start_playback_session') === '/v1/playback/sessions'
    && encodedRoutePath('get_playback_session', {id: 'x'}) === '/v1/playback/sessions/x'
    && routePath('get_admin_playback_diagnostics') === '/v1/admin/playback/diagnostics');
  check('target rejects a LAN address', (() => {try {targetOrigin('http://192.168.2.167:8096', false); return false;} catch {return true;}})());
  check('rehearsal target is loopback only', targetOrigin('http://127.0.0.1:8096', true) === 'http://127.0.0.1:8096');
  check('media references stay inside the grant', mediaPath('/v1/media/grant/master.m3u8', 'segment-000000.ts') === '/v1/media/grant/segment-000000.ts');
  check('absolute media references are refused', (() => {try {mediaPath('/v1/media/grant/master.m3u8', 'http://evil.invalid/x'); return false;} catch {return true;}})());
  check('parent traversal is refused', (() => {try {mediaPath('/v1/media/grant/master.m3u8', '../master.m3u8'); return false;} catch {return true;}})());
  const playlist = parsePlaylist(new TextEncoder().encode('#EXTM3U\n#EXT-X-MAP:URI="init.mp4"\n#EXTINF:4.0,\nsegment-000000.ts\n#EXTINF:4.0,\nsegment-000001.ts\n'));
  check('fmp4 playlist is read', playlist.map === 'init.mp4' && playlist.segments.length === 2);
  check('scratch refuses memory-backed paths', await scratchDirectory({...o, scratch: '/tmp'}).then(() => false, () => true));
  const base = await scratchDirectory(o);
  const scratch = await mkdtemp(join(base, 'p2-65-selfcheck-')).catch(() => fault('scratch_directory_unwritable'));
  try {
    const path = join(scratch, 'manifest.json');
    await writeFile(path, JSON.stringify({version: 1}), {mode: 0o600});
    await chmod(path, 0o600);
    check('private manifest is accepted', await privateManifest(path).then(() => true, () => false));
    await chmod(path, 0o644);
    check('world-readable manifest is refused', await privateManifest(path).then(() => false, () => true));
    await chmod(path, 0o600);
    await symlink(path, join(scratch, 'link.json'));
    check('symlink manifest is refused', await privateManifest(join(scratch, 'link.json')).then(() => false, () => true));
  } finally {await rm(scratch, {recursive: true, force: true});}
  const failures = checks.filter(entry => !entry.ok).map(entry => entry.check);
  return {mode: 'self-check', scope: 'offline parser and gate checks only; no HTTP route or server was exercised and this does not prove the fixture runnable',
    ok: !failures.length, checks: checks.length, failures};
}

export async function main(args = process.argv.slice(2)): Promise<void> {
  const o = options(args);
  if (o.selfCheck) {
    // The batch runtime is exact; another Node version is not the one CI and the
    // load driver use, so refuse rather than measure with a different engine.
    if (process.versions.node !== '24.21.0') fault('node_24_21_0_required');
    const report = await selfCheck(o);
    console.log(JSON.stringify(report, null, 2));
    if (!report.ok) process.exitCode = 1;
    return;
  }
  if (!o.execute) {
    console.log(JSON.stringify({mode: 'plan', state: 'UNRUN', sessions: o.sessions, directUsers: o.directUsers, quality: o.quality,
      target: o.rehearsal ? '127.0.0.1 (loopback rehearsal)' : '100.120.50.24',
      requires: ['explicit --execute', 'private owned 0600 manifest', 'owned 0700 mutation-store directory',
        'a private operator scratch directory for --self-check', 'server allowing conversion',
        'supported hardware backend with a DRM render node', 'one controlled source above the requested height', 'quiet server'],
      refuses: ['CI or the server host for the heavy target', 'software, unknown or fallback launches', 'missing render node',
        'no delivered HLS bytes', 'direct play', 'non-overlapping conversions', 'overwriting a receipt']}));
    return;
  }
  if (process.versions.node !== '24.21.0') fault('node_24_21_0_required');
  if (!o.manifest || !o.target || !o.out || !o.mutationStore) fault('execute_requires_manifest_target_out_mutation_store');
  const target = targetOrigin(o.target, o.rehearsal);
  requireDriver({selfCheck: false, rehearsal: o.rehearsal, target});
  const manifest = await privateManifest(o.manifest);
  validateManifest(manifest, o, target);
  // Reserve the receipt before any request. Never overwrite a previous run.
  const out = await open(o.out, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
  const controller = new AbortController();
  const interrupt = () => controller.abort();
  process.once('SIGINT', interrupt);
  process.once('SIGTERM', interrupt);
  let unlock: (() => Promise<void>) | undefined;
  let receipt: Record<string, unknown>;
  try {
    unlock = await lockMutationDirectory(o.mutationStore);
    receipt = await run(manifest, o, target, controller.signal);
  } catch (error) {
    receipt = {kind: 'p2-65-hardware-execution', version: 1, mode: 'execute', rehearsal: o.rehearsal, label: o.label,
      target, verdict: 'fail', failures: [{session: '', code: failureCode(error)}]};
  } finally {
    controller.abort();
    for (const client of liveClients) client.coordinator.dispose();
    liveClients.clear();
    process.removeListener('SIGINT', interrupt);
    process.removeListener('SIGTERM', interrupt);
    await out.writeFile(JSON.stringify(receipt!, null, 2) + '\n'); await out.close(); await unlock?.().catch(() => {});
  }
  console.log(JSON.stringify({mode: 'execute', verdict: receipt.verdict, sessions: Array.isArray(receipt.sessions) ? receipt.sessions.length : 0,
    overlappingConversions: receipt.overlappingConversions, failures: receipt.failures}));
  if (receipt.verdict !== 'pass') process.exitCode = 1;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    // Never emit tokens, grants or a server body. The private receipt, when already reserved, has the code.
    console.error('hardware execution fixture refused: ' + failureCode(error) + ' (see hardware/README.md)');
    process.exitCode = 2;
  });
}
