import type {PlaybackService, PlaybackSession} from '../../../../../packages/client-core/src/index';
import type {PlaybackSubject} from '../../../../../packages/client-core/src/presentation/kit-model';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import type {PlaybackFence} from '../../../../../packages/player-core/src';
import type {OfflineEntry} from '../offline-downloads';
import type {NativeOfflinePlayback, NativeOfflinePlaybackDevice, NativeOfflineSource} from './offline-playback';
import type {NativeOfflinePlaybackLifetime} from './types';

export const offlineContentKind = (kind: string | undefined) =>
  kind === 'movie' || kind === 'episode' || kind === 'song' || kind === 'audiobook_file' ? kind : undefined;

const audioKind = (kind: string | undefined) => kind === 'song' || kind === 'track' || kind === 'audiobook_file' || kind === 'audiobook';
const kindOf = (kind: string | undefined): 'video' | 'music' | 'audiobook' =>
  kind === 'song' || kind === 'track' ? 'music' : kind === 'audiobook_file' || kind === 'audiobook' ? 'audiobook' : 'video';

export type NativeOfflineHost = Readonly<{
  playback: PlaybackService;
  offline?: NativeOfflinePlayback;
  offlineDevice?: NativeOfflinePlaybackDevice;
  currentAuthority: () => boolean;
  startEpoch: () => number;
  beginOperation: () => number;
  setStartController: (controller: AbortController) => void;
  setOfflineLifetimeOff: (off: (() => void) | undefined) => void;
  clearOfflineLifetimeOffIf: (off: () => void) => void;
  selectionScope: (signal: AbortSignal, current: () => boolean) => Readonly<{signal: AbortSignal; close: () => void}>;
  rememberCurrent: () => void;
  cancelQueue: () => Promise<void>;
  getOfflineSource: () => NativeOfflineSource | undefined;
  bindOffline: (source: NativeOfflineSource, subject: PlaybackSubject) => void;
  setExpanded: (expanded: boolean) => void;
  setPreparing: (preparing: boolean) => void;
  changed: () => void;
  fence: () => PlaybackFence | undefined;
  retryLifetime: () => NativeOfflinePlaybackLifetime;
}>;

export function offlineSubjectFor(source: NativeOfflineSource): PlaybackSubject {
  const contentKind = offlineContentKind(source.metadata.kind);
  if (!contentKind) throw new Error(serviceText('downloads.offline.notPlayableHere'));
  return {kind: kindOf(contentKind), item: {id: source.itemId, title: source.metadata.title, kind: contentKind}};
}

export function makeOfflinePlaybackSession(offline: NativeOfflinePlayback | undefined, playback: PlaybackService, source: NativeOfflineSource): PlaybackSession {
  const session = source.session();
  // The real service has already assigned this play's run. Validate and bind
  // the full local identity before its session can reach the native engine.
  if (!offline?.fence(session, playback.getSnapshot().playbackRun)) throw new Error(serviceText('downloads.offline.notPlayableHere'));
  return session;
}

export async function startOfflinePlayback(host: NativeOfflineHost, entry: OfflineEntry, lifetime: NativeOfflinePlaybackLifetime, startSeconds?: number): Promise<void> {
  const admitted = () => !lifetime.signal.aborted && host.currentAuthority() && lifetime.isCurrent();
  const retired = () => Object.assign(new Error('cancelled'), {name: 'AbortError'});
  if (!admitted()) throw retired();
  if (!host.offline) throw new Error(serviceText('downloads.offline.notPlayableHere'));
  const epoch = host.beginOperation(), controller = new AbortController(); host.setStartController(controller);
  const abort = () => controller.abort();
  lifetime.signal.addEventListener('abort', abort, {once: true});
  const unfollow = () => lifetime.signal.removeEventListener('abort', abort); host.setOfflineLifetimeOff(unfollow);
  const current = () => epoch === host.startEpoch() && !controller.signal.aborted && admitted();
  const scoped = host.selectionScope(controller.signal, current);
  let accepted = false;
  try {
    const source = await host.offline.prepare(entry, current);
    if (!current()) throw retired();
    if (!offlineContentKind(source.metadata.kind)) throw new Error(serviceText('downloads.offline.notPlayableHere'));
    host.rememberCurrent(); await host.cancelQueue();
    if (!current() || !source.current()) throw retired();
    host.bindOffline(source, offlineSubjectFor(source));
    host.setExpanded(!audioKind(source.metadata.kind));
    host.setPreparing(true);
    await host.playback.playLocal(source.itemId, () => makeOfflinePlaybackSession(host.offline, host.playback, source), startSeconds, {signal: controller.signal, isCurrent: current});
    const session = host.playback.getSnapshot().session;
    accepted = !!session && host.getOfflineSource() === source && !!host.offline.mediaUrl(session);
    if (!accepted) {
      if (!current()) throw retired();
      throw new Error(host.playback.getSnapshot().error || serviceText('downloads.offline.notPlayableHere'));
    }
  } catch (error) {
    if (!current()) throw retired();
    throw error;
  } finally {
    scoped.close();
    if (!accepted) {unfollow(); controller.abort(); host.clearOfflineLifetimeOffIf(unfollow);}
    if (epoch === host.startEpoch()) {host.setPreparing(false); host.changed();}
  }
}

export function restartOfflinePlayback(host: NativeOfflineHost, fence: Extract<PlaybackFence, {kind: 'local'}>, seconds: number): Promise<void> {
  const entry = host.offlineDevice?.getSnapshot().entries.find(value => value.id === fence.entryId);
  if (!entry) throw new Error(serviceText('downloads.offline.notPlayableHere'));
  // A replay is a fresh local play, admitted again without the original row's lifetime.
  return startOfflinePlayback(host, entry, host.retryLifetime(), seconds);
}

export async function reopenOfflineBookmark(host: NativeOfflineHost, savedOffline: NativeOfflineSource | undefined, positionSeconds: number, epoch: number, signal: AbortSignal): Promise<void> {
  const valid = () => host.currentAuthority() && epoch === host.startEpoch() && !signal.aborted;
  if (!host.offline || !savedOffline || !savedOffline.current()) throw new Error(serviceText('downloads.offline.notPlayableHere'));
  const source = await host.offline.reopen(savedOffline, valid);
  if (!valid()) return;
  host.bindOffline(source, offlineSubjectFor(source));
  await host.playback.playLocal(source.itemId, () => makeOfflinePlaybackSession(host.offline, host.playback, source), positionSeconds,
    {initialIntent: 'paused', signal, isCurrent: valid});
  if (!valid()) return;
  const local = host.fence();
  if (!local || local.kind !== 'local') throw new Error(host.playback.getSnapshot().error || serviceText('downloads.offline.notPlayableHere'));
}
