import {sameCastRemote} from '../../../../../packages/client-core/src/cast';
import {sameFence, type PlaybackDock, type DockState} from '../../../../../packages/player-core/src/index';
import {clockText, timelineFacts, type FactPart} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import type {CastDockIdentity, DockRenderOccupant, DockAction, DockCommand, DockHostProps, DockOccupant} from './types';

export function sameDockFence(a:DockCommand['fence']|undefined,b:DockCommand['fence']|undefined):boolean{
 if(a?.kind==='cast-view'||b?.kind==='cast-view')return a?.kind==='cast-view'&&b?.kind==='cast-view'&&a.attachmentId===b.attachmentId&&sameCastRemote(a.remote,b.remote);
 return !!a&&!!b&&sameFence(a,b);
}

/** Text changes follow the actual media source, including a local play without a queue. */
export function dockSourceIdentity(fence: CastDockIdentity | Readonly<{kind: 'local'; playId: string}> | Readonly<{kind: 'queue' | 'channel'; sourceId?: string}>) {
  if(fence.kind==='cast-view')return JSON.stringify([fence.attachmentId,fence.remote.controller,fence.remote.occurrence]);
  return fence.kind === 'local' ? fence.playId : fence.sourceId;
}

export function currentOccupant(dock: PlaybackDock, snapshot: DockState, presentation: DockHostProps['presentation']): DockOccupant | null {
  // A stale React render may not bind new-source artwork/text to an older native surface.
  if (dock.getSnapshot() !== snapshot) return null;
  const occupant = dock.occupant();
  if (!occupant) return null;
  const model = occupant.remote ? presentation.remote : presentation.local;
  return model && sameFence(model.fence, occupant.playback.fence) ? {...occupant, presentation: model} : null;
}

export function dockCommand(occupant: DockRenderOccupant, type: DockAction, positionSeconds?: number): DockCommand {
  return {type, fence: occupant.playback.fence, ...(occupant.remote ? {remote: {id: occupant.remote.id, sequence: occupant.remote.sequence}} : {}), ...(positionSeconds !== undefined ? {positionSeconds} : {})};
}

/** Re-check at dispatch, including a remote sequence that changed since the control rendered. */
export function commandIsCurrent(dock: PlaybackDock, command: DockCommand): boolean {
  if(command.fence.kind==='cast-view')return false;
  const state = dock.getSnapshot();
  if (command.type === 'restore-audio') return !!state.parkedAudio && sameFence(state.parkedAudio.fence, command.fence);
  if (command.type === 'restore-video') return !!state.resumeVideo && sameFence(state.resumeVideo.fence, command.fence);
  if (command.remote) return state.remote?.id === command.remote.id && state.remote.sequence === command.remote.sequence && sameFence(state.remote.playback.fence, command.fence);
  return !state.remote && !!state.local && sameFence(state.local.fence, command.fence);
}

export function commandAllowed(occupant: DockRenderOccupant, type: DockAction): boolean {
  const {capabilities: cap, finished} = occupant.presentation;
  const phase = occupant.remote?.phase;
  if (type === 'dismiss-remote') return !!phase && ['stopped', 'replaced', 'error'].includes(phase);
  if (type === 'continue-here') return phase === 'stopped' && occupant.presentation.positionKnown !== false;
  if (type === 'stop' || type === 'close') return cap.stop;
  if (type === 'open-remote') return phase === 'playing';
  if (type === 'expand' || type === 'collapse') return !phase;
  // Remote-only actions were decided above using their exact phase/capability.
  // Remaining transport controls wait for the actual playing phase.
  if (phase && phase !== 'playing') return false;
  if (type === 'seek') return cap.seek;
  if (type === 'previous' || type === 'next' || type === 'cast') return cap[type];
  if (type === 'pause') return cap.pause;
  if (type === 'play' || type === 'replay') return cap.play && (type !== 'replay' || !!finished);
  return true;
}

export const playbackPosition = (occupant: DockRenderOccupant) => occupant.remote?.positionSeconds ?? occupant.playback.positionSeconds;
export function seekBounds(occupant: DockRenderOccupant) {
  const window = occupant.presentation.seekWindow;
  if (window && Number.isFinite(window.start) && Number.isFinite(window.end) && window.start >= 0 && window.end > window.start) return window;
  const duration = occupant.presentation.durationSeconds;
  return Number.isFinite(duration) && duration! > 0 ? {start: 0, end: duration!} : undefined;
}
export function progressOf(occupant: DockRenderOccupant): number | undefined {
  const bounds = seekBounds(occupant);
  return bounds ? timelineFacts(bounds.end - bounds.start, Math.max(0, playbackPosition(occupant) - bounds.start)).progress : undefined;
}
export function seekTo(occupant: DockRenderOccupant, position: number): DockCommand | undefined {
  const bounds = seekBounds(occupant);
  return bounds && Number.isFinite(position) && commandAllowed(occupant, 'seek') ? dockCommand(occupant, 'seek', Math.min(bounds.end, Math.max(bounds.start, position))) : undefined;
}

/** O-159: a Live start the server reports recoverable (its source isn't responding) says so in the
 * dock's second line while it is still starting and the server keeps trying; once the stream is up
 * (or the start ends) the ordinary identity returns. */
export function liveWaitingIdentity<T extends Readonly<{subtitle: readonly FactPart[]}>>(identity: T,
  playback: Readonly<{channel?: unknown; linearStalled?: boolean; phase: string}>, reason: () => string): T {
  if (!playback.channel || playback.linearStalled !== true || playback.phase === 'ready' || playback.phase === 'ended' || playback.phase === 'error') return identity;
  return {...identity, subtitle: [{id: 'channel-source', text: reason(), role: 'status' as const}]};
}

export function remoteSubtitle(occupant: DockRenderOccupant): readonly FactPart[] {
  const remote = occupant.remote;
  if (!remote) return occupant.presentation.identity.subtitle;
  const device = occupant.presentation.device ?? defaultI18n.t('cast.theTV');
  const position = occupant.presentation.positionKnown === false ? undefined : clockText(remote.positionSeconds);
  switch (remote.phase) {
    case 'connecting': case 'pairing': return [{id: 'connecting', text: defaultI18n.t('playOn.connectingTo', {device}), role: 'status'}];
    case 'waiting': return [{id: 'waiting', text: defaultI18n.t('cast.waitingForAllow'), role: 'status'}];
    case 'replaced': return [{id: 'replaced', text: defaultI18n.t('cast.replaced'), role: 'status'}];
    case 'error': return [{id: 'error', text: defaultI18n.t('cast.error.unreachable'), role: 'status'}];
    case 'stopped': return position ? [{id: 'stopped', text: defaultI18n.t('next.dock.native.stoppedAt', {position}), role: 'status'}] : [];
    case 'playing': return [{id: 'device', text: device, role: 'context'}, ...(position ? [{id: 'position', text: position, role: 'context' as const}] : [])];
  }
}

/** Release decisions are worklet-safe; no clocks, session writes, or fixed animation restart. */
export function releaseDock(position: number, velocity: number): 0 | 1 {
  'worklet';
  if (velocity < -600) return 0;
  return position > .35 || velocity > 600 ? 1 : 0;
}
export function releaseAside(position: number, velocity: number, width: number): -1 | 0 | 1 {
  'worklet';
  if (!(width > 0) || !Number.isFinite(position) || !Number.isFinite(velocity)) return 0;
  if (Math.abs(velocity) > 800) return velocity > 0 ? 1 : -1;
  return Math.abs(position) > width / 3 ? position > 0 ? 1 : -1 : 0;
}
export function validRect(rect: {x: number; y: number; width: number; height: number}) {
  return Object.values(rect).every(Number.isFinite) && rect.width > 0 && rect.height > 0;
}
/** foldable3f: the medium dock floats at the foot of the content beside the 84 pt
 * rail, no wider than 540 pt. Compact keeps the phone bar above the tab bar. */
export type DockBarFrame = Readonly<{left: number; right: number; bottom: number; maxWidth?: number}>;
/** foldable3f .md .mini: 66 pt tall, 8 pt gaps, 44 pt transport controls, at most 540 pt wide. */
export const mediumDockMetrics = Object.freeze({height: 66, gap: 8, button: 44, maxWidth: 540});
/** tablet-play 4a/13: the expanded dock is 100 tall at the content's foot, beside
 * the sidebar, with a 96 × 54 card, 44 pt tools around a 48 pt centre and a plain
 * 4 pt scrubber with a 44 pt touch zone. */
export const expandedDockMetrics = Object.freeze({height: 100, cardWidth: 96, cardHeight: 54, button: 44, primary: 48, sideLeft: 16, sideRight: 12,
  topInset: 12, bottomInset: 23, transportGap: 6, scrubWidth: 400, scrubHeight: 4, scrubZone: 44, endGap: 0,
  /** The 1180 frame's body and tools, clamped to the bar's own measured width. */
  bodyWidth: 232, toolsWidth: 132, edgeGap: 12, minBody: 108, minScrub: 120});
export function dockBarFrame(platform: 'phone' | 'tablet' | 'web' | 'tv', bottomInset: number | undefined, safeBottom: number, contentInset = 84): DockBarFrame {
  // The expanded window puts the bar at the foot of the content area beside the
  // sidebar; the medium one floats above the home indicator, 16 in from it.
  if (platform === 'web') return {left: contentInset, right: 0, bottom: 0};
  if (platform === 'tablet') return {left: contentInset + 16, right: 16, bottom: 18, maxWidth: mediumDockMetrics.maxWidth};
  return {left: 8, right: 8, bottom: bottomInset ?? Math.max(84, safeBottom) + 8};
}

export const GestureRelease = Object.freeze({dock: releaseDock, aside: releaseAside});
