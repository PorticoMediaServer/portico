import {canonicalQualityLadder} from '../../../../contracts/src/types.ts';
import {routePath, encodedRoutePath} from "../../../../contracts/src/route-paths.ts";
/**
 * A fake Portico playback server built from `Spec — Playback Protocol v1.md` (§3–§9, §14, §15, §16)
 * and the API style guide, for client tests before lane C's server exists. In memory; enforces
 * what a client must live with:
 * - idempotent start (422 idempotency_key_reused on a reused key with a different body; spec §17.1), 202 preparation;
 * - `If-Match` on sessions and queues (412 `revision_mismatch` with `current`; §16.2);
 * - generations bump only for byte changes; timeline ignores lower `seq` or other generations (§16.3);
 * - idempotent `DELETE`; lease expiry (§16.4); `replacesSessionId`;
 * - queues from selectors with lazily generated keys (a 10M-entry queue costs nothing), windows
 *   ≤ 200 (§16.8), seeded shuffle with the current entry first, move/remove as tombstones;
 * - device commands and transfers (commit on the target's first playing report; §16.9);
 * - `/v1/events` long-poll (parks until an event or `waitMs`) and a stream opener, with a
 *   bounded ring (`stream.resync` for cursors older than it) and `stream.closed`;
 * - admin Now Playing and terminate.
 * Not for production.
 */
import {ShufflePermutation} from '../shuffle.ts';
import {qualityLadder} from '../quality.ts';

export type FakeResponse = Readonly<{status: number; headers: Readonly<Record<string, string>>; body?: unknown}>;
export type FakeRequest = Readonly<{method: string; path: string; headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>;
export type FakeEvent = {id: string; type: string; at: string; resource?: {kind: string; id: string}; revision?: string; data?: unknown};

type Session = {
  id: string; revision: number; state: 'playing' | 'paused'; kind: 'vod' | 'audio' | 'channel'; itemId: string; versionId: string;
  audio?: unknown; subtitles?: unknown; quality: unknown; generation: number; positionMs: number; deviceId: string;
  /** Player failures reported on this session (each re-plans it another way). */
  failures?: number;
  lastSeq: number; lastActivity: number; ended: boolean; reports: Record<string, unknown>[];
  /** The current observer epoch, and the highest sample seq accepted under it. */
  epoch?: string; observedSeq?: number;
  queue?: {queueId: string; entryId: string}; transferId?: string; startedAt: number; terminatedMessage?: string; endReason?: string;
};
/** A range of one snapshot's keys: `snapshot.first + local` is the entry id's ordinal, so a
 * split (a move inserts beside its target) keeps every surrounding entry's id. */
type Segment = {id: string; snapshot: string; first: number; kind: 'items' | 'selector'; label: string; count: number; keyAt: (i: number) => string; removals: Set<number>};
type Queue = {id: string; revision: number; deviceId: string; ownerKind: 'device' | 'group'; authority: string; accountId: string; profileId: string; updatedAt: number; segments: Segment[]; current: number; repeat: 'off' | 'one' | 'all'; shuffle?: {seed: number; lap: number; domain: number; first: number}; sessionId?: string;
  /** §18: the prepared next entry (private until committed), and committed answers by token. */
  prepared?: {token: string; sessionId: string; sessionGeneration: number; nextPosition: number; nextSessionId: string; expiresAt: number; revision: number};
  committed?: Map<string, FakeResponse>};;
type Transfer = {id: string; sourceId: string; targetDeviceId: string; committed: boolean};

export type FakeServerOptions = Readonly<{
  now?: () => number;
  reportEveryMs?: number;
  leaseMs?: number;
  /** The device making requests (the "current" device). */
  deviceId?: string;
  devices?: readonly {id: string; name: string; form: string}[];
  /** Refuse a start (admission, spec §5.1): return an error code, or undefined to allow. */
  admit?: (body: Record<string, unknown>) => string | undefined;
  /** How many 202 responses a start key gets before 201 (preparation). */
  prepareRounds?: (body: Record<string, unknown>) => number;
  /** Entries a container selector expands to (default 12). Keys are generated, never stored. */
  containerSize?: (kind: string, id: string) => number;
  /** Events kept for resume (default 1000). */
  ring?: number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (t: unknown) => void;
  /** Items that play as audio sessions with a render plan (§18.1; default: ids starting "song"). */
  audioItem?: (itemId: string) => boolean;
  /** An audio item's kind on the wire (`song`, `audiobook_file`…); default `song` for audio, else `movie`. */
  itemKind?: (itemId: string) => string | undefined;
  /** The viewer the listening endpoints answer for (their `scope`). */
  viewer?: Readonly<{serverId: string; authority: string; accountId: string; profileId: string}>;
  /** The player engine every plan names (default `native`). */
  engine?: 'native' | 'mpv';
  /** The viewer's saved default quality that options report (default `original`). */
  savedQuality?: 'original' | 'automatic';
  /** What a non-automatic quality needs the connection to carry: a change whose `throughputKbps`
   * is below it is refused with `connection_too_slow` (default 0: never). */
  neededKbps?: number;
  /** An item's file and facts (e.g. a `fixtures/audio-gapless/` file and its sidecar),
   * served with Range at the plan's `url`. Without one, a synthetic three-minute FLAC plan. */
  audioSource?: (itemId: string) => FakeAudioSource | undefined;
  /** Choose the mode from the device's published `audioDecode` (spec §3, §18.1): `direct`
   * for a declared pair within its limits, else `converted` to FLAC when flac/flac is declared,
   * else `unavailable`. Off by default (every plan is `direct`, as before). */
  modeFromCapabilities?: boolean;
}>;

export type FakeAudioSource = Readonly<{
  bytes: Uint8Array; container: string; codec: string; sampleRate: number; channels: number; durationFrames: number;
  trim: Readonly<{startFrames: number; endFrames: number; source: string}>;
  gain?: Readonly<{trackDb?: number; albumDb?: number; trackPeak?: number; albumPeak?: number; source: string}>;
}>;

/** Strong quoted ETag of the revision; `If-Match` accepts either form (spec §17). */
const etag = (revision: number) => `"${revision}"`;
const unquote = (v: string) => v.replace(/^W\//, '').replace(/^"(.*)"$/, '$1');
const BYTES_CHANGING = ['versionId', 'partIndex', 'audio', 'subtitles', 'quality'] as const;
const WINDOW_MAX = 200;

export class FakePlaybackServer {
  readonly capabilities = new Map<string, unknown>();
  /** §18 counters for tests. */
  preparations = 0;
  commits = 0;
  readonly commands: {deviceId: string; command: Record<string, unknown>}[] = [];
  private sessions = new Map<string, Session>();
  private keys = new Map<string, {body: string; status: number; response: unknown; rounds: number}>();
  private queues = new Map<string, Queue>();
  private queueKeys = new Map<string, {body: string; response: FakeResponse; createdAt: number}>();
  private transfers = new Map<string, Transfer>();
  private events: FakeEvent[] = [];
  private eventSeq = 0;
  private waiters = new Set<() => void>();
  private streams = new Set<{push: (e: FakeEvent) => void; close: (reconnectAfterMs: number) => void; fail: (status: number) => void}>();
  private streamRefusal?: number;
  private failures: (number | 'offline')[] = [];
  private ids = {next: 0};
  private get nextId(): number { return this.ids.next; }
  private set nextId(value: number) { this.ids.next = value; }
  private o: FakeServerOptions & {reportEveryMs: number; leaseMs: number; deviceId: string; ring: number};
  private now: () => number;
  private setTimer: (fn: () => void, ms: number) => unknown;
  private clearTimer: (t: unknown) => void;

  constructor(options: FakeServerOptions = {}) {
    this.o = {reportEveryMs: 10_000, leaseMs: 120_000, deviceId: 'this-device', ring: 1000, ...options};
    this.now = options.now ?? Date.now;
    this.setTimer = options.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = options.clearTimer ?? (t => clearTimeout(t as ReturnType<typeof setTimeout>));
  }

  // ── Test controls ───────────────────────────────────────────────
  failNext(...outcomes: (number | 'offline')[]): void { this.failures.push(...outcomes); }
  session(id: string): Readonly<Session> | undefined { const s = this.sessions.get(id); if (s) this.expire(s); return s; }
  queue(id: string): Readonly<Queue> | undefined { return this.queues.get(id); }
  eventLog(): readonly FakeEvent[] { return this.events; }
  /** As another device (a TV, a phone) for the next requests' `deviceId`. */
  asDevice(deviceId: string): FakePlaybackServer { const view = Object.create(this) as FakePlaybackServer; (view as unknown as {o: FakePlaybackServer['o']}).o = {...this.o, deviceId}; return view; }
  /** A different authenticated viewer on the current device; queue ownership never follows it. */
  asViewer(viewer: NonNullable<FakeServerOptions['viewer']>): FakePlaybackServer { const view = Object.create(this) as FakePlaybackServer; view.o = {...this.o, viewer}; return view; }
  /** The real queue idle and create receipt lifetimes also apply to retained data. */
  sweepQueues(): void {
    for (const q of this.queues.values()) if (q.ownerKind === 'device' && q.updatedAt < this.now() - 90 * 24 * 60 * 60 * 1000) this.queues.delete(q.id);
    for (const [key, receipt] of this.queueKeys) if (receipt.createdAt < this.now() - 24 * 60 * 60 * 1000) this.queueKeys.delete(key);
  }
  /** Publish an event (tests may also publish their own). */
  publish(type: string, resource?: {kind: string; id: string}, revision?: string, data?: unknown): FakeEvent {
    const e: FakeEvent = {id: String(++this.eventSeq), type, at: new Date(this.now()).toISOString(), ...(resource ? {resource} : {}), ...(revision ? {revision} : {}), ...(data !== undefined ? {data} : {})};
    this.events.push(e);
    if (this.events.length > this.o.ring) this.events.shift();
    for (const w of [...this.waiters]) w();
    for (const s of [...this.streams]) s.push(e);
    return e;
  }
  /** End every open stream with `stream.closed`. */
  closeStreams(reconnectAfterMs = 0): void { for (const s of [...this.streams]) s.close(reconnectAfterMs); }
  /** Refuse (or fail) streams with an HTTP status, e.g. 409 `stream_exists`. `undefined` allows them again. */
  refuseStreams(status: number | undefined): void { this.streamRefusal = status; if (status) for (const s of [...this.streams]) s.fail(status); }

  /** A `StreamOpener` over this fake (what a platform's SSE adapter provides). */
  openStream = (lastEventId: string | undefined, onEvent: (e: {id?: string; event?: string; data: string}) => void, signal: AbortSignal): Promise<void> =>
    new Promise((resolve, reject) => {
      if (this.streamRefusal) { reject(Object.assign(new Error('stream refused'), {status: this.streamRefusal})); return; }
      const send = (e: FakeEvent) => onEvent({id: e.id, event: e.type, data: JSON.stringify(e)});
      for (const e of this.since(lastEventId)) send(e);
      const handle = {
        push: send,
        close: (after: number) => { send({id: String(++this.eventSeq), type: 'stream.closed', at: new Date(this.now()).toISOString(), data: {reconnectAfterMs: after}}); },
        fail: (status: number) => { this.streams.delete(handle); reject(Object.assign(new Error('stream failed'), {status})); },
      };
      this.streams.add(handle);
      signal.addEventListener('abort', () => { this.streams.delete(handle); resolve(); }, {once: true});
    });

  /** Events after a cursor; a cursor older than the ring yields one `stream.resync`. */
  private since(after: string | undefined): FakeEvent[] {
    if (after === undefined) return [];
    const n = Number(after);
    const oldest = this.events[0] ? Number(this.events[0].id) : this.eventSeq + 1;
    if (Number.isFinite(n) && n < oldest - 1) return [{id: String(this.eventSeq), type: 'stream.resync', at: new Date(this.now()).toISOString()}];
    return this.events.filter(e => Number(e.id) > n);
  }

  // ── Router ──────────────────────────────────────────────────────
  async handle(r: FakeRequest): Promise<FakeResponse> {
    const failure = this.failures.shift();
    if (failure === 'offline') throw Object.assign(new Error('network unreachable'), {name: 'TypeError'});
    if (typeof failure === 'number') return this.error(failure, failure >= 500 ? 'internal' : 'request_failed');
    const body = (r.body ?? {}) as Record<string, unknown>;
    const m = r.method.toUpperCase();
    const [pathOnly, qs] = r.path.split('?', 2) as [string, string | undefined];
    const q = new URLSearchParams(qs ?? '');
    const h = (name: string) => r.headers?.[name] ?? r.headers?.[name.toLowerCase()];
    let match: RegExpExecArray | null;

    if ((match = /^\/v1\/media\/grant-([^/]+)\/audio$/.exec(pathOnly)) && (m === 'GET' || m === 'HEAD')) return this.audioBytes(match[1]!, h('Range'), m === 'HEAD');
    if (m === 'PUT' && pathOnly === routePath("get_device_capabilities")) { this.capabilities.set(this.o.deviceId, body); return {status: 204, headers: {}}; }
    if (m === 'GET' && pathOnly === routePath("get_events")) return this.poll(q, r.signal);
    // Listening (music and books): the viewer's preferences and an item's context.
    if (m === 'GET' && pathOnly === routePath("get_listening_preferences")) return {status: 200, headers: {}, body: {scope: this.viewerScope(), data: {revision: 1, bookRate: 1, passoutMinutes: 0}}};
    if ((match = /^\/v1\/items\/([^/]+)\/listening$/.exec(pathOnly)) && m === 'GET') {
      const item = decodeURIComponent(match[1]!);
      return {status: 200, headers: {}, body: {scope: this.viewerScope(), data: {itemId: item, libraryId: 'library', kind: this.kindOf(item)}}};
    }
    if (m === 'GET' && pathOnly === routePath('get_me')+'/devices') return {status: 200, headers: {}, body: {items: (this.o.devices ?? []).filter(d => d.id !== this.o.deviceId).map(d => ({...d, nowPlaying: this.nowPlaying(d.id)})), page: {limit: 50}}};
    if ((match = /^\/v1\/items\/([^/]+)\/playback-options$/.exec(pathOnly)) && m === 'GET') return {status: 200, headers: {}, body: this.options(decodeURIComponent(match[1]!), q)};
    if (m === 'POST' && pathOnly === routePath("start_playback_session")) return this.start(h('Idempotency-Key'), body);
    if ((match = /^\/v1\/playback\/sessions\/([^/]+)\/transfers$/.exec(pathOnly)) && m === 'POST') return this.offer(decodeURIComponent(match[1]!), body);
    if ((match = /^\/v1\/playback\/sessions\/([^/]+)(\/observer|\/observation)?$/.exec(pathOnly))) {
      const s = this.sessions.get(decodeURIComponent(match[1]!));
      if (match[2] === '/observer' && m === 'POST') return this.claim(s);
      if (match[2] === '/observation' && m === 'PUT') return this.observation(s, body);
      if (m === 'PATCH') return this.patch(s, h('If-Match'), body);
      if (m === 'DELETE') return this.stop(s, body);
      if (m === 'GET') return s ? {status: 200, headers: {ETag: etag(s.revision)}, body: this.view(s)} : this.error(404, 'session_not_found');
    }
    if ((match = /^\/v1\/devices\/([^/]+)\/commands$/.exec(pathOnly)) && m === 'POST') return this.command(decodeURIComponent(match[1]!), body);
    if (m === 'POST' && pathOnly === routePath("create_queue")) return this.createQueue(h('Idempotency-Key'), body);
    if ((match = /^\/v1\/queues\/([^/:]+)(.*)$/.exec(pathOnly))) return this.queueRoute(m, decodeURIComponent(match[1]!), match[2]!, q, h('If-Match'), body);
    if (m === 'GET' && pathOnly === routePath("list_admin_sessions")) return this.adminList(q);
    if ((match = /^\/v1\/admin\/sessions\/([^/:]+):terminate$/.exec(pathOnly)) && m === 'POST') return this.terminate(decodeURIComponent(match[1]!), body);
    return this.error(404, 'not_found');
  }

  private error(status: number, code: string, extra: Record<string, unknown> = {}): FakeResponse {
    return {status, headers: {}, body: {error: {code, message: code, retry: status === 412 || status === 409 ? 'after_refresh' : status >= 500 ? 'same_request' : 'never', requestId: 'req', ...extra}}};
  }

  // ── Options (§4.1) ──────────────────────────────────────────────
  private options(itemId: string, q: URLSearchParams) {
    const mode = q.get('quality') ?? 'original', qualityId = q.get('qualityId') ?? undefined;
    const limited = mode === 'limit' || mode === 'fixed';
    const audio = q.get('audioId') ?? 'a1';
    return {
      itemId, kind: 'movie', resume: {positionMs: 120_000, partIndex: 0, updatedAt: new Date(this.now()).toISOString()},
      versions: [{
        id: 'v1', label: '4K HDR · Remux', container: 'mkv', durationMs: 7_200_000, sizeBytes: 60e9, bitrateKbps: 60_000,
        parts: [{id: 'p1', index: 0, durationMs: 7_200_000}],
        video: [{id: 'v0', codec: 'hevc', profile: 'main10', bitDepth: 10, width: 3840, height: 2160, fps: 23.976, hdr: 'hdr10'}],
        audio: [{id: 'a1', codec: 'truehd', channels: 8, layout: '7.1', atmos: true, language: 'en', title: 'English', default: true}, {id: 'a2', codec: 'aac', channels: 2, language: 'en', title: 'Commentary', commentary: true}],
        subtitles: [{id: 's3', format: 'pgs', source: 'embedded', language: 'en', title: 'English SDH', sdh: true}, {id: 's4', format: 'srt', source: 'sidecar', language: 'fr', title: 'Français'}],
        futureField: 'ignored by tolerant readers',
      }],
      chapters: [{startMs: 0, title: 'Opening'}, {startMs: 600_000, title: 'Act II'}],
      markers: [{type: 'intro', startMs: 10_000, endMs: 70_000, confidence: 'detected'}, {type: 'mystery', startMs: 1, endMs: 2, confidence: 'unheard-of'}],
      qualities: canonicalQualityLadder.map(q=>q.height?{mode:'fixed',id:q.id,maxHeight:q.height,maxVideoBitrateKbps:q.videoKbps,maxAudioBitrateKbps:q.audioKbps,dataGBPerHour:q.dataGBPerHour,sameAsOriginal:false,applicable:true}:{mode:q.id,applicable:true}),
      preferred: {versionId: 'v1', audioId: 'a1', reason: 'profile_language', quality: {mode: this.o.savedQuality ?? 'original'}},
      plan: {versionId: 'v1', mode: 'stream', engine: this.o.engine ?? 'native', streams: [{id: 'v0', action: limited ? 'transcode' : 'copy', reasons: limited ? ['bitrate_above_request'] : []}, {id: audio, action: audio === 'a1' ? 'transcode' : 'copy', to: audio === 'a1' ? 'eac3' : undefined, reasons: audio === 'a1' ? ['audio_codec_unsupported'] : []}], quality: {mode, ...(qualityId ? {id: qualityId} : {})}},
    };
  }

  // ── Sessions (§5) ───────────────────────────────────────────────
  private view(s: Session) {
    const quality = (s.quality ?? {mode: 'original'}) as {mode?: string; id?: string};
    const transcoded = quality.mode === 'limit' || quality.mode === 'fixed' || quality.mode === 'automatic' && !!quality.id || (s.failures ?? 0) > 1;
    return {
      id: s.id, revision: String(s.revision), kind: s.kind, role: s.transferId ? 'receiver' : 'local', state: s.ended ? 'ended' : s.state, itemId: s.itemId, versionId: s.versionId,
      ...(s.queue ? {queue: s.queue} : {}),
      ...(s.ended ? {end: {reason: s.endReason ?? 'stopped', ...(s.terminatedMessage ? {message: s.terminatedMessage} : {})}} : {}),
      lease: {expiresAt: new Date(s.lastActivity + this.o.leaseMs).toISOString(), reportEveryMs: this.o.reportEveryMs},
      presentation: {
        generation: s.generation, mode: 'stream', url: encodedRoutePath('get_media_grant_file',{grant:'grant-'+s.id,file:String(s.generation)})+'/master.m3u8', startPositionMs: s.positionMs,
        engine: this.o.engine ?? 'native', quality, audioTrackId: (s.audio as {trackId?: string} | undefined)?.trackId ?? 'a1', subtitle: {trackId: 's4', kind: 'text', delivery: 'sidecar'},
        subtitles: [{trackId: 's4', format: 'webvtt', url: encodedRoutePath('get_v1_media_subtitles_grant_resource_revision_vtt',{grant:'grant-'+s.id,resource:'s4',revision:String(s.generation)})}],
        decision: {video: {action: transcoded ? 'transcode' : 'copy', reasons: transcoded ? ['bitrate_above_request'] : []}, audio: {action: 'copy', reasons: []}, subtitles: {action: 'sidecar',reasons:[]}},
        ...(s.kind === 'audio' ? {audioRender: this.renderPlan(s.id, s.generation, s.itemId)} : {}),
      },
    };
  }

  private viewerScope() { return this.o.viewer ?? {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'}; }

  /** Bytes served to a prepared (private) presentation, by its session id: capped at prefetchBytes (§18.1). */
  private prefetched = new Map<string, number>();
  /** Version 2 direct audio: the file with Range (206), or the prepared grant's prefetch cap (403 prepared_limit). */
  private audioBytes(sessionId: string, range: string | undefined, head: boolean): FakeResponse {
    const s = this.sessions.get(sessionId);
    const pending = [...this.queues.values()].map(q => q.prepared).find(p => p?.nextSessionId === sessionId);
    const itemId = s?.itemId ?? (pending ? this.entryAt(this.queues.get([...this.queues.entries()].find(([, q]) => q.prepared === pending)![0])!, pending.nextPosition)?.itemId : undefined);
    if ((!s || s.ended) && !pending || !itemId) return this.error(404, 'not_found');
    const src = this.o.audioSource?.(itemId);
    if (!src) return this.error(404, 'not_found');
    const size = src.bytes.length;
    let start = 0, end = size - 1;
    const r = range && /^bytes=(\d*)-(\d*)$/.exec(range.trim());
    if (range && !r) return {status: 416, headers: {'Content-Range': `bytes */${size}`}};
    if (r) {
      if (r[1] === '') { start = Math.max(0, size - Number(r[2])); } else { start = Number(r[1]); if (r[2] !== '') end = Math.min(size - 1, Number(r[2])); }
      if (start > end || start >= size) return {status: 416, headers: {'Content-Range': `bytes */${size}`}};
    }
    if (!s && pending) {
      const served = (this.prefetched.get(sessionId) ?? 0) + (end - start + 1);
      if (served > Math.min(size, 8 << 20)) return {status: 403, headers: {}, body: {error: {code: 'prepared_limit', retry: 'never', requestId: 'req'}}};
      if (!head) this.prefetched.set(sessionId, served);
    }
    const headers: Record<string, string> = {'Accept-Ranges': 'bytes', 'Content-Length': String(end - start + 1), 'Content-Type': 'application/octet-stream'};
    if (r) headers['Content-Range'] = `bytes ${start}-${end}/${size}`;
    return {status: r ? 206 : 200, headers, body: head ? undefined : src.bytes.subarray(start, end + 1)};
  }

  /** A render plan (§18.1 version 2): the item's file (direct, Range) with its exact trim and gains. */
  renderPlan(sessionId: string, generation: number, itemId = '') {
    const src = this.o.audioSource?.(itemId);
    const bytes = src?.bytes.length ?? 180 * 44100 * 2;
    if (this.o.modeFromCapabilities) {
      const container = src?.container ?? 'flac', codec = src?.codec ?? 'flac', sampleRate = src?.sampleRate ?? 44100, channels = src?.channels ?? 2;
      const decode = ((this.capabilities.get(this.sessions.get(sessionId)?.deviceId ?? this.o.deviceId) as {audioDecode?: unknown} | undefined)?.audioDecode ?? []) as {codec?: string; containers?: string[]; maxSampleRate?: number; sampleRates?: number[]; maxChannels?: number}[];
      const fits = (d: (typeof decode)[number], c: string, k: string, rate: number) => d.codec === k && (d.containers ?? []).includes(c) && (d.sampleRates ? d.sampleRates.includes(rate) : rate <= (d.maxSampleRate ?? 0)) && channels <= (d.maxChannels ?? 0);
      const id = `m-${sessionId}-${generation}`;
      if (!decode.some(d => fits(d, container, codec, sampleRate))) {
        if (!decode.some(d => d.codec === 'flac' && (d.containers ?? []).includes('flac')))
          return {version: 2, mode: 'unavailable', id, reason: 'This device can’t decode this audio, and it declares no format Portico could convert it to.'};
        return {version: 2, mode: 'converted', id, reason: `This device can’t decode ${codec.toUpperCase()}, so Portico converts it to FLAC.`, url: encodedRoutePath('get_media_grant_audio',{grant:'grant-'+sessionId})+'?format=flac',
          container: 'flac', codec: 'flac', sampleRate, channels, durationFrames: src?.durationFrames ?? 180 * 44100, trim: {startFrames: 0, endFrames: 0, source: 'flac'},
          gain: src?.gain ?? {trackDb: -6, albumDb: -5, trackPeak: 0.9, albumPeak: 0.95, source: 'tags'}};
      }
    }
    return {version: 2, mode: 'direct', id: `m-${sessionId}-${generation}`, url: encodedRoutePath('get_media_grant_audio',{grant:'grant-'+sessionId}),
      container: src?.container ?? 'flac', codec: src?.codec ?? 'flac', sampleRate: src?.sampleRate ?? 44100, channels: src?.channels ?? 2,
      bytes, prefetchBytes: Math.min(bytes, 8 << 20), durationFrames: src?.durationFrames ?? 180 * 44100,
      trim: src?.trim ?? {startFrames: 0, endFrames: 0, source: 'flac'},
      gain: src?.gain ?? {trackDb: -6, albumDb: -5, trackPeak: 0.9, albumPeak: 0.95, source: 'tags'}};
  }

  /** The next position completion would play (repeat and shuffle laps as `:advance`), or undefined. */
  private completionNext(q: Queue): number | undefined {
    const total = this.total(q);
    if (q.repeat === 'one') return q.current;
    if (q.current + 1 < total) return q.current + 1;
    if (q.repeat === 'all') return 0;
    return undefined;
  }

  /** Any change to the queue or its current session cancels a preparation (§18.2). */
  private cancelPrepared(sessionId?: string) {
    for (const q of this.queues.values()) if (q.prepared && (sessionId === undefined || q.prepared.sessionId === sessionId)) q.prepared = undefined;
  }

  private expire(s: Session) { if (!s.ended && this.now() - s.lastActivity > this.o.leaseMs) this.end(s, 'lease_expired'); }

  /** Ends a session and says why, like the server: the event and a re-read both carry the reason. */
  private end(s: Session, reason = 'stopped') {
    if (s.ended) return;
    s.ended = true;
    s.endReason = reason;
    this.publish('session.updated', {kind: 'session', id: s.id}, String(++s.revision), {state: 'ended', reason, ...(s.terminatedMessage ? {message: s.terminatedMessage} : {})});
    this.publish('admin.sessions');
  }

  private newSession(itemId: string, fields: Partial<Session>): Session {
    const id = fields.id ?? `s${++this.nextId}`; // a committed preparation reserved its id (§18.3)
    const s: Session = {id, revision: 1, state: 'playing', kind: (this.o.audioItem ?? (i => i.startsWith('song')))(itemId) ? 'audio' : 'vod', itemId, versionId: 'v1', quality: {mode: 'original'}, generation: 1, positionMs: 0, deviceId: this.o.deviceId, lastSeq: 0, lastActivity: this.now(), ended: false, reports: [], startedAt: this.now(), ...fields};
    this.sessions.set(id, s);
    this.publish('admin.sessions');
    return s;
  }

  private start(key: string | undefined, body: Record<string, unknown>): FakeResponse {
    if (!key) return this.error(428, 'idempotency_key_required');
    const canonical = JSON.stringify(body);
    const prior = this.keys.get(key);
    if (prior) {
      if (prior.body !== canonical) return this.error(422, 'idempotency_key_reused');
      if (prior.rounds > 0) { prior.rounds--; return {status: 202, headers: {'Retry-After': '1'}, body: {retryAfterMs: 500}}; }
      if (prior.status === 202) return this.start2(key, body, prior);
      return {status: prior.status, headers: {'Idempotent-Replayed': 'true'}, body: prior.response};
    }
    const refused = this.o.admit?.(body);
    if (refused) return this.error(403, refused);
    const rounds = this.o.prepareRounds?.(body) ?? 0;
    const record = {body: canonical, status: 202, response: undefined as unknown, rounds};
    this.keys.set(key, record);
    if (rounds > 0) { record.rounds--; return {status: 202, headers: {'Retry-After': '1'}, body: {retryAfterMs: 500}}; }
    return this.start2(key, body, record);
  }

  private start2(_key: string, body: Record<string, unknown>, record: {status: number; response: unknown}): FakeResponse {
    let s: Session;
    if (typeof body.transferId === 'string') {
      const t = this.transfers.get(body.transferId);
      const src = t && this.sessions.get(t.sourceId);
      if (!t || !src) return this.error(404, 'transfer_not_found');
      s = this.newSession(src.itemId, {versionId: src.versionId, positionMs: src.positionMs, audio: src.audio, subtitles: src.subtitles, quality: src.quality, queue: src.queue, transferId: t.id});
    } else if (body.queue && typeof (body.queue as {queueId?: unknown}).queueId === 'string') {
      const qref = body.queue as {queueId: string; entryId: string};
      const queue = this.queues.get(qref.queueId);
      if (!queue || !this.ownsQueue(queue)) return this.error(404, 'queue_not_found');
      const itemId = this.itemOfEntry(queue, qref.entryId);
      if (!itemId) return this.error(404, 'queue_not_found');
      s = this.newSession(itemId, {
        queue: qref, state: body.state === 'paused' ? 'paused' : 'playing', positionMs: typeof body.startPositionMs === 'number' ? body.startPositionMs : 0,
        versionId: typeof body.versionId === 'string' ? body.versionId : 'v1', audio: body.audio, subtitles: body.subtitles, quality: body.quality ?? {mode: 'original'},
      });
      queue.sessionId = s.id;
    } else if (typeof body.itemId === 'string' || typeof body.channelId === 'string') {
      s = this.newSession((body.itemId ?? body.channelId) as string, {
        ...(typeof body.channelId === 'string' ? {kind: 'channel' as const} : {}), state: body.state === 'paused' ? 'paused' : 'playing',
        versionId: typeof body.versionId === 'string' ? body.versionId : 'v1', audio: body.audio, subtitles: body.subtitles, quality: body.quality ?? {mode: 'original'},
        positionMs: typeof body.startPositionMs === 'number' ? body.startPositionMs : body.startFrom === 'resume' ? 120_000 : 0,
      });
    } else return this.error(400, 'invalid_request');
    const replaced = typeof body.replacesSessionId === 'string' ? this.sessions.get(body.replacesSessionId) : undefined;
    if (replaced && replaced.deviceId === this.o.deviceId) this.end(replaced, 'replaced');
    record.status = 201;
    record.response = this.view(s);
    return {status: 201, headers: {ETag: etag(s.revision)}, body: record.response};
  }

  private patch(s: Session | undefined, ifMatch: string | undefined, body: Record<string, unknown>): FakeResponse {
    if (!s) return this.error(404, 'session_not_found');
    this.cancelPrepared(s.id); // §18.2: a change to the current session cancels its preparation
    this.expire(s);
    if (s.ended) return this.error(410, 'session_ended');
    if (ifMatch === undefined) return this.error(428, 'revision_required');
    if (unquote(ifMatch) !== String(s.revision)) return {status: 412, headers: {ETag: etag(s.revision)}, body: {error: {code: 'revision_mismatch', retry: 'after_refresh', current: this.view(s), requestId: 'req'}}};
    let bytes = false;
    // Automatic quality is a place on the ladder: a step moves from where the session is.
    const asked = body.quality as {mode?: string; id?: string; step?: string} | undefined;
    if (asked?.mode === 'automatic') {
      const ladder = ['', ...qualityLadder.map(rung => rung.id)], here = (s.quality as {mode?: string; id?: string} | undefined);
      const at = Math.max(0, ladder.indexOf(here?.mode === 'automatic' ? here.id ?? '' : ''));
      const next = asked.step === 'down' ? Math.min(ladder.length - 1, at + 1) : asked.step === 'up' ? Math.max(0, at - 1) : asked.id ? Math.max(0, ladder.indexOf(asked.id)) : at;
      body = {...body, quality: {mode: 'automatic', ...(ladder[next] ? {id: ladder[next]} : {})}};
    }
    // A player failure re-plans the same session another way, until nothing is left (three ways).
    if (body.failure !== undefined) {
      if ((s.failures ?? 0) >= 3) return this.error(422, 'unsupported_media');
      s.failures = (s.failures ?? 0) + 1;
      bytes = true;
    }
    if (typeof body.throughputKbps === 'number' && asked && asked.mode !== 'automatic' && body.throughputKbps < (this.o.neededKbps ?? 0)) return this.error(422, 'connection_too_slow', {reason: '720p'});
    if (typeof body.positionMs === 'number') s.positionMs = body.positionMs;
    for (const k of BYTES_CHANGING) if (k in body && JSON.stringify(body[k]) !== JSON.stringify((s as Record<string, unknown>)[k])) { (s as Record<string, unknown>)[k] = body[k]; bytes = true; }
    if (body.state === 'playing' || body.state === 'paused') s.state = body.state;
    const seek = body.seek as {positionMs?: unknown} | undefined;
    if (seek && typeof seek.positionMs === 'number') { s.positionMs = seek.positionMs; bytes = true; }
    if (bytes) s.generation++;
    s.revision++;
    s.lastActivity = this.now();
    return {status: 200, headers: {ETag: etag(s.revision)}, body: this.view(s)};
  }

  /** Simulate another client changing a session (a remote PATCH), for 412 tests. */
  bump(sessionId: string, change: Record<string, unknown> = {state: 'paused'}): void {
    const s = this.sessions.get(sessionId)!;
    this.patch(s, String(s.revision), change);
    this.publish('session.updated', {kind: 'session', id: s.id}, String(s.revision));
  }

  private epochs = 0;
  private claim(s: Session | undefined): FakeResponse {
    if (!s) return this.error(404, 'session_not_found');
    this.expire(s);
    if (s.ended) return this.error(410, 'session_ended');
    s.epoch = `fake-epoch-${String(++this.epochs).padStart(11, '0')}`;
    s.observedSeq = 0;
    return {status: 200, headers: {}, body: {epoch: s.epoch, everyMs: 1000, leaseUntil: new Date(this.now() + 120_000).toISOString()}};
  }

  /** One observation: the latest wins and is applied at once (the real server writes it in batches). */
  private observation(s: Session | undefined, body: Record<string, unknown>): FakeResponse {
    if (!s) return this.error(404, 'session_not_found');
    this.expire(s);
    if (s.ended) return this.error(410, 'session_ended');
    if (body.epoch !== s.epoch) return this.error(409, 'observation_epoch_changed');
    s.lastActivity = this.now();
    s.reports.push(body);
    if (typeof body.seq === 'number' && body.seq > (s.observedSeq ?? 0) && body.generation === s.generation) {
      s.observedSeq = body.seq;
      s.lastSeq++;
      if (typeof body.positionMs === 'number') s.positionMs = body.positionMs;
      if (body.state === 'playing' || body.state === 'paused') s.state = body.state;
      if (body.state === 'ended') this.end(s);
      // Transfer commit: the target's first playing (or paused) report ends the source (§9.2).
      if (s.transferId && (body.state === 'playing' || body.state === 'paused')) {
        const t = this.transfers.get(s.transferId);
        if (t && !t.committed) { t.committed = true; const src = this.sessions.get(t.sourceId); if (src) this.end(src, 'transferred'); }
      }
    }
    // As the real server does, a channel's ack carries its current view.
    const view = s.kind === 'channel' ? this.view(s) : undefined;
    return {status: 200, headers: {}, body: {acceptedSeq: s.observedSeq ?? 0, leaseUntil: new Date(this.now() + 120_000).toISOString(), ...(view ? {viewTag: JSON.stringify(view).length.toString(36) + ':' + s.revision, session: view} : {})}};
  }

  private stop(s: Session | undefined, body: Record<string, unknown>): FakeResponse {
    if (s) this.cancelPrepared(s.id);
    if (s && !s.ended) {
      if (typeof body.positionMs === 'number') s.positionMs = body.positionMs;
      this.end(s);
    }
    return {status: 204, headers: {}};
  }

  // ── Devices, commands, transfers (§9) ───────────────────────────
  private nowPlaying(deviceId: string) {
    const s = [...this.sessions.values()].find(x => x.deviceId === deviceId && !x.ended);
    return s ? {itemId: s.itemId, state: s.state, positionMs: s.positionMs} : undefined;
  }

  private command(deviceId: string, body: Record<string, unknown>): FakeResponse {
    if (!(this.o.devices ?? []).some(d => d.id === deviceId)) return this.error(404, 'device_not_found');
    if (typeof body.type !== 'string') return this.error(400, 'invalid_request');
    const commandId = `c${++this.nextId}`;
    this.commands.push({deviceId, command: body});
    this.publish('device.command', {kind: 'device', id: deviceId}, undefined, {...body, commandId, fromDeviceId: this.o.deviceId, targetDeviceId: deviceId});
    return {status: 202, headers: {}, body: {commandId}};
  }

  private offer(sessionId: string, body: Record<string, unknown>): FakeResponse {
    const s = this.sessions.get(sessionId);
    if (!s || s.ended) return this.error(404, 'session_not_found');
    if (typeof body.targetDeviceId !== 'string') return this.error(400, 'invalid_request');
    const t: Transfer = {id: `t${++this.nextId}`, sourceId: s.id, targetDeviceId: body.targetDeviceId, committed: false};
    this.transfers.set(t.id, t);
    this.publish('playback.transfer_offered', {kind: 'device', id: t.targetDeviceId}, undefined, {transferId: t.id, fromDeviceId: s.deviceId, itemId: s.itemId});
    return {status: 201, headers: {}, body: {transferId: t.id}};
  }

  // ── Events (§15) ────────────────────────────────────────────────
  private poll(q: URLSearchParams, signal?: AbortSignal): Promise<FakeResponse> | FakeResponse {
    const after = q.get('after') ?? undefined;
    const wait = Math.max(0, Math.min(Number(q.get('waitMs') ?? 25000), 55000));
    const answer = (): FakeResponse => {
      const events = this.since(after ?? String(this.eventSeq));
      return {status: 200, headers: {}, body: {events, nextAfter: events.length ? events[events.length - 1]!.id : after ?? String(this.eventSeq)}};
    };
    if (after !== undefined && this.since(after).length) return answer();
    return new Promise(resolve => {
      let timer: unknown;
      const done = () => { this.waiters.delete(done); this.clearTimer(timer); resolve(answer()); };
      this.waiters.add(done);
      timer = this.setTimer(done, wait);
      signal?.addEventListener('abort', () => { this.waiters.delete(done); this.clearTimer(timer); resolve({status: 499, headers: {}}); }, {once: true});
    });
  }

  // ── Queues (§8) ─────────────────────────────────────────────────
  private segmentFrom(source: Record<string, unknown>): Segment | undefined {
    const id = `g${++this.nextId}`;
    const items = (source.items as {ids?: unknown} | undefined)?.ids;
    if (Array.isArray(items) && items.every(x => typeof x === 'string') && items.length <= 500) {
      const ids = [...items] as string[];
      return {id, snapshot: id, first: 0, kind: 'items', label: `${ids.length} items`, count: ids.length, keyAt: i => ids[i]!, removals: new Set()};
    }
    const c = source.container as {kind?: unknown; id?: unknown} | undefined;
    if (c && typeof c.kind === 'string' && typeof c.id === 'string') {
      const n = this.o.containerSize?.(c.kind, c.id) ?? 12;
      const prefix = `${c.id}-`;
      return {id, snapshot: id, first: 0, kind: 'selector', label: `${c.kind} ${c.id}`, count: n, keyAt: i => prefix + i, removals: new Set()};
    }
    return undefined;
  }

  /** Visible entries in source order: (segment, ordinal) pairs, skipping tombstones. O(removals). */
  private total(q: Queue): number { return q.segments.reduce((n, s) => n + s.count - s.removals.size, 0); }

  private sourceAt(q: Queue, p: number): {seg: Segment; ordinal: number} | undefined {
    for (const seg of q.segments) {
      const live = seg.count - seg.removals.size;
      if (p < live) {
        let ordinal = p;
        for (const r of [...seg.removals].sort((a, b) => a - b)) if (r <= ordinal) ordinal++;
        return {seg, ordinal};
      }
      p -= live;
    }
    return undefined;
  }

  private sourceIndexOf(q: Queue, snapshot: string, ordinal: number): number | undefined {
    let base = 0;
    for (const seg of q.segments) {
      if (seg.snapshot === snapshot && ordinal >= seg.first && ordinal < seg.first + seg.count) {
        const local = ordinal - seg.first;
        if (seg.removals.has(local)) return undefined;
        return base + local - [...seg.removals].filter(r => r < local).length;
      }
      base += seg.count - seg.removals.size;
    }
    return undefined;
  }

  /** The segment holding a source ordinal of a snapshot, and that ordinal relative to it. */
  private segmentAt(q: Queue, snapshot: string, ordinal: number): {index: number; seg: Segment; local: number} | undefined {
    for (let index = 0; index < q.segments.length; index++) {
      const seg = q.segments[index]!;
      if (seg.snapshot === snapshot && ordinal >= seg.first && ordinal < seg.first + seg.count) return {index, seg, local: ordinal - seg.first};
    }
    return undefined;
  }

  /** Resolve an entry id (`snapshot.ordinal`) to its live segment, unless it is tombstoned. */
  private locateEntry(q: Queue, entryId: string): {index: number; seg: Segment; local: number; itemId: string} | undefined {
    const at = this.splitEntry(entryId);
    if (!at) return undefined;
    const found = this.segmentAt(q, at.snapshot, at.ordinal);
    if (!found || found.seg.removals.has(found.local)) return undefined;
    return {...found, itemId: found.seg.keyAt(found.local)};
  }

  private splitEntry(entryId: string): {snapshot: string; ordinal: number} | undefined {
    const dot = entryId.lastIndexOf('.');
    const ordinal = Number(entryId.slice(dot + 1));
    if (dot < 1 || !Number.isSafeInteger(ordinal) || ordinal < 0) return undefined;
    return {snapshot: entryId.slice(0, dot), ordinal};
  }

  /** Split a segment after `cut` local keys: the tail keeps the same snapshot and ordinals. */
  private splitSegment(q: Queue, index: number, cut: number): void {
    const g = q.segments[index]!, tail: Segment = {
      id: `g${++this.nextId}`, snapshot: g.snapshot, first: g.first + cut, kind: g.kind, label: g.label,
      count: g.count - cut, keyAt: i => g.keyAt(cut + i),
      removals: new Set([...g.removals].filter(r => r >= cut).map(r => r - cut)),
    };
    for (const r of [...g.removals]) if (r >= cut) g.removals.delete(r);
    g.count = cut;
    q.segments.splice(index + 1, 0, tail);
  }

  /** Play-order position → source position (shuffle keeps the current entry first; later additions are an unshuffled tail). */
  private orderToSource(q: Queue, p: number): number {
    const sh = q.shuffle;
    if (!sh || p >= sh.domain) return p;
    if (p === 0) return sh.first;
    const perm = new ShufflePermutation(sh.domain, sh.seed, sh.lap);
    const c = perm.indexOf(sh.first);
    return perm.at(p - 1 < c ? p - 1 : p);
  }

  private sourceToOrder(q: Queue, s: number): number {
    const sh = q.shuffle;
    if (!sh || s >= sh.domain) return s;
    if (s === sh.first) return 0;
    const perm = new ShufflePermutation(sh.domain, sh.seed, sh.lap);
    const c = perm.indexOf(sh.first), k = perm.indexOf(s);
    return k < c ? k + 1 : k;
  }

  private kindOf(itemId: string): string {
    return this.o.itemKind?.(itemId) ?? ((this.o.audioItem ?? (i => i.startsWith('song')))(itemId) ? 'song' : 'movie');
  }

  private entryAt(q: Queue, position: number) {
    const src = this.sourceAt(q, this.orderToSource(q, position));
    if (!src) return undefined;
    const itemId = src.seg.keyAt(src.ordinal);
    return {entryId: `${src.seg.snapshot}.${src.seg.first + src.ordinal}`, position, itemId, kind: this.kindOf(itemId), title: `Title ${itemId}`, durationMs: 180_000, available: true};
  }

  private positionOf(q: Queue, entryId: string): number | undefined {
    const at = this.splitEntry(entryId);
    const s = at ? this.sourceIndexOf(q, at.snapshot, at.ordinal) : undefined;
    return s === undefined ? undefined : this.sourceToOrder(q, s);
  }

  private itemOfEntry(q: Queue, entryId: string): string | undefined {
    return this.locateEntry(q, entryId)?.itemId;
  }

  private header(q: Queue) {
    const current = this.entryAt(q, q.current);
    return {
      id: q.id, revision: String(q.revision), total: this.total(q), repeat: q.repeat,
      ...(current ? {current: {entryId: current.entryId, position: q.current}} : {}),
      ...(q.shuffle ? {shuffle: {seed: String(q.shuffle.seed), lap: q.shuffle.lap}} : {}),
      segments: q.segments.map(s => ({id: s.id, kind: s.kind, label: s.label, count: s.count - s.removals.size, state: 'ready'})),
    };
  }

  private window(q: Queue, from: number, limit: number) {
    const total = this.total(q), n = Math.max(0, Math.min(limit, WINDOW_MAX, total - from));
    const items = Array.from({length: n}, (_, i) => this.entryAt(q, from + i)!).filter(Boolean);
    return {items, page: {limit: Math.min(limit, WINDOW_MAX), total, start: from, revision: String(q.revision), ...(from + n < total ? {nextCursor: `from:${from + n}`} : {})}};
  }

  private ownsQueue(q: Queue): boolean {
    const viewer = this.viewerScope();
    return q.authority === viewer.authority && q.accountId === viewer.accountId && q.profileId === viewer.profileId && !/^[~!]/.test(q.deviceId);
  }

  private createQueue(key: string | undefined, body: Record<string, unknown>): FakeResponse {
    if (!key) return this.error(428, 'idempotency_key_required');
    if (body.owner !== undefined && body.owner !== 'device') return this.error(400, 'invalid_request');
    const retain = body.retainQueueIds;
    if (retain != null && (!Array.isArray(retain) || retain.length > 3 || Array.from(retain).some(id => typeof id !== 'string' || id.length < 1 || id.length > 128 || /[^A-Za-z0-9_-]/.test(id)) || new Set(retain).size !== retain.length)) return this.error(400, 'invalid_request');
    const retained = (retain ?? []) as string[];
    const segs = Array.isArray(body.segments) ? body.segments as Record<string, unknown>[] : [];
    if (!segs.length || segs.length > 16) return this.error(400, 'invalid_request');
    const sp = body.startPlayback as {state?: string; quality?: unknown; startPositionMs?: unknown} | undefined;
    if (sp?.state !== undefined && sp.state !== 'playing' && sp.state !== 'paused') return this.error(400, 'invalid_request');
    const viewer = this.viewerScope();
    const {retainQueueIds: _, ...ordinary} = body;
    const semantic = {...ordinary, viewer: {authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId}, ...(retained.length ? {retainQueueIds: [...retained].sort()} : {})};
    const canonical = (v: unknown): unknown => Array.isArray(v) ? v.map(canonical) : v && typeof v === 'object' ? Object.fromEntries(Object.entries(v).sort(([a], [b]) => a.localeCompare(b)).map(([k, value]) => [k, canonical(value)])) : v;
    const digest = JSON.stringify(canonical(semantic)), receiptKey = JSON.stringify([this.o.deviceId, key]);
    const receipt = this.queueKeys.get(receiptKey);
    if (receipt && receipt.createdAt >= this.now() - 24 * 60 * 60 * 1000) return receipt.body === digest ? receipt.response : this.error(422, 'idempotency_key_reused');
    this.sweepQueues();
    for (const id of retained) {
      const old = this.queues.get(id);
      if (!old || old.ownerKind !== 'device' || old.deviceId !== this.o.deviceId || !this.ownsQueue(old)) return this.error(404, 'queue_not_found');
    }
    let id: string;
    do { id = `q${++this.nextId}`; } while (this.queues.has(id));
    const q: Queue = {id, revision: 1, deviceId: this.o.deviceId, ownerKind: 'device', authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId, updatedAt: this.now(), segments: [], current: 0, repeat: 'off'};
    for (const s of segs) { const seg = this.segmentFrom((s.source ?? {}) as Record<string, unknown>); if (!seg) return this.error(422, 'unsupported_selector'); q.segments.push(seg); }
    const first = segs[0]!;
    const order = first.order as {mode?: string; seed?: string} | undefined;
    const anchor = first.anchor as {itemId?: string; position?: number} | string | undefined;
    const total = this.total(q);
    if (!total) return this.error(404, 'queue_not_found');
    let start = 0;
    if (anchor && typeof anchor === 'object' && typeof anchor.position === 'number') start = Math.min(anchor.position, total - 1);
    if (anchor && typeof anchor === 'object' && typeof anchor.itemId === 'string') { for (let i = 0; i < Math.min(total, 10_000); i++) if (this.sourceAt(q, i)!.seg.keyAt(this.sourceAt(q, i)!.ordinal) === anchor.itemId) { start = i; break; } }
    if (order?.mode === 'shuffle') {
      const seed = order.seed !== undefined ? Number(order.seed) >>> 0 : (this.nextId * 2654435761) >>> 0;
      // Shuffle-play from the start picks a uniformly random first entry (ARCH-MEDIA-04); here, from the seed.
      const firstSource = anchor ? start : new ShufflePermutation(total, seed, 1).at(0);
      q.shuffle = {seed, lap: 0, domain: total, first: firstSource};
      q.current = 0;
    } else q.current = start;
    // Commit only after the whole request has been validated and built. Retention
    // exempts exactly these rows; all other queues on the device retire as usual.
    for (const old of this.queues.values()) if (old.ownerKind === 'device' && old.deviceId === this.o.deviceId) {
      if (!retained.includes(old.id)) this.queues.delete(old.id);
      else { old.sessionId = undefined; old.prepared = undefined; }
    }
    this.queues.set(q.id, q);
    let session: Session | undefined;
    if (sp) {
      const cur = this.entryAt(q, q.current)!;
      // A resumed start (a book's saved position): the test says where the session begins.
      const positionMs = typeof sp.startPositionMs === 'number' && sp.startPositionMs >= 0 ? sp.startPositionMs : 0;
      session = this.newSession(cur.itemId, {queue: {queueId: q.id, entryId: cur.entryId}, state: sp.state === 'paused' ? 'paused' : 'playing', quality: sp.quality ?? {mode: 'original'}, positionMs});
      q.sessionId = session.id;
    }
    const w = this.window(q, Math.max(0, q.current - 20), 71);
    const response = {status: 201, headers: {ETag: etag(q.revision)}, body: {queue: this.header(q), window: w.items, ...(session ? {session: this.view(session)} : {})}};
    this.queueKeys.set(receiptKey, {body: digest, response, createdAt: this.now()});
    return response;
  }

  private queueRoute(m: string, id: string, rest: string, qs: URLSearchParams, ifMatch: string | undefined, body: Record<string, unknown>): FakeResponse {
    const q = this.queues.get(id);
    if (!q || !this.ownsQueue(q)) return m === 'DELETE' && rest === '' ? {status: 204, headers: {}} : this.error(404, 'queue_not_found');
    if (m === 'GET' && rest === '') return {status: 200, headers: {ETag: etag(q.revision)}, body: this.header(q)};

    if (m==='GET'&&rest==='/entry') {
      const item=qs.get('itemId');
      if(!item)return this.error(400,'invalid_request');
      for(let position=0;position<this.total(q);position++){const entry=this.entryAt(q,position);if(entry?.itemId===item)return {status:200,headers:{},body:{revision:String(q.revision),entry}};}
      return this.error(404,'not_found');
    }
    if (m === 'GET' && rest === '/entries') {
      const limit = Number(qs.get('limit') ?? 50);
      if (!(limit >= 1)) return this.error(400, 'invalid_limit');
      return {status: 200, headers: {}, body: this.window(q, Math.max(0, Number(qs.get('from') ?? 0)), limit)};
    }
    if (m === 'GET' && rest === '/window') {
      const before = Math.min(Number(qs.get('before') ?? 20), WINDOW_MAX), after = Math.min(Number(qs.get('after') ?? 50), WINDOW_MAX - before);
      const from = Math.max(0, q.current - before);
      return {status: 200, headers: {}, body: this.window(q, from, q.current - from + after + 1)};
    }
    if (m === 'DELETE' && rest === '') { this.queues.delete(id); return {status: 204, headers: {}}; }
    if (m === 'POST' && rest === ':commit-next') return this.commitNext(q, body);
    // Commands: If-Match.
    if (ifMatch === undefined) return this.error(428, 'revision_required');
    if (unquote(ifMatch) !== String(q.revision)) return {status: 412, headers: {}, body: {error: {code: 'revision_mismatch', retry: 'after_refresh', current: this.header(q), requestId: 'req'}}};
    let match: RegExpExecArray | null;
    let session: Session | undefined;
    if (m === 'POST' && rest === ':prepare-next') return this.prepareNext(q, body);
    q.prepared = undefined; // any other command cancels a preparation (§18.2)
    if (m === 'POST' && rest === '/segments') {
      const seg = this.segmentFrom((body.source ?? {}) as Record<string, unknown>);
      if (!seg) return this.error(422, 'unsupported_selector');
      const placement = body.placement as string | {after?: string};
      const currentSrc = this.sourceAt(q, this.orderToSource(q, q.current));
      let at = q.segments.length;
      if (placement === 'next' && currentSrc) at = q.segments.indexOf(currentSrc.seg) + 1;
      if (typeof placement === 'object' && placement?.after) {
        const target = this.locateEntry(q, placement.after);
        if (target) { if (target.local + 1 < target.seg.count) this.splitSegment(q, target.index, target.local + 1); at = target.index + 1; }
      }
      q.segments.splice(at, 0, seg);
    } else if (m === 'DELETE' && (match = /^\/entries\/(.+)$/.exec(rest))) {
      const entryId = decodeURIComponent(match[1]!);
      if (q.shuffle) return this.error(409, 'unsupported_in_fake');
      const located = this.locateEntry(q, entryId);
      if (!located) return this.error(404, 'entry_not_found');
      const pos = this.positionOf(q, entryId)!;
      located.seg.removals.add(located.local);
      if (pos < q.current) q.current--;
    } else if (m === 'POST' && (match = /^\/entries\/(.+):move$/.exec(rest))) {
      if (q.shuffle) return this.error(409, 'unsupported_in_fake');
      const entryId = decodeURIComponent(match[1]!);
      const to = (body.after ?? body.before) as string | undefined;
      const entry = this.locateEntry(q, entryId), target = to ? this.locateEntry(q, to) : undefined;
      if (!entry || !target || entryId === to) return this.error(404, 'entry_not_found');
      const current = this.entryAt(q, q.current)?.entryId;
      entry.seg.removals.add(entry.local);
      const at = this.locateEntry(q, to!);
      if (!at) return this.error(404, 'entry_not_found');
      // Place the moved key beside the target inside its snapshot, as the real server's
      // tombstone-plus-split does; surrounding entries keep their ids.
      let insert = at.index;
      if (body.after) { if (at.local + 1 < at.seg.count) this.splitSegment(q, at.index, at.local + 1); insert = at.index + 1; }
      else if (at.local > 0) { this.splitSegment(q, at.index, at.local); insert = at.index + 1; }
      const id = `g${++this.nextId}`;
      const one: Segment = {id, snapshot: id, first: 0, kind: 'items', label: '1 item', count: 1, keyAt: () => entry.itemId, removals: new Set()};
      q.segments.splice(insert, 0, one);
      if (current) { const p = this.positionOf(q, current); if (p !== undefined) q.current = p; }
    } else if (m === 'PATCH' && rest === '') {
      if (body.repeat === 'off' || body.repeat === 'one' || body.repeat === 'all') q.repeat = body.repeat;
      const sh = body.shuffle as {on?: boolean; seed?: string} | undefined;
      if (sh?.on === true) { const src = this.orderToSource(q, q.current); q.shuffle = {seed: sh.seed !== undefined ? Number(sh.seed) >>> 0 : (this.nextId * 40503) >>> 0, lap: 0, domain: this.total(q), first: src}; q.current = 0; }
      if (sh?.on === false && q.shuffle) { q.current = this.orderToSource(q, q.current); q.shuffle = undefined; }
    } else if (m === 'POST' && rest === ':advance') {
      const total = this.total(q);
      const reason = body.reason;
      if (reason === 'entry' && typeof body.entryId === 'string') { const p = this.positionOf(q, body.entryId); if (p === undefined) return this.error(404, 'entry_not_found'); q.current = p; }
      else if (reason === 'previous') q.current = Math.max(0, q.current - 1);
      else if (q.repeat === 'one' && reason === 'completion') { /* same entry */ }
      else if (q.current + 1 < total) q.current++;
      else if (q.repeat === 'all') { q.current = 0; if (q.shuffle) q.shuffle.lap++; }
      else return this.error(409, 'queue_ended');
      if (q.sessionId) {
        const cur = this.entryAt(q, q.current)!;
        const old = this.sessions.get(q.sessionId);
        // §18.5: the successor inherits state, quality and audio track, even after an `ended` report.
        session = this.newSession(cur.itemId, {queue: {queueId: q.id, entryId: cur.entryId}, ...(old ? {state: old.state, quality: old.quality, audio: old.audio} : {})});
        if (old) this.end(old);
        q.sessionId = session.id;
      }
    } else return this.error(404, 'not_found');
    q.revision++;
    q.updatedAt = this.now();
    this.publish('queue.updated', {kind: 'queue', id: q.id}, String(q.revision), {current: q.current});
    return {status: 200, headers: {ETag: etag(q.revision)}, body: {queue: this.header(q), ...(session ? {session: this.view(session)} : {})}};
  }

  // ── Queue transitions (§18) ─────────────────────────────────────
  private prepareNext(q: Queue, body: Record<string, unknown>): FakeResponse {
    const current = q.sessionId ? this.sessions.get(q.sessionId) : undefined;
    if (current) this.expire(current);
    if (!current || current.ended || body.sessionId !== current.id) return {status: 409, headers: {}, body: {error: {code: 'prepare_not_allowed', reason: 'session_not_current', retry: 'after_refresh', requestId: 'req'}}};
    if (body.sessionGeneration !== current.generation) return {status: 409, headers: {}, body: {error: {code: 'prepare_not_allowed', reason: 'session_changed', retry: 'after_refresh', requestId: 'req'}}};
    if (current.kind !== 'audio') return {status: 409, headers: {}, body: {error: {code: 'prepare_not_allowed', reason: 'not_rendered', retry: 'after_refresh', requestId: 'req'}}};
    const next = this.completionNext(q);
    if (next === undefined) return this.error(409, 'queue_ended');
    const entry = this.entryAt(q, next)!;
    const nextSessionId = `s${++this.nextId}`, token = `pn_${this.nextId}`, expiresAt = this.now() + 60_000;
    q.prepared = {token, sessionId: current.id, sessionGeneration: current.generation, nextPosition: next, nextSessionId, expiresAt, revision: q.revision};
    this.preparations++;
    return {status: 200, headers: {}, body: {token, expiresAt: new Date(expiresAt).toISOString(), entryId: entry.entryId, itemId: entry.itemId,
      presentation: {generation: 1, mode: 'stream', url: '', startPositionMs: 0, subtitles: [], decision: {audio: {action: 'copy', reasons: []}}, audioRender: this.renderPlan(nextSessionId, 1, entry.itemId)}}};
  }

  private commitNext(q: Queue, body: Record<string, unknown>): FakeResponse {
    const token = typeof body.token === 'string' ? body.token : '';
    const replay = q.committed?.get(token);
    if (replay) return replay;
    const p = q.prepared;
    const canceled = (reason: string): FakeResponse => { q.prepared = undefined; return {status: 409, headers: {}, body: {error: {code: 'prepared_canceled', reason, retry: 'never', requestId: 'req'}}}; };
    if (!p || p.token !== token) return this.error(404, 'not_found');
    if (p.expiresAt <= this.now()) { q.prepared = undefined; return this.error(410, 'prepared_expired'); }
    const old = this.sessions.get(p.sessionId);
    if (old) this.expire(old);
    if (!old || old.ended || old.generation !== p.sessionGeneration || q.sessionId !== old.id) return canceled('session_changed');
    if (q.revision !== p.revision) return canceled('queue_changed');
    q.current = p.nextPosition;
    const entry = this.entryAt(q, q.current)!;
    const session = this.newSession(entry.itemId, {id: p.nextSessionId, queue: {queueId: q.id, entryId: entry.entryId}, state: old.state, quality: old.quality, audio: old.audio});
    this.end(old, 'completed');
    q.sessionId = session.id;
    q.prepared = undefined;
    q.revision++;
    q.updatedAt = this.now();
    this.commits++;
    this.publish('queue.updated', {kind: 'queue', id: q.id}, String(q.revision), {current: q.current});
    const response: FakeResponse = {status: 200, headers: {ETag: etag(q.revision)}, body: {queue: this.header(q), session: this.view(session)}};
    (q.committed ??= new Map()).set(token, response);
    return response;
  }

  // ── Admin (§14) ─────────────────────────────────────────────────
  private adminList(q: URLSearchParams): FakeResponse {
    const active = [...this.sessions.values()].filter(s => { this.expire(s); return !s.ended; });
    const limit = Math.max(1, Math.min(Number(q.get('limit') ?? 10), 200));
    const start = Number((q.get('cursor') ?? 'at:0').split(':')[1]);
    active.sort((a,b)=>b.startedAt-a.startedAt || a.id.localeCompare(b.id));
    const page = active.slice(start, start + limit).map(s => ({
      protocol:'v1',id: s.id, user: {id: 'u1', name: 'demo'}, profile: {id: 'p1', name: 'Admin'}, device: {id: s.deviceId, name: s.deviceId}, item: {id: s.itemId, title: `Title ${s.itemId}`,kind:s.kind,art:[]},
      kind: s.kind, state: s.state, positionMs: s.positionMs, decision: this.view(s).presentation.decision, bitrateKbps: 8000, location: 'local', startedAt: new Date(s.startedAt).toISOString(),
    }));
    return {status: 200, headers: {}, body: {scope:{serverId:'fake-server',viewerFence:'a'.repeat(64)},observedAt:new Date(this.now()).toISOString(),totals:{streams:active.length,paused:active.filter(s=>s.state==='paused').length,converting:active.filter(s=>{const d=this.view(s).presentation.decision;return d.video?.action==='transcode' || d.audio?.action==='transcode';}).length,bandwidthKbps:active.length*8000,people:active.length?1:0,atHome:active.length,away:0},items: page, page: {start,revision:'',limit, total: active.length, ...(start + limit < active.length ? {nextCursor: `at:${start + limit}`} : {})}}};
  }

  private terminate(id: string, body: Record<string, unknown>): FakeResponse {
    const s = this.sessions.get(id);
    if (!s || s.ended) return this.error(404, 'session_not_found');
    s.terminatedMessage = typeof body.message === 'string' ? body.message : '';
    this.end(s, 'terminated');
    return {status: 204, headers: {}};
  }
}

/** A client-shaped error for a non-2xx fake response (status and code, like ApiError). */
export function fakeResponseError(r: FakeResponse): Error {
  const code = (r.body as {error?: {code?: string}} | undefined)?.error?.code ?? 'request_failed';
  return Object.assign(new Error(code), {status: r.status, code, body: r.body});
}

/** A `V1Http` over the fake (what the platform's HTTP adapter will be in phase 2). */
export function fakeHttp(server: FakePlaybackServer) {
  return {send: (request: FakeRequest) => server.handle(request)};
}
