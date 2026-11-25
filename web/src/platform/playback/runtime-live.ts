import {localV1Http} from '@core/playback-v1/local-http.ts';
import {V1ChannelControl} from '@core/playback-v1/channel-control.ts';
import {serverNow} from '@core/server-clock.ts';
import type {HomeChannel} from '@core/home.ts';
import type {GuideChannel} from '@core/channel-guide.ts';
import type {SelectionContext} from '@core/selection-model/types.ts';
import type {PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {markPresentedInPlayer} from './presented.ts';
import type {WebPlaybackEnvironment, WebPlaybackScreenLifetime, WebPlaybackSource, WebPlaybackStartDisplay} from './types.ts';
import {problem, startStopped, type Stack, type Starting} from './runtime-types.ts';

export type LiveRuntimeHost = Readonly<{
  life(): AbortController;
  nextOperation(): number;
  operation(): number;
  assertCurrent(): void;
  current(stack?: Stack): boolean;
  require(fence: PlaybackFence): Stack;
  selectionCurrent(context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): void;
  retainMediaStart(signal?: AbortSignal, isCurrent?: () => boolean): () => void;
  beginStarting(display: WebPlaybackStartDisplay | undefined, retry: () => Promise<void>, options?: Readonly<{watchdog?: boolean}>): Starting;
  retryLifetime(): WebPlaybackScreenLifetime;
  replace(mode?: Stack['mode']): Stack;
  abandon(stack: Stack, serial: number): void;
  reportStartProblem(error: unknown, display: WebPlaybackStartDisplay | undefined, retry: () => Promise<void>, serial: number, sentence?: string): 'shown' | 'quiet' | 'unhandled';
  sync(): void;
}>;

export function createChannelControl(source: WebPlaybackSource, environment: WebPlaybackEnvironment, life: AbortController): V1ChannelControl {
  const inner = localV1Http(source.api);
  return new V1ChannelControl({
    events: source.events,
    http: {send: request => inner.send(request.method === 'DELETE' ? request : {...request, signal: request.signal ? AbortSignal.any([request.signal, life.signal]) : life.signal})},
    key: environment.key,
    now: environment.timers?.now,
    // A browser plays a channel's HLS, never a live source's own MPEG-TS; the server converts it for a browser (11 rule 23).
    serverNow: () => serverNow(),
    plays: presentation => !(presentation.mode === 'live' && presentation.route === 'original'),
  });
}

export async function playChannel(host: LiveRuntimeHost, channel: HomeChannel, context: SelectionContext, signal: AbortSignal, current: (context: SelectionContext) => boolean): Promise<void> {
  host.selectionCurrent(context, signal, current);
  const value = channel.channel, source = channel.source;
  if (!value.tuneAvailable || value.tuneUnavailableReason || value.sourceId !== source.id || value.generation !== source.generation || value.provenance !== source.provenance) problem('invalid_channel_target');
  await playFromGuide(host, value, {signal, isCurrent: () => {host.selectionCurrent(context, signal, current);return true;}});
}

/** A genuine guide handle needs no Home entry or Home selection context. The channel
 * generation goes unchanged to the server's current-source/access admission check. */
export async function playFromGuide(host: LiveRuntimeHost, channel: GuideChannel, context: WebPlaybackScreenLifetime): Promise<void> {
  const check = () => {host.assertCurrent();if (context.signal.aborted || !context.isCurrent()) problem('stale_selection_context');};
  check();
  if (!channel.tuneAvailable || channel.tuneUnavailableReason || !['live-source', 'library-channel'].includes(channel.provenance)
    || ![channel.id, channel.sourceId, channel.generation].every(value => typeof value === 'string' && value.length > 0 && value.length <= 256 && !/[\r\n]/.test(value))) problem('invalid_channel_target');
  const value = Object.freeze({...channel}), reference = Object.freeze({kind: value.provenance, sourceId: value.sourceId, channelId: value.id, generation: value.generation, name: value.name, ...(value.logoPath ? {logoUrl: value.logoPath} : {})});
  const serial = host.nextOperation();
  const finishRetention = host.retainMediaStart(context.signal, () => serial === host.operation());
  // The visible guide row supplies the channel's name and any server-held logo.
  const subject = {kind: 'channel' as const, id: value.id, sourceId: value.sourceId, channelName: value.name};
  const starting = host.beginStarting({subject}, () => playFromGuide(host, value, host.retryLifetime()), {watchdog: false}), stopped = new AbortController();
  starting.stopWith(() => stopped.abort());
  // The channel's own words for a start that could not play (every tuner busy, a stream this browser cannot play yet).
  let said: string | undefined;
  try {
    const stack = host.replace();
    starting.attach(stack);
    stack.expanded = true;
    stack.channel = {channel: value};
    stack.subject = subject;
    const abandon = () => host.abandon(stack, serial);
    starting.stopWith(() => {stopped.abort(); abandon();});
    const lifetime = AbortSignal.any([context.signal, stack.life.signal, host.life().signal, stopped.signal]);
    const current = () => {if (lifetime.aborted || !host.current(stack) || serial !== host.operation()) return false;try {check();return true;} catch {return false;}};
    const cancel = () => {if (host.current(stack) && serial === host.operation()) stack.service.leave();};
    lifetime.addEventListener('abort', cancel, {once: true});
    try {
      await stack.service.playChannel(reference, {signal: lifetime, isCurrent: current});
      if (!current()) problem('stale_selection_context');
      const state = stack.service.getSnapshot();
      if (state.phase === 'error' || !state.channel) {said = state.error || undefined;problem('channel_playback_failed');}
      starting.accepted(); starting.stopWith(abandon);
      host.sync();
    } catch (error) { cancel(); throw error; }
    finally {lifetime.removeEventListener('abort', cancel);}
  } catch (error) {
    const display = starting.display();
    starting.end(); if (stopped.signal.aborted) throw startStopped();
    // Drawn in the player, with Try again, as a title's refused start is; never a bare toast.
    const outcome = host.reportStartProblem(error, display, () => playFromGuide(host, value, host.retryLifetime()), serial, said);
    throw outcome === 'unhandled' ? error : markPresentedInPlayer(error);
  }
  finally {finishRetention();}
}

/** The existing channel service's Go Live intent. No seek-to-zero, no second controller and
 * no ACK: the next published snapshot is the only source of the live-edge state. */
export function goLive(host: LiveRuntimeHost, fence: PlaybackFence): void {
  const stack = host.require(fence);
  if (fence.kind !== 'channel') problem('playback_command_unavailable');
  const live = stack.service.getSnapshot();
  if (!live.channel || live.channel.kind !== fence.channelKind || live.channel.sourceId !== fence.sourceId
    || live.channel.channelId !== fence.channelId || live.channel.generation !== fence.channelGeneration) problem('playback_command_unavailable');
  stack.service.goLive();
  if (host.current(stack)) host.sync();
}
