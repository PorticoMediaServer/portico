import type {ResyncReadContext, ResyncRegistration} from '../resync-coordinator.ts';
/**
 * Server › Overview Now Playing on Playback Protocol v1 (spec §14, be/playback c85d526):
 * `GET /v1/admin/sessions` and `POST /v1/admin/sessions/{id}:terminate`. A server that
 * announces v1 is read through client-core's `NowPlayingStore`; one without it (or one whose
 * v1 list answers 404) keeps the /v2 owner streams list.
 *
 * Refreshing never storms: while the device's v1 event feed is connected the list re-reads on
 * `admin.sessions` / `session.updated` (and a resync), coalesced by the store; otherwise it
 * re-reads every 10 s while the tab is visible, and once when the tab is shown again.
 */
import type {AdminSession} from '../playback-v1/types.ts';
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import {when} from './console-words.ts';
import {ALREADY_DONE, sendWithFreshRecord} from '../freshness-actions.ts';
import type {SessionControls} from '../playback-controls.ts';

export const NOW_PLAYING_POLL_MS = 10_000;
/** The server's limit for the message shown to the viewer (`AdminTerminateMessageMax`). */
export const STOP_MESSAGE_MAX = 500;
/** Row pitch of the windowed list, px; each row's copy is clamped so the pitch is fixed. */
export const NOW_PLAYING_ROW_PX = 68;

type EventsLike = {
  on(type: string, fn: () => void): () => void;
  onResync?(fn: () => void): () => void;
  onResyncRead?(read: (context: ResyncReadContext) => Promise<void>, options?: {open?: boolean}): ResyncRegistration;
  getStatus(): {connected: boolean};
  subscribeStatus(fn: () => void): () => void;
};
type Env = {setTimer: (fn: () => void, ms: number) => unknown; clearTimer: (t: unknown) => void; hidden: () => boolean; onVisibility: (fn: () => void) => () => void};
const browserEnv: Env = {
  setTimer: (fn, ms) => setTimeout(fn, ms),
  clearTimer: t => clearTimeout(t as ReturnType<typeof setTimeout>),
  hidden: () => typeof document !== 'undefined' && document.visibilityState === 'hidden',
  // A client without a document (a phone app) passes its own environment, or simply polls.
  onVisibility: fn => { if (typeof document === 'undefined') return () => {}; document.addEventListener('visibilitychange', fn); return () => document.removeEventListener('visibilitychange', fn); },
};

/** Reads now, then keeps the list current (see the module note). Returns a stop function. */
export function driveNowPlaying(refresh: () => void | Promise<void>, events: EventsLike | undefined, env: Env = browserEnv): () => void {
  let timer: unknown, stopped = false;
  let recovery: ResyncRegistration | undefined;
  const live = () => !!events?.getStatus().connected;
  const arrange = () => {
    env.clearTimer(timer); timer = undefined;
    if (stopped || live() || env.hidden()) return;
    timer = env.setTimer(() => { timer = undefined; if (stopped || live() || env.hidden()) return; refresh(); arrange(); }, NOW_PLAYING_POLL_MS);
  };
  const offs: (() => void)[] = [];
  if (events) {
    offs.push(events.on('admin.sessions', refresh), events.on('session.updated', refresh));
    recovery = events.onResyncRead?.(async context => {if (!stopped && !context.signal.aborted) await refresh();}, {open: !env.hidden()});
    if (recovery) offs.push(() => recovery!.dispose());
    else if (events.onResync) offs.push(events.onResync(refresh));
    let was = live();
    // The status also moves with every event's cursor; only a change of connection matters here.
    offs.push(events.subscribeStatus(() => { const now = live(); if (now !== was) { was = now; arrange(); } }));
  }
  offs.push(env.onVisibility(() => { recovery?.setOpen(!env.hidden()); if (!env.hidden() && !live()) refresh(); arrange(); }));
  refresh();
  arrange();
  return () => { stopped = true; env.clearTimer(timer); for (const off of offs) off(); };
}

/** The rows in view (plus `overscan` either side) of a fixed-pitch list. */
export function windowRange(scrollTop: number, viewport: number, count: number, rowPx = NOW_PLAYING_ROW_PX, overscan = 3): {first: number; last: number} {
  if (count <= 0) return {first: 0, last: -1};
  const first = Math.max(0, Math.floor(Math.max(0, scrollTop) / rowPx) - overscan);
  const last = Math.min(count - 1, Math.ceil((Math.max(0, scrollTop) + Math.max(viewport, rowPx)) / rowPx) + overscan);
  return {first, last};
}

/** How a session is delivered, as a catalogue id: converting beats repackaging beats direct. */
export function deliveryId(decision: AdminSession['decision']): string | undefined {
  const video = decision.video?.action, audio = decision.audio?.action, subtitles = decision.subtitles?.action;
  if (video === 'transcode' || subtitles === 'burn' || video === 'burn') return 'web.nowPlaying.delivery.convertVideo';
  if (audio === 'transcode') return 'web.nowPlaying.delivery.convertAudio';
  if (video === 'copy' || audio === 'copy') return 'web.nowPlaying.delivery.directStream';
  if (video === 'direct' || audio === 'direct') return 'web.nowPlaying.delivery.directPlay';
  return undefined;
}

export function stateId(state: string): string | undefined {
  return state === 'playing' ? 'web.nowPlaying.state.playing' : state === 'paused' ? 'web.nowPlaying.state.paused' : state === 'buffering' || state === 'preparing' ? 'web.nowPlaying.state.starting' : undefined;
}

export function locationId(location: AdminSession['location']): string | undefined {
  return location === 'local' ? 'web.nowPlaying.location.local' : location === 'remote' ? 'web.nowPlaying.location.remote' : undefined;
}

/** Megabits per second, one decimal below 10 (the delivered rate, else the chosen bitrate). */
export function rateMbps(session: Pick<AdminSession, 'bandwidthKbps' | 'bitrateKbps'>): string | undefined {
  const kbps = session.bandwidthKbps || session.bitrateKbps;
  if (!kbps || kbps <= 0) return undefined;
  const mbps = kbps / 1000;
  return mbps >= 10 ? String(Math.round(mbps)) : mbps.toFixed(1);
}

/** The optional message for the viewer: trimmed, at most 500 characters, empty means none. */
export function stopMessage(text: string): string | undefined {
  const t = text.trim();
  return t ? [...t].slice(0, STOP_MESSAGE_MAX).join('') : undefined;
}

type Translate = (id: MessageId, values?: MessageValues) => string;

/** Username first, followed by the session profile only when it differs. */
export function sessionViewer(x: AdminSession, t: Translate): string {
  return x.profile && x.user && x.profile !== x.user ? t('web.nowPlaying.viewerWithAccount', {profile: x.profile, account: x.user}) : x.profile || x.user || t('web.nowPlaying.viewer');
}

/** One session's line under its title: who, on what, how it is delivered, how fast, since when. */
export function sessionFacts(x: AdminSession, t: Translate): string {
  const parts = [sessionViewer(x, t)];
  if (x.device) parts.push(t('web.nowPlaying.onDevice', {device: x.device}));
  const state = stateId(x.state);
  if (state) parts.push(t(state as MessageId));
  // Where playback is, for something with a timeline: what Back and Forward are measured from.
  if ((x.kind === 'vod' || x.kind === 'audio') && x.positionMs > 0) parts.push(x.durationMs ? t('web.nowPlaying.positionOf', {position: positionClock(x.positionMs), duration: positionClock(x.durationMs)}) : t('web.nowPlaying.position', {position: positionClock(x.positionMs)}));
  for (const id of [deliveryId(x.decision), locationId(x.location)]) if (id) parts.push(t(id as MessageId));
  const rate = rateMbps(x);
  if (rate) parts.push(t('web.nowPlaying.rate', {rate}));
  const started = x.startedAt ? Date.parse(x.startedAt) : NaN;
  if (Number.isFinite(started)) parts.push(t('web.nowPlaying.started', {time: when(started)}));
  return parts.join('  ');
}

// ── Pause, resume and seek on somebody else's playback ─────────────────────────────────────

/** How far Back and Forward move playback that is on another device. */
export const REMOTE_SKIP_MS = 30_000;
export type TransportCommand = 'pause' | 'resume' | 'back' | 'forward' | 'startOver';

/**
 * What can be asked of playback on another device, in the order a menu lists it: Pause or Resume
 * (whichever applies), then Back, Forward and Start over for something with a timeline. A
 * channel has no position of its own to move, so it is only paused and resumed. `allowed` is the
 * server's own list for the session, when it has been read; nothing it leaves out is offered.
 */
export function transportOffers(x: Readonly<{kind: string; state: string}>, allowed?: readonly string[]): readonly TransportCommand[] {
  const may = (action: string) => !allowed || allowed.includes(action);
  const offers: TransportCommand[] = [];
  if (x.state === 'paused') { if (may('resume')) offers.push('resume'); }
  else if (may('pause')) offers.push('pause');
  if ((x.kind === 'vod' || x.kind === 'audio') && may('seek')) offers.push('back', 'forward', 'startOver');
  return offers;
}

/** The server action a command is sent as. */
export const transportAction = (command: TransportCommand): 'pause' | 'resume' | 'seek' => (command === 'pause' || command === 'resume' ? command : 'seek');

/**
 * Where a seeking command lands from a position, in whole milliseconds; nothing for Pause and
 * Resume. With the length known, Forward stops short of the end rather than running past it.
 */
export function seekTarget(command: TransportCommand, positionMs: number, durationMs?: number): number | undefined {
  const at = Number.isFinite(positionMs) ? Math.max(0, Math.round(positionMs)) : 0;
  const target = command === 'back' ? Math.max(0, at - REMOTE_SKIP_MS) : command === 'forward' ? at + REMOTE_SKIP_MS : command === 'startOver' ? 0 : undefined;
  return target === undefined ? undefined : clampToLength(target, durationMs);
}

/** How near the end a move may land: the last seconds are left to play. */
export const END_MARGIN_MS = 5_000;

/** A position kept inside a known length (short of its last seconds); unchanged when the length is unknown. */
export function clampToLength(positionMs: number, durationMs?: number): number {
  const at = Math.max(0, Math.round(Number.isFinite(positionMs) ? positionMs : 0));
  if (!durationMs || durationMs <= 0) return at;
  return Math.min(at, Math.max(0, durationMs - END_MARGIN_MS));
}

/** How far through playback is, 0–1, for a position bar; undefined without a known length. */
export function playedFraction(x: Readonly<{positionMs: number; durationMs?: number}>): number | undefined {
  if (!x.durationMs || x.durationMs <= 0) return undefined;
  return Math.min(1, Math.max(0, x.positionMs / x.durationMs));
}

/** Whether a position bar is offered: something with a timeline, a known length and leave to seek. */
export function offersScrubber(x: Readonly<{kind: string; durationMs?: number}>, allowed?: readonly string[]): boolean {
  return (x.kind === 'vod' || x.kind === 'audio') && !!x.durationMs && x.durationMs > 0 && (!allowed || allowed.includes('seek'));
}

/** Whether the session already is what a command asks for (someone else pressed it first). */
export const transportDone = (command: TransportCommand, state: string): boolean => (command === 'pause' && state === 'paused') || (command === 'resume' && state === 'playing');

const ended = () => Object.assign(new Error('session_ended'), {code: 'session_ended', status: 410});

/**
 * The owner's Pause, Resume, Back, Forward or Start over on any session. The session's controls
 * are read first (they carry the revision and the position a seek is measured from); when the
 * session moved in between, they are read again and the command is sent again, silently. A
 * session that has ended, or no longer allows the command, ends with `session_ended`.
 */
export async function sendTransport(service: Readonly<{readAdmin(id: string): Promise<SessionControls>; controlAdmin(current: SessionControls, action: 'pause' | 'resume' | 'seek', positionMs?: number): Promise<SessionControls>}>, sessionId: string, command: TransportCommand): Promise<SessionControls> {
  const action = transportAction(command);
  const first = await service.readAdmin(sessionId);
  if (!first.actions.includes(action)) throw ended();
  if (transportDone(command, first.state)) return first;
  let latest = first;
  try {
    const out = await sendWithFreshRecord(first, current => service.controlAdmin(current, action, seekTarget(command, current.positionMs, current.durationMs)), {
      reread: async () => (latest = await service.readAdmin(sessionId)),
      applies: now => (!now.actions.includes(action) ? false : transportDone(command, now.state) ? 'done' : true),
    });
    return out === ALREADY_DONE ? latest : out;
  } catch (error) {
    // It ended while the command was on its way: say that, not that something changed.
    if (!latest.actions.includes(action)) throw ended();
    throw error;
  }
}

/**
 * The owner moved the position bar of a session on another device to `positionMs`. As with the
 * other commands the controls are read first and a moved session is sent again silently; the
 * position is absolute, so a session that moved underneath still lands where the owner pointed.
 */
export async function sendSeekTo(service: Parameters<typeof sendTransport>[0], sessionId: string, positionMs: number): Promise<SessionControls> {
  const first = await service.readAdmin(sessionId);
  if (!first.actions.includes('seek')) throw ended();
  let latest = first;
  try {
    const out = await sendWithFreshRecord(first, current => service.controlAdmin(current, 'seek', clampToLength(positionMs, current.durationMs)), {
      reread: async () => (latest = await service.readAdmin(sessionId)),
      applies: now => now.actions.includes('seek'),
    });
    return out === ALREADY_DONE ? latest : out;
  } catch (error) {
    if (!latest.actions.includes('seek')) throw ended();
    throw error;
  }
}

/** A position as the console shows it: `1:02:03`, or `2:03` under an hour. */
export function positionClock(positionMs: number): string {
  const total = Math.max(0, Math.floor((Number.isFinite(positionMs) ? positionMs : 0) / 1000));
  const h = Math.floor(total / 3600), m = Math.floor((total % 3600) / 60), sec = total % 60;
  const two = (n: number) => String(n).padStart(2, '0');
  return h ? `${h}:${two(m)}:${two(sec)}` : `${m}:${two(sec)}`;
}
