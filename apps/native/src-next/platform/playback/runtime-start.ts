import {readPlaybackCapabilities} from '../../../../../packages/client-core/src/query/playback';
import {serverNow} from '../../../../../packages/client-core/src/server-clock';
import type {HttpLocalApi, LocalSession, PlaybackService} from '../../../../../packages/client-core/src/index';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {PlaybackOptionsClient} from '../../../../../packages/client-core/src/playback-v1/options';
import {parseDiscTitleRef, type DiscTitleRef, type PlaybackOptions, type Session} from '../../../../../packages/client-core/src/playback-v1/types';
import {serverSupportsQueueTransitions, type V1QueuePlayer} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import {playbackFence, sameFence, type DockPlayback, type PlaybackDock, type PlaybackFence} from '../../../../../packages/player-core/src';
import {serverPlaybackV1Kinds} from './native-listening-kinds';
import {signInHolds} from './paint-authority';
import {nativeStartSeconds} from './start-intent';
import {captureNativeStartPresentation, type NativeStartPresentationOwner} from './start-presentation';
import {entryDisplay, type NativeStartHandle} from './start-watch';
import type {NativePlaybackStart, NativePlaybackVersionSelection, NativeStartDisplay, PlaybackAuthority} from './types';

const authorityKey = (value: PlaybackAuthority) => JSON.stringify([value.serverId, value.accountId, value.profileId, value.epoch, value.privacyFence ?? value.viewerFence]);
const wireId = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_.-]{1,128}$/.test(value);
const audioKind = (kind: string | undefined) => kind === 'song' || kind === 'track' || kind === 'audiobook_file' || kind === 'audiobook';

export type NativeStartHost = Readonly<{
  api: HttpLocalApi;
  bound: PlaybackAuthority;
  playback: PlaybackService;
  queue: V1QueuePlayer;
  dock: PlaybackDock;
  optionsClient: PlaybackOptionsClient;
  session: () => LocalSession | undefined;
  capabilitiesReady: () => Promise<void>;
  subscribe: (listener: () => void) => () => void;
  subscribeAuthority: (listener: () => void) => () => void;
  subscribeStartContext?: (listener: () => void) => () => void;
  connect: () => void;
  active: () => boolean;
  currentAuthority: () => boolean;
  isRetired: () => boolean;
  hasSnapshot: () => boolean;
  startEpoch: () => number;
  startContext: () => (() => boolean) | undefined;
  startOperationCurrent: (epoch: number) => boolean;
  beginOperation: (retainScope?: AbortSignal) => number;
  nextVersionMenuEpoch: () => number;
  versionMenuEpoch: () => number;
  setStartController: (controller: AbortController | undefined, expected?: AbortController) => void;
  selectionScope: (signal: AbortSignal, current: () => boolean) => Readonly<{signal: AbortSignal; close: () => void}>;
  request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>;
  beginStarting: (display: NativeStartDisplay | undefined, retry: () => Promise<void>) => NativeStartHandle;
  retryStart: (input: NativePlaybackStart) => Promise<void>;
  setInitialPresentation: (owner: NativeStartPresentationOwner | undefined, target?: {owner: NativeStartPresentationOwner; epoch: number; itemId: string; session?: Session}) => void;
  getInitialPresentation: () => NativeStartPresentationOwner | undefined;
  setServerCapabilities: (kinds: readonly string[], transitions: boolean) => void;
  remember: (playback: DockPlayback) => void;
  prepareOfferedStart: (entry: ContentEntry | undefined, offered: PlaybackOptions, expanded: boolean) => void;
  finishOfferedStart: (epoch: number) => void;
  snapshotFence: () => PlaybackFence | undefined;
  fence: () => PlaybackFence | undefined;
  currentSession: () => Session | undefined;
  stop: (fence: PlaybackFence) => Promise<void>;
  publish: () => void;
}>;

export async function startOfferedPlayback(
  host: NativeStartHost,
  input: NativePlaybackStart,
  offered: PlaybackOptions,
  capabilities: unknown,
  epoch: number,
  versionId?: string,
  lifetime?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>,
  discTitle?: DiscTitleRef,
): Promise<void> {
  const current = () => host.active() && host.startOperationCurrent(epoch) && !lifetime?.signal.aborted && lifetime?.isCurrent() !== false;
  if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
  if (offered.itemId !== input.target.itemId || !offered.versions.length) throw new Error(serviceText('queuePlayer.unsupported'));
  host.setServerCapabilities(serverPlaybackV1Kinds(capabilities), serverSupportsQueueTransitions(capabilities));
  const old = host.dock.getSnapshot().local;
  if (old) {
    host.remember(old);
    const snapshotFence = host.snapshotFence();
    if (snapshotFence && sameFence(old.fence, snapshotFence)) host.playback.pause();
  }
  if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'}); // A synchronous pause subscriber may retire the chooser or start another item.
  const entry = input.entry.id === input.target.itemId ? input.entry : undefined;
  host.prepareOfferedStart(entry, offered, !audioKind(offered.kind));
  try {
    if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
    const move = input.move, choices = {...(versionId ? {versionId, ...(discTitle ? {discTitle} : {})} : {}),
      ...(move ? {moveRequestId: move.requestId, ...(move.audioTrackId ? {audioTrackId: move.audioTrackId} : {})} : {})};
    await host.queue.playItem(input.target.itemId, nativeStartSeconds(input), undefined, undefined, undefined, Object.keys(choices).length ? choices : undefined,
      // Plays on through the item's show, album or book, anchored on it (PB-1, MU-2); a move
      // plays on through the queue the other device had.
      {...lifetime, state: input.state ?? 'playing', isCurrent: current, propagateError: true,
        ...(move?.queueId ? {from: {queue: {id: move.queueId}}} : offered.container ? {from: {container: offered.container}} : {})});
  } catch (error) {
    // Keep the original typed admission refusal, including its closed reason,
    // retry policy and account detail. A retired operation cannot publish it.
    if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
    throw error;
  } finally {host.finishOfferedStart(epoch);}
}

export async function startItemPlayback(host: NativeStartHost, input: NativePlaybackStart): Promise<void> {
  const caller = input.lifetime;
  const callerCurrent = () => !caller?.signal.aborted && caller?.isCurrent() !== false;
  if (!callerCurrent()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
  host.connect();
  if (!host.active()) return;
  if (!wireId(input.target.itemId)) throw new Error('Invalid playback target.');
  nativeStartSeconds(input);
  const epoch = host.beginOperation();
  const controller = new AbortController(); host.setStartController(controller);
  // O-159: drawn at once from the pressed card; Try again belongs to this runtime, not the page.
  let stopped = false;
  const starting = host.beginStarting(entryDisplay(input.entry), () => host.retryStart({...input, lifetime: undefined, presentation: undefined}));
  starting.stopWith(() => {stopped = true; controller.abort();});
  const abort = () => controller.abort();
  caller?.signal.addEventListener('abort', abort, {once: true});
  if (caller?.signal.aborted) abort();
  const current = () => host.active() && host.startOperationCurrent(epoch) && !controller.signal.aborted && callerCurrent();
  const scoped = host.selectionScope(controller.signal, current);
  // Paint retains the original page/actor, not the command scope that closes
  // on successful completion. It has no ability to start/restart playback.
  const originalContext = host.startContext(), api = host.api, route = api.getRouteSnapshot(), principal = host.session();
  const connection = api.getRouteConnection(), pin = connection?.pin.publicKey, certificate = api.getCertificateSessionEpoch();
  const paintUntil = principal ? Math.min(Date.parse(principal.expiresAt), Date.parse(principal.authorizationHorizon)) : NaN;
  const paintCurrent = () => host.currentAuthority() && host.startOperationCurrent(epoch) && callerCurrent()
    && !!principal && Number.isFinite(paintUntil) && serverNow() < paintUntil && signInHolds(principal, host.session())
    && api.getRouteConnection() === connection && !!pin && connection?.pin.publicKey === pin && api.getCertificateSessionEpoch() === certificate
    && host.startContext() === originalContext && originalContext?.() !== false
    && host.api === api && (route ? api.getRouteSnapshot()?.phase === 'ready'
      && api.getRouteSnapshot()?.revision === route.revision && api.getRouteSnapshot()?.origin === route.origin : api.getRouteSnapshot() === null);
  const presentation = captureNativeStartPresentation(caller && originalContext ? input.presentation : undefined, {
    current: paintCurrent,
    sourceCurrent: original => sameFence(host.fence(), original) && !['error', 'ended'].includes(host.playback.getSnapshot().phase),
    changed: () => {if (host.hasSnapshot() && !host.isRetired()) host.publish();},
    subscribe: listener => {
      const offs = [host.subscribe(listener), host.subscribeAuthority(listener), host.subscribeStartContext?.(listener), api.subscribeRoute(listener)];
      caller?.signal.addEventListener('abort', listener, {once: true});
      return () => {offs.forEach(off => off?.()); caller?.signal.removeEventListener('abort', listener);};
    },
  });
  if (paintCurrent()) {
    host.setInitialPresentation(presentation, presentation ? {owner: presentation, epoch, itemId: input.target.itemId} : undefined);
  } else presentation?.retire();
  try {
    await host.queue.cancel(); if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
    await host.capabilitiesReady(); if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
    const [offered, capabilities] = await Promise.all([
      host.optionsClient.options(input.target.itemId, {}, scoped.signal),
      readPlaybackCapabilities({request: host.request}, scoped.signal),
    ]);
    if (!current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'});
    // A returning Cast title keeps the edition the receiver reported, when it is still offered.
    const versionId = input.versionId && offered.versions.filter(value => value.id === input.versionId).length === 1 ? input.versionId : undefined;
    await startOfferedPlayback(host, input, offered, capabilities, epoch, versionId, {signal: scoped.signal, isCurrent: current});
    const accepted = host.fence(), session = host.currentSession();
    // Accepted: the watch now runs until this fence's real dock is drawn; Stop ends the session.
    starting.accepted(accepted);
    starting.stopWith(() => {stopped = true; controller.abort(); if (accepted) void host.stop(accepted).catch(() => {});});
    if (presentation && host.getInitialPresentation() === presentation && current() && !audioKind(offered.kind)
      && accepted?.kind === 'queue' && accepted.itemId === input.target.itemId && session?.id === accepted.sessionId
      && session.role === 'local' && session.presentation.generation === accepted.generation && session.versionId === accepted.sourceId
      && session.queue?.entryId === accepted.entryId) presentation.bind(accepted);
    else if (host.getInitialPresentation() === presentation) {host.setInitialPresentation(undefined); presentation?.retire();}
  } catch (error) {
    starting.end();
    if (host.getInitialPresentation() === presentation) {host.setInitialPresentation(undefined); presentation?.retire();}
    if (stopped || !current()) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'}); throw error;
  } finally {
    caller?.signal.removeEventListener('abort', abort); scoped.close();
    host.setStartController(undefined, controller);
  }
}

export async function startVersionPlayback(host: NativeStartHost, input: NativePlaybackStart, selection: NativePlaybackVersionSelection): Promise<void> {
  host.connect();
  if (!host.active()) return;
  let epoch = host.startEpoch();
  const menu = host.nextVersionMenuEpoch(), lifetime = selection.lifetime;
  const authority = authorityKey(selection.authority), expected = selection.expectedFence ? playbackFence(selection.expectedFence) : undefined;
  const versionId = selection.versionId, discTitle = parseDiscTitleRef(selection.discTitle), principal = host.session();
  const sameSource = () => expected ? sameFence(host.snapshotFence(), expected) : host.snapshotFence() === undefined;
  // A captured output belongs to preflight. Once this command owns the queue intent,
  // the core's generation/adoption guard must allow its own newly accepted output.
  let dispatching = false;
  const current = () => !lifetime.signal.aborted && lifetime.isCurrent() && host.currentAuthority() && signInHolds(principal, host.session()) && epoch === host.startEpoch() && menu === host.versionMenuEpoch() && authority === authorityKey(host.bound) && (!dispatching || host.startOperationCurrent(epoch)) && (dispatching || sameSource());
  if (!current()) return;
  if (!wireId(input.target.itemId) || !wireId(versionId) || selection.options.itemId !== input.target.itemId || selection.options.versions.filter(value => value.id === versionId).length !== 1) throw new Error(serviceText('queuePlayer.unsupported'));
  const drawnTitle = selection.options.versions.find(value => value.id === versionId)?.discTitle;
  if (drawnTitle && (drawnTitle.id !== discTitle?.id || drawnTitle.revision !== discTitle?.revision)) throw new Error(serviceText('queuePlayer.unsupported'));
  if (discTitle && !selection.options.versions.some(value => value.id === versionId
    && value.discTitle?.id === discTitle.id && value.discTitle.revision === discTitle.revision)) throw new Error(serviceText('queuePlayer.unsupported'));
  const scoped = host.selectionScope(lifetime.signal, current);
  try {
    await host.capabilitiesReady();
    if (!current()) return;
    const [offered, capabilities] = await Promise.all([
      host.optionsClient.options(input.target.itemId, discTitle ? {versionId, discTitle} : {}, scoped.signal),
      readPlaybackCapabilities({request: host.request}, scoped.signal),
    ]);
    if (!current()) return;
    if (offered.itemId !== input.target.itemId || offered.versions.filter(value => value.id === versionId).length !== 1) throw new Error(serviceText('queuePlayer.unsupported'));
    // This final read owns resume. An earlier card/menu position has no authority here.
    const positionMs = discTitle ? 0 : offered.resume?.positionMs ?? 0;
    if (!Number.isFinite(positionMs) || positionMs < 0) throw new Error(serviceText('queuePlayer.unsupported'));
    const fresh = {...input, startIntent: 'explicit' as const, target: {...input.target, startSeconds: positionMs / 1000}};
    epoch = host.beginOperation(scoped.signal);
    await host.queue.cancel();
    if (!current()) return;
    dispatching = true;
    await startOfferedPlayback(host, fresh, offered, capabilities, epoch, versionId, {signal: scoped.signal, isCurrent: current}, discTitle);
  } catch (error) {if (!current() || scoped.signal.aborted) throw Object.assign(new Error('Playback start retired'), {name: 'AbortError'}); throw error;}
  finally {scoped.close();}
}
