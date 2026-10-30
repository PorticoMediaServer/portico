import {defaultI18n} from '@i18n';
import {formatBehindSec} from '@core/presentation/index.ts';
import {sameFence, type ChannelPlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackAuthority, WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import type {LiveChromeDrag, LiveChromePort, LiveChromeWindow} from './live-chrome-types';

const t = defaultI18n.t;
const clamp = (value: number) => Math.min(100, Math.max(0, value));

/** The rail is drawn across the programme's wall-clock span; the bright band is the actual
 * retained window from tuning in to now, the dashed remainder is still ahead of the source. */
export type LiveRail = Readonly<{gone: number; buffer: number; played: number; future: number; edge: number; knob: number}>;
export function liveRail(window: LiveChromeWindow): LiveRail {
  const from = window.programmeStartMs ?? window.seekStartMs, to = window.programmeEndMs ?? window.seekEndMs;
  const span = to - from;
  if (!(span > 0)) return {gone: 0, buffer: 0, played: 0, future: 100, edge: 100, knob: 100};
  const percent = (value: number) => clamp((value - from) / span * 100);
  const start = percent(window.seekStartMs), position = percent(window.positionMs), edge = percent(window.liveEdgeMs);
  return {gone: start, buffer: Math.max(0, edge - start), played: Math.max(0, position - start), future: Math.max(0, 100 - edge), edge, knob: position};
}

/** "8:00 to 8:15 PM" and "4:03 PM" from the same locale/time-zone as the player. */
export function liveTimeRange(startMs: number, endMs: number, format: (ms: number) => string): string {
  const from = format(startMs), to = format(endMs);
  const meridiem = from.match(/ (AM|PM)$/i)?.[1];
  const same = meridiem !== undefined && meridiem === to.match(/ (AM|PM)$/i)?.[1];
  return t('guide.cellTime', {start: same ? from.slice(0, -(meridiem.length + 1)) : from, end: to});
}

/** The slider speaks wall-clock position, programme span and live delay together. */
export function liveRailValueText(window: LiveChromeWindow, programme: LiveChromePort['programme'], format: (ms: number) => string): string {
  return [format(window.positionMs), programme ? liveTimeRange(programme.startMs, programme.endMs, format) : undefined,
    liveBehindLabel(Math.max(0, (window.liveEdgeMs - window.positionMs) / 1000)) ?? t('guide.live')].filter(Boolean).join(', ');
}

export function liveBehindLabel(seconds: number): string | undefined {
  if (!(seconds > 2)) return undefined;
  const duration = seconds < 3600 && seconds % 60 === 0 ? t('next.live.behindMinutes', {count: seconds / 60}) : formatBehindSec(seconds);
  return t('player.live.behindLive', {duration});
}

const actionable = new Set(['scheduled', 'conflicted', 'waiting-source', 'waiting-guide', 'recording', 'preparing', 'finalizing']);

/** The one real command a recording button may send: record, cancel that exact recording, or none. */
export function liveRecordCommand(hasRecording: boolean, state?: string): 'record' | 'cancel' | 'none' {
  if (!hasRecording) return 'record';
  return state !== undefined && actionable.has(state) ? 'cancel' : 'none';
}

export function liveRecordLabel(programme: LiveChromePort['programme']): string | undefined {
  const state = programme?.recording;
  if (!state) return t('guide.record');
  if (state === 'recording' || state === 'scheduled') return t('next.kit.recording');
  if (state === 'conflicted') return t('dvr.state.conflicted');
  if (state === 'waiting-source') return t('dvr.state.waitingSource');
  if (state === 'waiting-guide') return t('dvr.state.waitingGuide');
  if (state === 'preparing') return t('dvr.state.preparing');
  if (state === 'finalizing') return t('dvr.state.finalizing');
  return undefined;
}

export function liveNeighbour(port: LiveChromePort, direction: -1 | 1) {
  return direction === -1 ? port.down : port.up;
}

/** The next programme line is the session's actual next airing, never a title guessed from the guide page. */
export function liveNextLine(port: LiveChromePort): string | undefined {
  return port.nextProgramme ? t('next.live.then', {title: port.nextProgramme.title}) : undefined;
}


/** The wall-clock rail is display only. The channel service takes stream-relative seconds,
 * measured from the exact media origin captured with this window. */
export function liveSeekSeconds(window: LiveChromeWindow, wallClockMs: number): number {
  return (wallClockMs - window.mediaOriginMs) / 1000;
}

/** A pointer drag belongs to the fence it started on. A full-fence replacement during the
 * drag (pointer-down A, rerender B) leaves the captured drag unheld, so pointer-up discards
 * it instead of seeking the channel it never started on. */
export function liveRailDragHeld(drag: LiveChromeDrag | undefined, fence: ChannelPlaybackFence): boolean {
  return !!drag && sameFence(drag.fence, fence);
}

/** The one seek command a held drag may send: the finger's wall-clock value measured from the
 * media origin captured at pointer-down, never from a later render. */
export function liveRailDragSeek(drag: LiveChromeDrag | undefined, fence: ChannelPlaybackFence, wallClockMs: number | undefined): number | undefined {
  return drag && wallClockMs !== undefined && liveRailDragHeld(drag, fence) ? liveSeekSeconds(drag.window, wallClockMs) : undefined;
}

/** Both the retained screen authority and this exact channel fence must still be live. */
export function liveNavigationAllowed(screenCurrent: boolean, portCurrent: boolean): boolean {
  return screenCurrent && portCurrent;
}

/** The runtime snapshot still holds this exact channel fence, authority, source generation
 * and channel association. A replacement or a source generation change is never current. */
export function liveChannelCurrent(fence: ChannelPlaybackFence, authority: WebPlaybackAuthority, live: WebPlaybackRuntimeSnapshot): boolean {
  if (live.disposed || live.authority !== authority || !live.fence || live.fence.kind !== 'channel' || !sameFence(live.fence, fence)) return false;
  const channel = live.playback.channel;
  return !!channel && channel.kind === fence.channelKind && channel.sourceId === fence.sourceId && channel.channelId === fence.channelId
    && channel.generation === fence.channelGeneration && live.playback.linear?.channel.generation === fence.channelGeneration;
}
