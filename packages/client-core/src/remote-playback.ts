import type {AccountStream, ReceiverItem, ReceiverRequest as WireRequest, Routes} from '../../contracts/src/next/api.ts';
import {serviceProblem, serviceText} from './presentation/service-text.ts';
import {serverNow} from './server-clock.ts';

/** Playing on another of the account's devices, and being that device (area 8, rules 21 to 30).
 *
 * The sending device asks the other one to play (`POST /v1/receivers/{id}/requests`). The other
 * device's person allows it once per sending sign-in, and the device remembers that itself. The
 * sender keeps playing until the other device reports that it is playing the title; the server
 * then stops the sender's session. A move that is declined, cancelled, refused or not playing
 * within two minutes leaves the sender playing where it was.
 *
 * Afterwards the sender controls the other device through the ordinary session routes: a `PATCH`
 * of that session becomes a command the other device hears on its change feed and carries out
 * through its own player.
 *
 * `HandoffController` is the sending half, `ReceiverHost` the receiving half. Who may send to a
 * device, and what may be played there, is the server's to decide: only the account's own devices,
 * only titles both profiles may see, and only while the device has "Play from other devices" on. */

export type RemoteApi = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;
/** The app's one change feed. Remote playback only subscribes; it never opens a connection. */
export type RemoteEvents = Readonly<{
  on(type: string, listener: (event: Readonly<{resource?: Readonly<{kind: string; id: string}>}>) => void): () => void;
  onResync(listener: () => void): () => void;
  subscribeStatus(listener: () => void): () => void;
  getStatus(): Readonly<{connected: boolean}>;
}>;

/** Event types the change feed's `receiver` and `playback` topics arrive as (server-events.ts). */
export const receiverChangedEvent = 'receiver.changed';
export const playbackChangedEvent = 'playback.changed';

const status = (e: unknown) => (e as {status?: number} | null)?.status ?? 0;
const code = (e: unknown) => {const v = e as {code?: unknown; body?: {code?: unknown}} | null; return typeof v?.code === 'string' ? v.code : typeof v?.body?.code === 'string' ? v.body.code : '';};
const retired = () => new DOMException('Remote playback retired', 'AbortError');
const str = (v: unknown) => typeof v === 'string' ? v : '';
const ms = (v: unknown) => typeof v === 'number' && Number.isFinite(v) && v >= 0 ? Math.round(v) : 0;
/** A route of the rebuilt server by its registered name, with its placeholders filled in. */
type Route = keyof Routes;
const at = (route: Route, params: Readonly<Record<string, string>> = {}) => ({method: route.slice(0, route.indexOf(' ')),
  path: route.slice(route.indexOf(' ') + 1).replace(/\{([a-zA-Z]+)\}/g, (_, name: string) => encodeURIComponent(params[name] ?? ''))});

/** What another device is playing, as the account's playback list shows it. A title the asking
 * profile may not see has no id or name (rule 30). */
export type RemoteStream = Readonly<{
  sessionId: string; deviceName: string; state: 'playing' | 'paused' | 'ended' | 'other';
  itemId?: string; title?: string; positionMs: number; durationMs?: number;
  /** What the device's queue plays after this; absent with no queue, at its last entry, or when
   * the asking profile may not see that title. */
  next?: Readonly<{itemId: string; title: string}>;
}>;
export function parseRemoteStream(value: unknown): RemoteStream | undefined {
  const v = value as Partial<AccountStream> | null;
  if (!v || typeof v !== 'object' || !str(v.id)) return undefined;
  const state = v.state === 'playing' || v.state === 'paused' || v.state === 'ended' ? v.state : 'other';
  const next = (value as {next?: {itemId?: unknown; title?: unknown}}).next;
  return Object.freeze({sessionId: v.id!, deviceName: str(v.deviceName), state, positionMs: ms(v.positionMs),
    ...(str(v.itemId) ? {itemId: v.itemId} : {}), ...(str(v.title) ? {title: v.title} : {}),
    ...(ms(v.durationMs) > 0 ? {durationMs: ms(v.durationMs)} : {}),
    ...(next && str(next.itemId) && str(next.title) ? {next: Object.freeze({itemId: next.itemId as string, title: next.title as string})} : {})});
}

/** One of the account's other open devices. */
export type PlayOnDevice = Readonly<{id: string; displayName: string; platform: string;
  /** It already let this sign-in control it in this run of the server: it will not ask again. */
  allowed: boolean; playing?: RemoteStream}>;
/** Play on's list: every other open device of the account. This device is left out. */
export function parseReceivers(value: unknown): readonly PlayOnDevice[] {
  const items = (value as {items?: unknown} | null)?.items;
  if (!Array.isArray(items)) throw new Error('receiver list');
  const out: PlayOnDevice[] = [];
  for (const raw of items as Partial<ReceiverItem>[]) {
    if (!raw || typeof raw !== 'object' || !str(raw.id) || raw.current === true) continue;
    const playing = parseRemoteStream(raw.playing);
    out.push(Object.freeze({id: raw.id!, displayName: str(raw.name) || serviceText('playOn.porticoDevice'), platform: str(raw.platform),
      allowed: raw.allowed === true, ...(playing ? {playing} : {})}));
  }
  return Object.freeze(out);
}

export type DeviceRequestState = 'asking' | 'allowed' | 'playing' | 'declined' | 'failed' | 'cancelled';
/** What is asked of a device: to play (a move) or to be controlled. */
export type DeviceRequest = Readonly<{
  id: string; receiverId: string; kind: 'play' | 'control'; state: DeviceRequestState;
  /** Why it failed: timed_out, couldnt_start or not_accepting (the device stopped accepting). */
  failed?: string;
  from: Readonly<{deviceId: string; deviceName: string; signInId: string}>;
  play?: Readonly<{itemId: string; positionMs: number; audioTrackId?: string; subtitleTrackId?: string; queueId?: string}>;
  /** The session the device plays once it reports playing. */
  sessionId?: string;
  createdAt: string; expiresAt: string;
}>;
const STATES: readonly DeviceRequestState[] = ['asking', 'allowed', 'playing', 'declined', 'failed', 'cancelled'];
export function parseDeviceRequest(value: unknown): DeviceRequest {
  const v = value as Partial<WireRequest> | null;
  if (!v || typeof v !== 'object' || !str(v.id) || (v.kind !== 'play' && v.kind !== 'control') || !STATES.includes(v.state as DeviceRequestState)) throw new Error('device request');
  const from = v.from as Partial<WireRequest['from']> | undefined, play = v.play;
  return Object.freeze({id: v.id!, receiverId: str(v.receiverId), kind: v.kind, state: v.state as DeviceRequestState,
    ...(str(v.failed) ? {failed: v.failed} : {}),
    from: Object.freeze({deviceId: str(from?.deviceId), deviceName: str(from?.deviceName), signInId: str(from?.signInId)}),
    ...(play && str(play.itemId) ? {play: Object.freeze({itemId: play.itemId, positionMs: ms(play.positionMs),
      ...(typeof play.audioTrackId === 'string' ? {audioTrackId: play.audioTrackId} : {}),
      ...(typeof play.subtitleTrackId === 'string' ? {subtitleTrackId: play.subtitleTrackId} : {}),
      ...(str(play.queueId) ? {queueId: play.queueId} : {})})} : {}),
    ...(str(v.sessionId) ? {sessionId: v.sessionId} : {}),
    createdAt: str(v.createdAt), expiresAt: str(v.expiresAt)});
}
const open = (r: DeviceRequest) => r.state === 'asking' || r.state === 'allowed' && r.kind === 'play';

/** Waits `ms`, or less when woken or aborted. */
class Sleeper {
  private wakeUp?: () => void;
  sleep(ms: number, signal: AbortSignal): Promise<void> {
    return new Promise<void>(resolve => {
      if (signal.aborted) {resolve(); return;}
      const done = () => {clearTimeout(t); signal.removeEventListener('abort', done); if (this.wakeUp === done) this.wakeUp = undefined; resolve();};
      const t = setTimeout(done, ms);
      this.wakeUp = done;
      signal.addEventListener('abort', done, {once: true});
    });
  }
  wake() {this.wakeUp?.();}
}

// ── The sending device ──────────────────────────────────────────────

export type HandoffPhase = 'idle' | 'asking' | 'starting' | 'done' | 'failed';
export type HandoffSnapshot = Readonly<{
  receivers: readonly PlayOnDevice[]; directoryPhase: 'idle' | 'loading' | 'ready' | 'error'; directoryError?: string;
  phase: HandoffPhase; target?: PlayOnDevice; message?: string;
  /** The other device's session, once it plays what was sent: what the bar controls. */
  receiverPlaybackId?: string; remote?: RemoteStream; controlling?: boolean;
}>;
/** What this device plays now, which moves with it (rule 26): the title, the place, the audio and
 * subtitle picks (`''` is subtitles off) and the queue. */
export type HandoffSource = Readonly<{playbackId: string; itemId: string; positionSeconds: number; audioTrackId?: string; subtitleTrackId?: string; queueId?: string}>;
/** Checked before and after every call a send or a control makes; false retires it. */
export type RemoteGuard = () => boolean;

/** A move not playing in two minutes has failed (rule 25); a little more covers a slow answer. */
const MOVE_LIMIT_MS = 130_000;

export class HandoffController {
  private readonly api: RemoteApi;
  private readonly events?: RemoteEvents;
  private readonly isCurrent: () => boolean;
  private readonly pollMs: number;
  private state: HandoffSnapshot = Object.freeze<HandoffSnapshot>({receivers: [], directoryPhase: 'idle', phase: 'idle'});
  private readonly listeners = new Set<() => void>();
  private run?: AbortController;
  private asked?: DeviceRequest;
  private readonly sleeper = new Sleeper();
  private controlGuard: RemoteGuard = () => true;
  private controlWork: Promise<boolean> = Promise.resolve(true);
  private readonly off: (() => void)[] = [];

  constructor(options: Readonly<{api: RemoteApi; events?: RemoteEvents; isCurrent?: () => boolean; pollMs?: number}>) {
    this.api = options.api; this.events = options.events; this.isCurrent = options.isCurrent ?? (() => true); this.pollMs = options.pollMs ?? 2000;
    if (this.events) {
      this.off.push(this.events.on(receiverChangedEvent, e => {if (!e.resource?.id || e.resource.id === this.asked?.id) this.sleeper.wake();}));
      this.off.push(this.events.on(playbackChangedEvent, e => {if (this.state.receiverPlaybackId && e.resource?.id === this.state.receiverPlaybackId) void this.refreshRemote();}));
      this.off.push(this.events.onResync(() => {this.sleeper.wake(); if (this.state.receiverPlaybackId) void this.refreshRemote();}));
    }
  }
  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => {this.listeners.add(fn); return () => {this.listeners.delete(fn);};};
  private publish(patch: Partial<HandoffSnapshot>) {this.state = Object.freeze({...this.state, ...patch}); this.listeners.forEach(f => f());}
  dispose() {this.cancel(); for (const off of this.off.splice(0)) off(); this.listeners.clear();}

  private async call<T>(guard: RemoteGuard, route: Route, params?: Readonly<Record<string, string>>, body?: unknown, signal?: AbortSignal): Promise<T> {
    if (!this.isCurrent() || !guard()) throw retired();
    const {path, method} = at(route, params);
    const value = await this.api.request<T>(path, method, body, signal);
    if (!this.isCurrent() || !guard()) throw retired();
    return value;
  }

  /** The account's other devices. A failed refresh keeps the list it had. */
  async refresh(): Promise<void> {
    if (this.state.directoryPhase === 'idle') this.publish({directoryPhase: 'loading'});
    try {
      const receivers = parseReceivers(await this.call(() => true, 'GET /v1/receivers'));
      const remote = this.state.receiverPlaybackId ? receivers.find(r => r.playing?.sessionId === this.state.receiverPlaybackId)?.playing : undefined;
      this.publish({receivers, directoryPhase: 'ready', directoryError: undefined, ...(remote ? {remote} : {})});
    } catch (e) {
      if ((e as Error)?.name === 'AbortError') return;
      this.publish({directoryPhase: this.state.receivers.length ? 'ready' : 'error', directoryError: serviceProblem(e, 'devices', 'playOn.devicesUnavailable', 'load')});
    }
  }

  /** Stops waiting. A request still open is withdrawn, and whatever the other device had started
   * for it is stopped by the server. */
  cancel() {
    this.run?.abort(); this.run = undefined;
    if (this.state.phase !== 'done') this.publish({phase: 'idle', target: undefined, message: undefined});
  }

  /** Resolves true once the other device plays what was sent; the server has then stopped this
   * device's session, and the caller stops its own player. Anything else leaves it playing. */
  async send(target: PlayOnDevice, source: HandoffSource, guard: RemoteGuard = () => true, controlGuard: RemoteGuard = guard): Promise<boolean> {
    this.run?.abort();
    const run = this.run = new AbortController(), signal = run.signal, device = {device: target.displayName};
    const fail = (message: string) => {if (!signal.aborted) this.publish({phase: 'failed', message}); return false;};
    let asked: DeviceRequest | undefined;
    this.asked = undefined;
    this.publish({phase: 'asking', target, message: undefined, receiverPlaybackId: undefined, remote: undefined});
    try {
      const play = {itemId: source.itemId, positionMs: Math.max(0, Math.round(source.positionSeconds * 1000)),
        ...(source.audioTrackId ? {audioTrackId: source.audioTrackId} : {}),
        ...(source.subtitleTrackId !== undefined ? {subtitleTrackId: source.subtitleTrackId} : {}),
        ...(source.queueId ? {queueId: source.queueId} : {})};
      asked = this.asked = parseDeviceRequest(await this.call(guard, 'POST /v1/receivers/{id}/requests', {id: target.id}, {kind: 'play', play, sessionId: source.playbackId}, signal));
      const deadline = Date.now() + MOVE_LIMIT_MS;
      while (open(asked) && !signal.aborted && Date.now() < deadline) {
        this.publish(asked.state === 'asking' ? {phase: 'asking', message: serviceText('playOn.approveOn', device)} : {phase: 'starting', message: undefined});
        await this.sleeper.sleep(this.events?.getStatus().connected ? Math.max(this.pollMs, 5000) : this.pollMs, signal);
        if (signal.aborted) break;
        asked = this.asked = parseDeviceRequest(await this.call(guard, 'GET /v1/receivers/{id}/requests/{requestId}', {id: target.id, requestId: asked.id}, undefined, signal));
      }
      if (signal.aborted || open(asked)) {
        void this.withdraw(target.id, asked);
        return signal.aborted ? false : fail(serviceText('playOn.didNotStartInTime', device));
      }
      switch (asked.state) {
        case 'playing':
          this.controlGuard = controlGuard;
          this.publish({phase: 'done', receiverPlaybackId: asked.sessionId, message: serviceText('playOn.playingOn', device)});
          void this.refreshRemote();
          return true;
        case 'declined': return fail(serviceText('playOn.declined', device));
        case 'failed': return fail(serviceText(asked.failed === 'not_accepting' ? 'playOn.notAccepting' : asked.failed === 'timed_out' ? 'playOn.didNotStartInTime' : 'playOn.couldNotStart', device));
        default: return fail(serviceText('playOn.couldNotStart', device));
      }
    } catch (e) {
      if (asked && open(asked)) void this.withdraw(target.id, asked);
      if (signal.aborted || (e as Error)?.name === 'AbortError') return false;
      // Not found: the device stopped accepting, or the title is one its profile may not see.
      if (status(e) === 404) return fail(serviceText('playOn.couldNotStart', device));
      if (status(e) === 0) return fail(serviceText('playOn.notReachable', device));
      return fail(serviceProblem(e, 'devices', 'playOn.couldNotStart'));
    } finally {
      if (this.run === run) this.run = undefined;
    }
  }
  private async withdraw(receiverId: string, asked: DeviceRequest) {
    try {await this.api.request(at('DELETE /v1/receivers/{id}/requests/{requestId}', {id: receiverId, requestId: asked.id}).path, 'DELETE');} catch {/* it runs out by itself */}
  }

  /** Asks a device to let this one control it, and waits for the answer: at once from a device
   * that remembers this sign-in, else after its person answers. */
  private async askControl(target: PlayOnDevice): Promise<boolean> {
    const signal = new AbortController().signal, deadline = Date.now() + 60_000;
    let asked = this.asked = parseDeviceRequest(await this.call(this.controlGuard, 'POST /v1/receivers/{id}/requests', {id: target.id}, {kind: 'control'}));
    while (asked.state === 'asking' && Date.now() < deadline) {
      await this.sleeper.sleep(this.pollMs, signal);
      asked = this.asked = parseDeviceRequest(await this.call(this.controlGuard, 'GET /v1/receivers/{id}/requests/{requestId}', {id: target.id, requestId: asked.id}));
    }
    return asked.state === 'allowed';
  }

  /** Reads what the other device plays now. False when it no longer plays it. */
  async refreshRemote(): Promise<boolean> {
    const id = this.state.receiverPlaybackId;
    if (!id) return false;
    try {
      const page = await this.call<{items?: unknown[]}>(this.controlGuard, 'GET /v1/playback/sessions');
      if (this.state.receiverPlaybackId !== id) return false;
      const remote = (page.items ?? []).map(parseRemoteStream).find(s => s?.sessionId === id);
      this.publish({remote: remote ?? (this.state.remote ? Object.freeze({...this.state.remote, state: 'ended' as const}) : undefined)});
      return !!remote && remote.state !== 'ended';
    } catch {return false;}
  }
  play() {return this.control({state: 'playing'});}
  pause() {return this.control({state: 'paused'});}
  seek(positionSeconds: number) {
    if (!Number.isFinite(positionSeconds) || positionSeconds < 0) return Promise.resolve(false);
    return this.control({seek: {positionMs: Math.round(positionSeconds * 1000)}});
  }
  next() {return this.control({skip: 'next'});}
  stop() {return this.control();}
  /** A command for the other device: it carries it out itself. A refusal shows the server's words. */
  private control(change?: Readonly<Record<string, unknown>>): Promise<boolean> {
    const id = this.state.receiverPlaybackId;
    if (!id) return Promise.resolve(false);
    return this.controlWork = this.controlWork.catch(() => false).then(async () => {
      if (this.state.receiverPlaybackId !== id) return false;
      this.publish({controlling: true, message: undefined});
      try {
        if (!change) {
          await this.call(this.controlGuard, 'DELETE /v1/playback/sessions/{id}', {id});
          this.publish({receiverPlaybackId: undefined, remote: undefined, phase: 'idle'});
          return true;
        }
        try {await this.call(this.controlGuard, 'PATCH /v1/playback/sessions/{id}', {id}, change);}
        catch (e) {
          // The server forgets who may control a device when it restarts; the device remembers,
          // so it is asked once more and answers by itself (rule 42), and the command is sent again.
          if (status(e) !== 403 || code(e) !== 'control_not_allowed' || !this.state.target || !await this.askControl(this.state.target)) throw e;
          await this.call(this.controlGuard, 'PATCH /v1/playback/sessions/{id}', {id}, change);
        }
        if (this.state.receiverPlaybackId === id) await this.refreshRemote();
        return true;
      } catch (e) {
        if ((e as Error)?.name !== 'AbortError') this.publish({message: serviceProblem(e, 'devices', 'playOn.controlUnavailable', 'action')});
        return false;
      } finally {this.publish({controlling: false});}
    });
  }
}

// ── The receiving device ────────────────────────────────────────────

/** What the receiving device is asked to play. Absent picks are the device's own choice;
 * `subtitleTrackId: ''` is subtitles off. */
export type ReceiverPlay = Readonly<{requestId: string; itemId: string; positionSeconds: number; audioTrackId?: string; subtitleTrackId?: string; queueId?: string}>;
/** A command another device sent to what this device plays. */
export type ReceiverCommand = Readonly<{id: string; state?: 'playing' | 'paused'; seekMs?: number; skip?: 'next' | 'previous'; audioTrackId?: string; subtitleTrackId?: string}>;
/** What the receiving device's player offers. */
export type ReceiverPlayer = Readonly<{
  /** Starts what was sent, with `requestId` on the start so the two devices count as one stream
   * until this one plays. Rejects when it cannot. */
  load(play: ReceiverPlay): Promise<void>;
  state(): Readonly<{itemId: string; playbackId: string; playing: boolean; positionSeconds: number}>;
  /** Carries out a command on the session it plays. */
  command(sessionId: string, command: ReceiverCommand): void;
}>;
/** The sign-ins this device's person allowed, kept by the device itself (rule 23). */
export type AllowedDevices = Readonly<{has(signInId: string): boolean; add(signInId: string): void}>;
export function memoryAllowedDevices(): AllowedDevices {
  const held = new Set<string>();
  return {has: id => held.has(id), add: id => {held.add(id);}};
}

export type ReceiverPhase = 'off' | 'opening' | 'waiting' | 'starting' | 'turned-off' | 'error';
export type ReceiverSnapshot = Readonly<{phase: ReceiverPhase; asking?: DeviceRequest; error?: string}>;

/** A command older than this is not carried out: it was for a moment that has passed. */
const COMMAND_FRESH_MS = 15_000;

export class ReceiverHost {
  private readonly api: RemoteApi;
  private readonly player: ReceiverPlayer;
  private readonly events?: RemoteEvents;
  private readonly allowed: AllowedDevices;
  private readonly isCurrent: () => boolean;
  private readonly pollMs: number;
  private readonly busyPollMs: number;
  private state: ReceiverSnapshot = Object.freeze<ReceiverSnapshot>({phase: 'off'});
  private readonly listeners = new Set<() => void>();
  private run?: AbortController;
  private readonly sleeper = new Sleeper();
  private releaseEvents?: () => void;
  private readonly handled = new Set<string>();
  private taking?: string;
  private reading?: Promise<void>;
  private lastCommand = '';
  /** When to say again that this device accepts playback; 0 is now. */
  private renewAt = 0;

  constructor(options: Readonly<{api: RemoteApi; player: ReceiverPlayer; events?: RemoteEvents; allowed?: AllowedDevices; isCurrent?: () => boolean; pollMs?: number; busyPollMs?: number}>) {
    this.api = options.api; this.player = options.player; this.events = options.events; this.allowed = options.allowed ?? memoryAllowedDevices();
    this.isCurrent = options.isCurrent ?? (() => true); this.pollMs = options.pollMs ?? 30_000; this.busyPollMs = options.busyPollMs ?? 5000;
  }
  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => {this.listeners.add(fn); return () => {this.listeners.delete(fn);};};
  private publish(patch: Partial<ReceiverSnapshot>) {this.state = Object.freeze({...this.state, ...patch}); this.listeners.forEach(f => f());}
  private async call<T>(signal: AbortSignal, route: Route, params?: Readonly<Record<string, string>>, body?: unknown): Promise<T> {
    if (signal.aborted || !this.isCurrent()) throw retired();
    const {path, method} = at(route, params);
    const value = await this.api.request<T>(path, method, body, signal);
    if (signal.aborted || !this.isCurrent()) throw retired();
    return value;
  }

  /** Stops accepting playback from other devices; the server stops listing this one. */
  stop() {
    const was = this.run;
    this.releaseEvents?.(); this.releaseEvents = undefined;
    this.run?.abort(); this.run = undefined;
    this.publish({phase: 'off', asking: undefined});
    if (was && this.isCurrent()) void this.api.request(at('DELETE /v1/receivers/current').path, 'DELETE').catch(() => {});
  }
  dispose() {this.stop(); this.listeners.clear();}

  /** Accepts playback from the account's other devices until stopped. It says so again as the
   * server asks, reads what is asked of it when the change feed says so (and every so often
   * without), and keeps trying while the server cannot be reached. */
  start() {
    if (this.run) return;
    const run = this.run = new AbortController(), signal = run.signal;
    if (this.events) this.followEvents(signal);
    void (async () => {
      let failures = 0, renewEvery = 60_000;
      this.renewAt = 0;
      if (this.state.phase === 'off') this.publish({phase: 'opening'});
      while (!signal.aborted) {
        try {
          if (Date.now() >= this.renewAt) {
            const opened = await this.call<{renewEveryMs?: number}>(signal, 'PUT /v1/receivers/current');
            renewEvery = Math.min(Math.max(ms(opened.renewEveryMs) || 60_000, 5000), 120_000);
            this.renewAt = Date.now() + renewEvery;
            if (this.state.phase === 'opening' || this.state.phase === 'error') this.publish({phase: 'waiting', error: undefined});
          }
          await this.readRequests(signal);
          await this.readCommand(signal);
          failures = 0;
        } catch (e) {
          if (signal.aborted || (e as Error)?.name === 'AbortError') return;
          if (status(e) === 409 && code(e) === 'play_from_other_devices_off') {
            // "Play from other devices" is off for this device on the server: nothing to do here.
            this.publish({phase: 'turned-off', asking: undefined, error: undefined});
            this.releaseEvents?.(); this.releaseEvents = undefined;
            if (this.run === run) this.run = undefined;
            return;
          }
          failures++; this.renewAt = 0;
          this.publish({phase: 'error', error: serviceProblem(e, 'devices', 'playOn.receiverOffline', 'load')});
        }
        const busy = !!this.player.state().playbackId || !!this.taking;
        const idle = this.events?.getStatus().connected ? Math.max(this.pollMs, renewEvery) : this.pollMs;
        const wait = failures ? Math.min(60_000, 2000 * 2 ** Math.min(failures, 5)) : Math.min(this.renewAt - Date.now(), busy && !this.events?.getStatus().connected ? this.busyPollMs : idle);
        await this.sleeper.sleep(Math.max(wait, 250), signal);
      }
    })();
  }

  private followEvents(signal: AbortSignal) {
    const events = this.events!;
    const read = () => {if (!signal.aborted) void this.readRequests(signal).catch(() => {});};
    const offs = [
      events.on(receiverChangedEvent, read),
      events.on(playbackChangedEvent, e => {const id = this.player.state().playbackId; if (id && (!e.resource?.id || e.resource.id === id)) void this.readCommand(signal).catch(() => {});}),
      // Read everything again: after a server restart, which forgot this device, it says at once
      // that it accepts playback, so it is listed again within moments (rule 42).
      events.onResync(() => {this.renewAt = 0; this.sleeper.wake();}),
      events.subscribeStatus(() => this.sleeper.wake()),
    ];
    this.releaseEvents = () => {for (const off of offs) off();};
  }

  /** What is asked of this device now. One read at a time; a later wake joins it. */
  private readRequests(signal: AbortSignal): Promise<void> {
    return this.reading ??= (async () => {
      try {
        const page = await this.call<{items?: unknown[]}>(signal, 'GET /v1/receivers/current/requests');
        const pending: DeviceRequest[] = [];
        for (const raw of page.items ?? []) {try {pending.push(parseDeviceRequest(raw));} catch {/* a newer shape: skipped */}}
        // The person is asked about one device at a time; one that is no longer asking closes.
        const asking = pending.find(r => r.state === 'asking' && !this.allowed.has(r.from.signInId) && !this.handled.has(r.id));
        if (asking?.id !== this.state.asking?.id) this.publish({asking});
        for (const request of pending) {
          if (this.handled.has(request.id)) continue;
          if (request.state === 'allowed' || this.allowed.has(request.from.signInId)) void this.proceed(request, signal);
        }
      } finally {this.reading = undefined;}
    })();
  }

  /** The person's answer to the device asking now. Allowing is remembered on this device. */
  async decide(request: DeviceRequest, decision: 'allow' | 'decline'): Promise<void> {
    const signal = this.run?.signal;
    if (!signal || this.state.asking?.id !== request.id) return;
    this.publish({asking: undefined});
    if (decision === 'decline') {
      this.handled.add(request.id);
      try {await this.call(signal, 'PATCH /v1/receivers/current/requests/{requestId}', {requestId: request.id}, {answer: 'declined'});} catch {/* it runs out by itself */}
      this.sleeper.wake();
      return;
    }
    if (request.from.signInId) this.allowed.add(request.from.signInId);
    await this.proceed(request, signal);
    this.sleeper.wake();
  }

  /** An allowed request: control needs only the answer; a move starts the title here and says
   * playing once it really plays. */
  private async proceed(request: DeviceRequest, signal: AbortSignal) {
    if (this.handled.has(request.id) || request.kind === 'play' && this.taking) return;
    this.handled.add(request.id);
    if (this.handled.size > 64) this.handled.delete(this.handled.values().next().value!);
    const answer = (body: Readonly<Record<string, unknown>>) => this.call(signal, 'PATCH /v1/receivers/current/requests/{requestId}', {requestId: request.id}, body);
    try {
      if (request.state === 'asking') await answer({answer: 'allowed'});
      if (request.kind !== 'play' || !request.play) return;
    } catch {this.handled.delete(request.id); return;}
    const play = request.play;
    this.taking = request.id;
    this.publish({phase: 'starting'});
    try {
      await this.player.load({requestId: request.id, itemId: play.itemId, positionSeconds: play.positionMs / 1000,
        ...(play.audioTrackId ? {audioTrackId: play.audioTrackId} : {}), ...(play.subtitleTrackId !== undefined ? {subtitleTrackId: play.subtitleTrackId} : {}),
        ...(play.queueId ? {queueId: play.queueId} : {})});
      // The server's two minutes, on this device's own clock.
      const deadline = Date.now() + Math.max(5000, (Date.parse(request.expiresAt) - serverNow()) || 120_000);
      while (!signal.aborted && Date.now() < deadline) {
        const now = this.player.state();
        if (now.itemId === play.itemId && now.playing && now.playbackId) {
          this.lastCommand = '';
          await answer({answer: 'playing', sessionId: now.playbackId});
          return;
        }
        await this.sleeper.sleep(500, signal);
      }
      if (!signal.aborted) await answer({answer: 'failed'});
    } catch (e) {
      if (!signal.aborted && (e as Error)?.name !== 'AbortError') await answer({answer: 'failed'}).catch(() => {});
    } finally {
      if (this.taking === request.id) this.taking = undefined;
      if (!signal.aborted) this.publish({phase: 'waiting'});
    }
  }

  /** The newest command another device sent to what this device plays, carried out once. */
  private async readCommand(signal: AbortSignal) {
    const id = this.player.state().playbackId;
    if (!id) return;
    const session = await this.call<{id?: string; command?: Record<string, unknown>}>(signal, 'GET /v1/playback/sessions/{id}', {id});
    const c = session.command;
    if (!c || session.id !== id || this.player.state().playbackId !== id || !str(c.id) || c.id === this.lastCommand) return;
    this.lastCommand = c.id as string;
    const at = Date.parse(str(c.at));
    if (Number.isFinite(at) && serverNow() - at > COMMAND_FRESH_MS) return;
    this.player.command(id, Object.freeze({id: c.id as string,
      ...(c.state === 'playing' || c.state === 'paused' ? {state: c.state} : {}),
      ...(typeof c.seekMs === 'number' && Number.isFinite(c.seekMs) ? {seekMs: Math.max(0, c.seekMs)} : {}),
      ...(c.skip === 'next' || c.skip === 'previous' ? {skip: c.skip} : {}),
      ...(typeof c.audioTrackId === 'string' ? {audioTrackId: c.audioTrackId} : {}),
      ...(typeof c.subtitleTrackId === 'string' ? {subtitleTrackId: c.subtitleTrackId} : {})}));
  }
}
