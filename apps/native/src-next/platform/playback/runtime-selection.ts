import {readPlaybackCapabilities} from '../../../../../packages/client-core/src/query/playback';
import type {PlaybackService} from '../../../../../packages/client-core/src/index';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {HomeSelectionPlaybackRequest} from '../../../../../packages/client-core/src/home-selection/types';
import type {SelectionContext} from '../../../../../packages/client-core/src/selection-model/index';
import type {V1Request, V1Response} from '../../../../../packages/client-core/src/playback-v1/http';
import {QueueClient, type SegmentInput} from '../../../../../packages/client-core/src/playback-v1/queue';
import type {QueueHeader, Selector} from '../../../../../packages/client-core/src/playback-v1/types';
import {serverSupportsQueueTransitions, type V1QueuePlayer} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {serverPlaybackV1Kinds} from './native-listening-kinds';
import {selectionSegments, selectionStartSeconds} from './selection';

export type NativeSelectionHost = Readonly<{
  playback: PlaybackService;
  queue: V1QueuePlayer;
  requestId: () => string;
  sendHttp: (request: V1Request) => Promise<V1Response>;
  request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>;
  capabilitiesReady: () => Promise<void>;
  connect: () => void;
  currentAuthority: () => boolean;
  startEpoch: () => number;
  sourceEpoch: () => number;
  startOperationCurrent: (epoch: number) => boolean;
  beginOperation: () => number;
  assertSelection: (context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean) => void;
  selectionCurrent: (context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean) => boolean;
  selectionScope: (signal: AbortSignal, current: () => boolean) => Readonly<{signal: AbortSignal; close: () => void}>;
  setServerCapabilities: (kinds: readonly string[], transitions: boolean) => void;
  rememberCurrent: () => void;
  prepareStartState: (entry: ContentEntry | undefined, epoch: number) => void;
  finishStartState: (epoch: number) => void;
  retainedQueueIds: () => readonly string[];
  changed: () => void;
}>;

async function appendQueueSegments(
  host: NativeSelectionHost,
  segments: readonly SegmentInput[],
  placement: 'next' | 'end',
  signal: AbortSignal,
  check: () => void,
): Promise<QueueHeader> {
  const client = new QueueClient({send: request => host.sendHttp({...request, signal})}, host.requestId);
  host.connect(); check();
  const held = host.queue.getSnapshot().view?.queue;
  if (!held) {
    check();
    const created = await client.create(segments, {signal, retainQueueIds: host.retainedQueueIds()}); check();
    await host.queue.open(created.queue.id, signal); check();
    host.changed(); return created.queue;
  }
  let queue = await client.header(held.id, signal); check();
  const ordered = placement === 'next' ? [...segments].reverse() : segments;
  for (const segment of ordered) {
    check();
    queue = (await client.addSegment(queue.id, queue.revision, placement, segment)).queue;
    check();
  }
  await host.queue.refresh(signal); check();
  host.changed();
  return queue;
}

export async function playSelectionNow(
  host: NativeSelectionHost,
  request: HomeSelectionPlaybackRequest,
  current: (context: SelectionContext) => boolean,
): Promise<{queue: QueueHeader}> {
  host.assertSelection(request.context, request.signal, current);
  const segments = selectionSegments(request.items, true), epoch = host.beginOperation();
  const valid = () => host.startOperationCurrent(epoch) && host.selectionCurrent(request.context, request.signal, current);
  const scoped = host.selectionScope(request.signal, valid);
  try {
    host.connect();
    await host.queue.cancel();
    host.assertSelection(request.context, scoped.signal, current); if (!valid()) throw new Error('stale_selection_context');
    await host.capabilitiesReady();
    host.assertSelection(request.context, scoped.signal, current); if (!valid()) throw new Error('stale_selection_context');
    const capabilities = await readPlaybackCapabilities({request: host.request}, scoped.signal);
    if (!valid()) throw new Error('stale_selection_context');
    host.setServerCapabilities(serverPlaybackV1Kinds(capabilities), serverSupportsQueueTransitions(capabilities));
    host.rememberCurrent();
    const first = request.items[0]!;
    const entry = 'entry' in first && first.entry.id === first.target.itemId ? first.entry : undefined;
    host.prepareStartState(entry, epoch);
    // Only the canonical core owns the create/adopt path and its pre-adoption context fence.
    const queue = await host.queue.playSegments(segments, {startSeconds: selectionStartSeconds(request.items), signal: scoped.signal, isCurrent: valid});
    if (!valid()) throw new Error('stale_selection_context');
    return {queue};
  } catch (error) {
    if (host.startOperationCurrent(epoch) && host.currentAuthority()) {await host.queue.cancel(); host.playback.leave(); host.changed();}
    throw error;
  } finally {
    scoped.close();
    host.finishStartState(epoch);
  }
}

export async function enqueueSelection(
  host: NativeSelectionHost,
  request: HomeSelectionPlaybackRequest & {placement: 'next' | 'end'},
  current: (context: SelectionContext) => boolean,
  epoch: number,
  source: number,
): Promise<{queue: QueueHeader}> {
  host.assertSelection(request.context, request.signal, current);
  if (request.placement !== 'next' && request.placement !== 'end') throw new Error('invalid_queue_placement');
  const segments = selectionSegments(request.items, false);
  const valid = () => epoch === host.startEpoch() && source === host.sourceEpoch() && host.selectionCurrent(request.context, request.signal, current);
  if (!valid()) throw new Error('stale_selection_context');
  const scoped = host.selectionScope(request.signal, valid);
  const check = () => {if (!valid()) throw new Error('stale_selection_context');};
  try {
    const queue = await appendQueueSegments(host, segments, request.placement, scoped.signal, check);
    return {queue};
  } finally {scoped.close();}
}

export async function playCatalogueSegmentsNow(
  host: NativeSelectionHost,
  segments: readonly SegmentInput[],
  options: NonNullable<Parameters<V1QueuePlayer['playSegments']>[1]>,
): Promise<QueueHeader> {
  const {signal, isCurrent} = options;
  if (!signal || !isCurrent) throw new Error('native_catalogue_lifetime_required');
  if (!segments.length || options.startSeconds !== undefined && (!Number.isFinite(options.startSeconds) || options.startSeconds < 0 || options.startSeconds > 1e9)) throw new Error('native_catalogue_selection_invalid');
  const admitted = () => !signal.aborted && host.currentAuthority() && isCurrent();
  const retired = () => Object.assign(new Error('native_catalogue_retired'), {name: 'AbortError'});
  if (!admitted()) throw retired();
  const epoch = host.beginOperation(), valid = () => host.startOperationCurrent(epoch) && admitted();
  const scoped = host.selectionScope(signal, valid);
  const assertCurrent = () => {if (!valid()) throw retired();};
  try {
    host.connect();
    await host.queue.cancel(); assertCurrent();
    await host.capabilitiesReady(); assertCurrent();
    const capabilities = await readPlaybackCapabilities({request: host.request}, scoped.signal); assertCurrent();
    host.setServerCapabilities(serverPlaybackV1Kinds(capabilities), serverSupportsQueueTransitions(capabilities));
    host.rememberCurrent(); assertCurrent();
    host.prepareStartState(undefined, epoch);
    const queue = await host.queue.playSegments(segments, {...options, signal: scoped.signal, isCurrent: valid});
    assertCurrent(); return queue;
  } catch (error) {
    if (host.startOperationCurrent(epoch) && host.currentAuthority()) {await host.queue.cancel(); host.playback.leave(); host.changed();}
    throw error;
  } finally {
    scoped.close();
    host.finishStartState(epoch);
  }
}

export async function enqueueCatalogueSegments(
  host: NativeSelectionHost,
  segments: readonly SegmentInput[],
  placement: 'next' | 'end',
  lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>,
  epoch: number,
  source: number,
): Promise<QueueHeader> {
  const admitted = () => !lifetime.signal.aborted && host.currentAuthority() && lifetime.isCurrent();
  const retired = () => Object.assign(new Error('native_catalogue_retired'), {name: 'AbortError'});
  if (!admitted() || !segments.length) throw retired();
  if (placement !== 'next' && placement !== 'end') throw Object.assign(new Error('native_catalogue_placement_invalid'), {code: 'invalid_queue_placement'});
  const valid = () => epoch === host.startEpoch() && source === host.sourceEpoch() && admitted();
  if (!valid()) throw retired();
  const scoped = host.selectionScope(lifetime.signal, valid);
  const check = () => {if (!valid()) throw retired();};
  try {
    return await appendQueueSegments(host, segments, placement, scoped.signal, check);
  } finally {scoped.close();}
}

export async function startGroupNow(
  host: NativeSelectionHost,
  input: Readonly<{source: Selector; shuffle: boolean; signal: AbortSignal; isCurrent: () => boolean}>,
): Promise<void> {
  const admitted = () => !input.signal.aborted && host.currentAuthority() && input.isCurrent();
  if (!admitted()) throw Object.assign(new Error('native_group_retired'), {name: 'AbortError'});
  const epoch = host.beginOperation(), valid = () => host.startOperationCurrent(epoch) && admitted();
  const scoped = host.selectionScope(input.signal, valid);
  const assertCurrent = () => {if (!valid()) throw Object.assign(new Error('native_group_retired'), {name: 'AbortError'});};
  try {
    host.connect();
    await host.queue.cancel(); assertCurrent();
    await host.capabilitiesReady(); assertCurrent();
    const capabilities = await readPlaybackCapabilities({request: host.request}, scoped.signal); assertCurrent();
    host.setServerCapabilities(serverPlaybackV1Kinds(capabilities), serverSupportsQueueTransitions(capabilities));
    host.rememberCurrent(); assertCurrent();
    host.prepareStartState(undefined, epoch);
    await host.queue.playSegments([{source: input.source, ...(input.shuffle ? {order: {mode: 'shuffle' as const}} : {})}], {signal: scoped.signal, isCurrent: valid});
    assertCurrent();
  } catch (error) {
    if (host.startOperationCurrent(epoch) && host.currentAuthority()) {await host.queue.cancel(); host.playback.leave(); host.changed();}
    throw error;
  } finally {
    scoped.close();
    host.finishStartState(epoch);
  }
}
