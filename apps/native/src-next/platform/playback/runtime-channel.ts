import {readPlaybackCapabilities} from '../../../../../packages/client-core/src/query/playback';
import {serverNow} from '../../../../../packages/client-core/src/server-clock';
import type {LocalSession, PlaybackService} from '../../../../../packages/client-core/src/index';
import type {V1Request, V1Response} from '../../../../../packages/client-core/src/playback-v1/http';
import type {EventsClient} from '../../../../../packages/client-core/src/playback-v1/events';
import type {Timers} from '../../../../../packages/client-core/src/playback-v1/sessions';
import {V1ChannelControl, serverSupportsV1Channels} from '../../../../../packages/client-core/src/playback-v1/channel-control';
import {channelSeconds, type ChannelReference} from '../../../../../packages/client-core/src/playback/linear-protocol';
import {readPlayerGuide} from '../../../../../packages/client-core/src/guide/player-guide';
import {legacyChannel} from '../../../../../packages/client-core/src/guide/legacy-source';
import type {HomeChannel} from '../../../../../packages/client-core/src/home';
import type {SelectionContext} from '../../../../../packages/client-core/src/selection-model/index';
import {playbackFence, sameFence, type ChannelPlaybackFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import {signInHolds} from './paint-authority';
import type {NativePlaybackChannelStart} from './types';

const wireId = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_.-]{1,128}$/.test(value);

/** The guide's programme airing now, for the title a channel's player draws (`ChannelReference.programme`). */
export const airingNow = (programmes: readonly Readonly<{id: string; title: string; start: number; end: number}>[]): {programme?: NonNullable<ChannelReference['programme']>} => {
  const at = serverNow(), now = programmes.find(p => p.title && Number.isFinite(p.start) && Number.isFinite(p.end) && p.start <= at && at < p.end);
  return now ? {programme: Object.freeze({id: now.id, title: now.title, startMs: now.start, endMs: now.end})} : {};
};

export type NativeChannelHost = Readonly<{
  playback: PlaybackService;
  events: Pick<EventsClient, 'on' | 'onResync'>;
  requestId: () => string;
  timers?: Timers;
  sendHttp: (request: V1Request) => Promise<V1Response>;
  request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>;
  session: () => LocalSession | undefined;
  capabilitiesReady: () => Promise<void>;
  connect: () => void;
  cancelQueue: () => Promise<void>;
  currentAuthority: () => boolean;
  startEpoch: () => number;
  startOperationCurrent: (epoch: number) => boolean;
  beginOperation: (retainScope?: AbortSignal) => number;
  assertSelection: (context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean) => void;
  selectionCurrent: (context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean) => boolean;
  selectionScope: (signal: AbortSignal, current: () => boolean) => Readonly<{signal: AbortSignal; close: () => void}>;
  rememberCurrent: () => void;
  setExpanded: (expanded: boolean) => void;
  clearCatalogState: () => void;
  snapshotFence: () => PlaybackFence | undefined;
  fence: () => PlaybackFence | undefined;
  changed: () => void;
}>;

/** Live channel playback lifecycle, tuning, guide-origin tracking, and paused position restore. */
export class NativeChannelRunner {
  private control?: V1ChannelControl;
  private cleanup?: () => void;
  private channel?: HomeChannel;
  private guide?: NativePlaybackChannelStart['guide'];
  private readonly host: NativeChannelHost;

  constructor(host: NativeChannelHost) {
    this.host = host;
  }

  clear(): void {
    this.control?.close(); this.control = undefined;
    this.cleanup?.(); this.cleanup = undefined;
    this.channel = undefined;
    this.guide = undefined;
  }

  retry(): void {
    this.control?.retry();
  }

  goLive(): void {
    this.control?.goLive();
  }

  channelName(): string | undefined {
    return this.channel?.channel.name;
  }

  guideFor(fence: PlaybackFence | undefined): NativePlaybackChannelStart['guide'] | undefined {
    if (fence?.kind !== 'channel' || !this.guide) return undefined;
    const row = legacyChannel(this.guide.row);
    return row?.id === fence.channelId && row.sourceId === fence.sourceId && row.provenance === fence.channelKind && row.generation === fence.channelGeneration
      ? this.guide : undefined;
  }

  async playHomeChannel(channel: HomeChannel, context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): Promise<void> {
    this.host.assertSelection(context, signal, current);
    const value = channel.channel, source = channel.source;
    if (!value.tuneAvailable || value.tuneUnavailableReason || value.hidden || value.sourceId !== source.id || value.generation !== source.generation || value.provenance !== source.provenance || !['live-source', 'library-channel'].includes(value.provenance) || !wireId(source.generation) || !wireId(source.id) || !wireId(value.id) || value.provenance === 'library-channel' && source.id !== value.id) throw new Error('invalid_channel_target');
    const epoch = this.host.beginOperation();
    const valid = () => epoch === this.host.startEpoch() && this.host.selectionCurrent(context, signal, current);
    const scoped = this.host.selectionScope(signal, valid);
    try {
      this.host.connect(); await this.host.cancelQueue();
      if (!valid()) throw new Error('stale_selection_context');
      await this.host.capabilitiesReady();
      if (!valid()) throw new Error('stale_selection_context');
      const capabilities = await readPlaybackCapabilities({request: this.host.request}, scoped.signal);
      if (!valid()) throw new Error('stale_selection_context');
      if (!serverSupportsV1Channels(capabilities)) throw new Error('channel_playback_unavailable');
      this.host.rememberCurrent(); this.channel = channel; this.host.setExpanded(true);
      const now = value.programmes.map(p => ({id: p.id, title: p.title, start: Date.parse(p.start), end: Date.parse(p.end)}));
      await this.open({kind: value.provenance, sourceId: source.id, channelId: value.id, generation: value.generation, ...(value.name ? {name: value.name} : {}),
        ...(value.logoPath ? {logoUrl: value.logoPath} : {}), ...airingNow(now)}, epoch, scoped.signal, valid, 'playing');
      this.host.changed();
    } catch (error) {
      if (epoch === this.host.startEpoch() && this.host.currentAuthority()) {this.host.playback.leave(); this.host.changed();}
      throw error;
    } finally {scoped.close();}
  }

  async startChannel(input: NativePlaybackChannelStart): Promise<void> {
    const accepted = input.onAccepted;
    // These are this invocation's values even if its caller later reuses a DTO.
    input = Object.freeze({...input, lifetime: Object.freeze({...input.lifetime})});
    if (!wireId(input.sourceId) || !wireId(input.channelId) || !input.timezone || input.lifetime.signal.aborted || !input.lifetime.isCurrent() || !this.host.currentAuthority()) return;
    const expected = input.expectedFence && playbackFence(input.expectedFence);
    if (expected && !sameFence(this.host.snapshotFence(), expected)) return;
    // A zap's preflight still borrows the old output. Taking the operation below
    // closes that output itself, so only the owned epoch guards actual admission.
    let dispatching = !expected, epoch = expected ? this.host.startEpoch() : this.host.beginOperation();
    const principal = this.host.session();
    const current = () => this.host.currentAuthority() && signInHolds(principal, this.host.session())
      && epoch === this.host.startEpoch() && !input.lifetime.signal.aborted && input.lifetime.isCurrent()
      && (dispatching ? this.host.startOperationCurrent(epoch) : !!expected && sameFence(this.host.snapshotFence(), expected));
    const scoped = this.host.selectionScope(input.lifetime.signal, current);
    try {
      this.host.connect(); if (!expected) await this.host.cancelQueue(); if (!current()) return;
      await this.host.capabilitiesReady(); if (!current()) return;
      const options = input.guide?.options ?? {kind: 'all' as const, sourceId: '', timezone: input.timezone}, at = serverNow();
      const [capabilities, channels, listing] = await Promise.all([
        readPlaybackCapabilities({request: this.host.request}, scoped.signal),
        readPlayerGuide({request: this.host.request}, input.channelId, options, scoped.signal),
        // Only for the title drawn in the player: a channel with no listings, or a failed read, tunes the same.
        readPlayerGuide({request: this.host.request}, input.channelId, options, scoped.signal, {from: new Date(at), to: new Date(at + 60_000)}).catch(() => undefined),
      ]);
      if (!current()) return;
      if (!serverSupportsV1Channels(capabilities)) throw new Error('channel_playback_unavailable');
      const currentRow = channels.find(row => row.step === 0)?.channel;
      const channel = currentRow && legacyChannel(currentRow);
      if (!channel || channel.sourceId !== input.sourceId || channel.id !== input.channelId || (channel.hidden && !input.guide?.options.includeHidden) || !channel.tuneAvailable) throw new Error('channel_source_unavailable');
      if (input.expectedSourceGeneration !== undefined && channel.generation !== input.expectedSourceGeneration) throw new Error('channel_source_unavailable');
      const origin = input.guide, row = origin && legacyChannel(origin.row);
      if (origin && (!row || row.id !== channel.id || row.sourceId !== channel.sourceId || row.provenance !== channel.provenance
        || row.generation !== channel.generation || !row.tuneAvailable || origin.options.timezone !== input.timezone)) throw new Error('channel_source_unavailable');
      if (expected) {
        if (!current()) return;
        epoch = this.host.beginOperation(scoped.signal); dispatching = true;
        await this.host.cancelQueue(); if (!current()) return;
      }
      this.host.rememberCurrent(); this.channel = undefined; this.host.setExpanded(true);
      const airing = listing?.find(row => row.step === 0 && row.channel.id === currentRow?.id && row.channel.sourceId === currentRow?.sourceId)?.programmes ?? [];
      await this.open({kind: channel.provenance, sourceId: channel.sourceId, channelId: input.channelId, generation: channel.generation, ...(currentRow?.name ? {name: currentRow.name} : {}),
        ...(channel.logoPath ? {logoUrl: channel.logoPath} : {}), ...airingNow(airing)}, epoch, scoped.signal, current, 'playing', accepted);
      if (current()) {this.guide = origin ? Object.freeze({row: origin.row, options: Object.freeze({...origin.options})}) : undefined; this.host.changed();}
    } catch (error) {
      if (current()) {if (dispatching) {this.host.playback.leave(); this.host.changed();} throw error;}
    } finally {scoped.close();}
  }

  async open(reference: ChannelReference, epoch: number, signal: AbortSignal, beforeAdoption: () => boolean, state: 'playing' | 'paused', onAccepted?: (fence: ChannelPlaybackFence) => void): Promise<void> {
    let adopted = false, opening = false, notified = false;
    const valid = () => epoch === this.host.startEpoch() && this.host.currentAuthority() && (adopted || !signal.aborted && beforeAdoption());
    const notifyAccepted = () => {
      if (!onAccepted || notified || !adopted) return;
      // Observed retirement is terminal for this notification, independently of
      // the accepted channel owner's ordinary playback lifetime.
      if (!valid() || !beforeAdoption()) {notified = true; return;}
      const fence = this.host.fence(), session = this.host.playback.getSnapshot().session;
      if (fence?.kind !== 'channel' || fence.channelKind !== reference.kind || fence.channelId !== reference.channelId
        || fence.sourceId !== reference.sourceId || fence.channelGeneration !== reference.generation
        || session?.id !== fence.sessionId || session.generation !== fence.generation) return;
      // Predicates may synchronously retire or replace an owner. Check the same
      // actual fence again, then latch before invoking the captured callback.
      if (!valid() || !beforeAdoption() || !sameFence(this.host.fence(), fence)) {notified = true; return;}
      notified = true;
      onAccepted(fence);
      if (!valid() || !beforeAdoption() || !sameFence(this.host.fence(), fence)) return;
    };
    const scoped = this.host.selectionScope(signal, valid);
    let control: V1ChannelControl | undefined;
    let rejectStart: ((error: Error) => void) | undefined;
    const abort = () => {if (!adopted) {control?.close(); rejectStart?.(new Error('cancelled'));}};
    signal.addEventListener('abort', abort, {once: true});
    const close = () => {signal.removeEventListener('abort', abort); control?.close(); scoped.close(); rejectStart?.(new Error('stale_selection_context')); rejectStart = undefined;};
    try {
      // DELETE cleanup stays bound to its original authority after a Home context expires.
      control = new V1ChannelControl({events: this.host.events, key: this.host.requestId, http: {send: async request => {
        if (request.method === 'DELETE') return this.host.sendHttp(request);
        if (!valid()) {control?.close(); throw new Error('stale_selection_context');}
        const response = await this.host.sendHttp(adopted ? request : {...request, signal: scoped.signal});
        if (!valid()) control?.close();
        return response;
      }}});
      const channelControl = control;
      this.host.playback.useChannelPlayback({
        open: async (ref, sink, options) => {
          opening = true;
          // open() can return before its ordered join seek. Await the service's real session.
          let resolveStart!: () => void;
          const joined = new Promise<void>((resolve, reject) => {resolveStart = resolve; rejectStart = reject;});
          void joined.catch(() => {});
          await channelControl.open(ref, {
            state: value => {
              if (!valid()) {close(); return;}
              sink.state(value);
              if (!adopted && this.host.playback.getSnapshot().session?.id === value.playbackId) {
                adopted = true; scoped.close(); signal.removeEventListener('abort', abort); rejectStart = undefined; resolveStart();
              }
              notifyAccepted();
            },
            error: (message, terminal) => {if (valid()) sink.error(message, terminal); if (!adopted) {rejectStart?.(new Error('channel_playback_failed')); rejectStart = undefined;}},
          }, options);
          await joined;
        },
        recover: () => channelControl.recover(), pause: () => channelControl.pause(), resume: () => channelControl.resume(),
        seek: seconds => channelControl.seek(seconds), goLive: () => channelControl.goLive(), retry: () => channelControl.retry(),
        observe: (seconds, seekId, errorCode) => channelControl.observe(seconds, seekId, errorCode),
        close: () => {if (opening) close(); else channelControl.close();},
      });
      this.control = control; this.cleanup = close;
      this.host.clearCatalogState();
      await this.host.playback.playChannel(reference, {state});
      if (!valid()) throw new Error('stale_selection_context');
      if (this.host.playback.getSnapshot().phase === 'error' || !this.host.playback.getSnapshot().session) throw new Error('channel_playback_failed');
    } catch (error) {close(); throw error;}
  }

  restorePosition(fence: ChannelPlaybackFence, seconds: number, epoch: number, signal: AbortSignal): Promise<void> {
    const before = this.host.playback.getSnapshot();
    const previousSeek = before.linear?.desired.seek?.seekCommandId;
    return new Promise((resolve, reject) => {
      let off = () => {}, done = false;
      let seekId: string | undefined;
      let cancelTimer = () => {};
      const finish = (error?: Error) => {
        if (done) return; done = true; off(); cancelTimer(); signal.removeEventListener('abort', abort); error ? reject(error) : resolve();
      };
      const check = () => {
        const state = this.host.playback.getSnapshot(), current = this.host.fence(), linear = state.linear;
        // A server-resolved seek can change media generation. Its session, service run,
        // source and channel reference must still be the ones this restore actually opened.
        if (signal.aborted || epoch !== this.host.startEpoch() || !this.host.currentAuthority() || current?.kind !== 'channel' || current.generation < fence.generation || !sameFence(current, {...fence, generation: current.generation})) return finish(new Error('stale_selection_context'));
        if (state.phase === 'error' || state.failedSeek || state.seekError || state.intent !== 'paused') return finish(new Error('channel_position_unavailable'));
        if (!linear || seconds < channelSeconds(linear.media.windowStartUs) || seconds > channelSeconds(linear.media.windowEndUs)) return finish(new Error('channel_position_unavailable'));
        const seek = linear.desired.seek;
        if (!seek || seek.seekCommandId === previousSeek || state.seekRevision <= before.seekRevision) return;
        if (seek.positionUs === null || Math.abs(channelSeconds(seek.positionUs) - seconds) > .001 || seekId !== undefined && seekId !== seek.seekCommandId) return finish(new Error('channel_position_unavailable'));
        seekId = seek.seekCommandId;
        if (seek.bufferGeneration === linear.media.bufferGeneration && linear.acknowledgedSeekCommandId === seekId && !state.pendingSeek && Math.abs(state.positionSeconds - seconds) < .25) finish();
      };
      const abort = () => finish(new Error('cancelled'));
      const timeout = () => finish(new Error('channel_position_unavailable'));
      const timers = this.host.timers;
      if (timers?.setTimer && timers.clearTimer) {
        const clear = timers.clearTimer, timer = timers.setTimer(timeout, 15000);
        cancelTimer = () => clear(timer);
      } else {
        const timer = setTimeout(timeout, 15000);
        cancelTimer = () => clearTimeout(timer);
      }
      if (done) {cancelTimer(); return;}
      off = this.host.playback.subscribe(check); signal.addEventListener('abort', abort, {once: true});
      if (signal.aborted) {abort(); return;}
      this.host.playback.seek(seconds); check();
    });
  }
}
