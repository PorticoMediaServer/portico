import {GroupFollower, type GroupPlayer, type GroupSessionService} from '@core/group-session.ts';
import type {PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackScreenLifetime} from './types.ts';

type Options = Readonly<{
  service: GroupSessionService; player: Omit<GroupPlayer, 'load' | 'occurrence'>; isCurrent(): boolean; reportError(error: unknown): void;
  changed(): void;
  load(itemId: string, seconds: number, playing: boolean, context: WebPlaybackScreenLifetime): Promise<void>;
}>;
/** One canonical follower per actual room/revision; it uses the persistent runtime's engine.
 * Room state and the server clock always come from GroupSessionService. */
export function bindRuntimeGroup(options: Options) {
  let disposed = false, key = '', follower: GroupFollower | undefined, loading: AbortController | undefined, adopted: ReturnType<NonNullable<GroupPlayer['occurrence']>> = null;
  const active = () => {const state = options.service.getSnapshot();return !disposed && options.isCurrent() && (state.phase === 'live' || state.phase === 'reconnecting') ? state.group : undefined;};
  const player: GroupPlayer = {...options.player, occurrence: () => adopted, load: (itemId, seconds, playing) => {
    const group = active();if (!group || group.timeline.itemId !== itemId) return;
    loading?.abort();const life = new AbortController();loading = life;
    const entryId = group.timeline.currentEntryId, revision = group.playbackRevision;
    const isCurrent = () => {const live = active();return !life.signal.aborted && live?.id === group.id && live.playbackRevision === revision && live.timeline.currentEntryId === entryId && live.timeline.itemId === itemId;};
    void options.load(itemId, seconds, playing, {signal: life.signal, isCurrent}).then(() => {if (isCurrent()) {adopted = {groupId: group.id, entryId};options.changed();}}).catch(error => {if (isCurrent()) options.reportError(error);});
  }};
  const sync = () => {
    const group = active(), next = group ? `${group.id}:${group.playbackRevision}` : '';
    if (next === key) return;key = next;loading?.abort();loading = undefined;follower?.stop();follower = undefined;
    if (group) {follower = new GroupFollower(options.service, player);follower.start();}
  };
  const off = options.service.subscribe(sync);sync();
  return {
    active, matches: (fence: PlaybackFence) => {const group = active();return fence.kind === 'queue' && group?.timeline.itemId === fence.itemId && adopted?.groupId === group.id && adopted.entryId === group.timeline.currentEntryId;},
    dispose: () => {if (disposed) return;disposed = true;off();loading?.abort();follower?.stop();},
  };
}
export type RuntimeGroupBinding = ReturnType<typeof bindRuntimeGroup>;
