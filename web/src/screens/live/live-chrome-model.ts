import {sameFence, type ChannelPlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackAuthority, WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import type {LiveChromePort} from './live-chrome-types';

/** What the Live chrome draws from its port is the kit part's (kit/player/live-model); what follows is the screen's own. */
export * from '../../kit/player/live-model';

const actionable = new Set(['scheduled', 'conflicted', 'waiting-source', 'waiting-guide', 'recording', 'preparing', 'finalizing']);

/** The one real command a recording button may send: record, cancel that exact recording, or none. */
export function liveRecordCommand(hasRecording: boolean, state?: string): 'record' | 'cancel' | 'none' {
  if (!hasRecording) return 'record';
  return state !== undefined && actionable.has(state) ? 'cancel' : 'none';
}

export function liveNeighbour(port: LiveChromePort, direction: -1 | 1) {
  return direction === -1 ? port.down : port.up;
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
