import {NativeEventEmitter, NativeModules} from 'react-native';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src/identity';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority} from '../../shell/model';
import {tvKeySource} from '../../platform/tv-key-source';

type VideoMetadata = Readonly<{
  itemId: string; sourceToken: string; title: string; subtitle?: string; seriesTitle?: string;
  durationSeconds: number;
  live: boolean; canNext: boolean; canPrevious: boolean; skipBackSeconds: number; skipForwardSeconds: number;
}>;
export type NativeVideoSystemMediaPort = Readonly<{
  setVideo: (metadata: VideoMetadata) => void;
  clear: () => void;
  subscribe: (listener: (event: unknown) => void) => () => void;
}>;
type Context = Pick<NativeShellBootstrapContext, 'playback' | 'current' | 'authority' | 'subscribeAuthority' | 'preferences' | 'subscribePreferences'>;
let nativeOwner: object | undefined;
// Opaque per-binding token the native module echoes on each command.
let sourceSequence = 0;

/** One owner per restored authority; the listening engine retains its own session.
 * Vega already installs its source-bound SDK consumer and must not receive another. */
export function bindNativeVideoSystemMedia(context: Context, reportError: (error: unknown) => void): () => void {
  if (tvKeySource === 'vega') return () => {};
  const module = NativeModules.PorticoSystemMedia;
  if (typeof module?.setVideo !== 'function' || typeof module?.clear !== 'function') return () => {};
  const emitter = new NativeEventEmitter(module), owner = {}; nativeOwner = owner;
  let stop: () => void;
  try {
    stop = bindVideoSystemMedia(context, {
      setVideo: metadata => {if (nativeOwner === owner) module.setVideo(metadata);}, clear: () => {if (nativeOwner === owner) module.clear();},
      subscribe: listener => {const subscription = emitter.addListener('PorticoSystemMediaCommand', event => {if (nativeOwner === owner) listener(event);}); return () => subscription.remove();},
    }, reportError);
  } catch (error) {if (nativeOwner === owner) nativeOwner = undefined; throw error;}
  return () => {try {stop();} finally {if (nativeOwner === owner) nativeOwner = undefined;}};
}

/** The native module only reports a request. The original adapter owns effects,
 * capabilities and Watch Together policy; no TV-event transport path competes with it. */
export function bindVideoSystemMedia(context: Context, port: NativeVideoSystemMediaPort, reportError: (error: unknown) => void): () => void {
  const playback = context.playback, authority = Object.freeze({...context.authority()});
  let disposed = false, published = false, metadataKey: string | undefined;
  let binding: Readonly<{fence: PlaybackFence; lease: string; itemId: string; sourceToken: string}> | undefined;
  let offCommands: (() => void) | undefined;
  const current = () => !disposed && context.current() && context.playback === playback && sameAuthority(context.authority(), authority);
  const source = () => {
    if (!current()) return;
    const state = playback.getSnapshot(), local = state.dock.local, fence = state.fence;
    if (state.retired || !sameAuthority(state.authority, authority) || !fence || !local || !sameFence(local.fence, fence)
      || local.kind !== 'video' && local.kind !== 'live' || state.playback.nativeListening) return;
    return state;
  };
  const clear = () => {
    offCommands?.(); offCommands = undefined; binding = undefined; metadataKey = undefined;
    if (published) {published = false; port.clear();}
  };
  const update = () => {
    const state = source();
    if (!state?.fence) {clear(); return;}
    const fence = state.fence, itemId = fence.kind === 'channel' ? fence.channelId : fence.itemId;
    if (!binding || !sameFence(binding.fence, fence) || binding.lease !== state.lease) {
      offCommands?.(); offCommands = undefined; binding = undefined;
      const captured = {fence, lease: state.lease, itemId, sourceToken: `video-${++sourceSequence}`};
      offCommands = port.subscribe(event => {
        const live = source();
        if (!live || binding !== captured || live.lease !== captured.lease || !sameFence(live.fence, captured.fence)
          || !event || typeof event !== 'object') return;
        const command = event as {command?: unknown; itemId?: unknown; sourceToken?: unknown; positionSeconds?: unknown; deltaSeconds?: unknown};
        // The native event captured this token at dispatch. Matching the title
        // alone would admit an old event into a new session for the same title.
        if (command.itemId !== captured.itemId || command.sourceToken !== captured.sourceToken) return;
        const ready = live.playback.phase !== 'ended' && live.playback.phase !== 'error';
        let operation: Promise<void> | undefined;
        try {
          switch (command.command) {
            case 'play': if (ready && live.capabilities.play && live.playback.intent === 'paused') operation = playback.toggle(captured.fence); break;
            case 'pause': if (ready && live.capabilities.pause && live.playback.intent === 'playing') operation = playback.toggle(captured.fence); break;
            case 'toggle': if (ready && (live.playback.intent === 'playing' ? live.capabilities.pause : live.capabilities.play)) operation = playback.toggle(captured.fence); break;
            case 'seek': if (ready && live.capabilities.seek && typeof command.positionSeconds === 'number' && Number.isFinite(command.positionSeconds)) operation = playback.seek(captured.fence, command.positionSeconds); break;
            case 'skip': if (ready && live.capabilities.seek && typeof command.deltaSeconds === 'number' && Number.isFinite(command.deltaSeconds)) operation = playback.skip(captured.fence, command.deltaSeconds); break;
            case 'next': if (ready && live.capabilities.next) operation = playback.next(captured.fence); break;
            case 'previous': if (ready && live.capabilities.previous) operation = playback.previous(captured.fence); break;
          }
          void operation?.catch(error => {if (current() && binding === captured) reportError(error);});
        } catch (error) {if (current() && binding === captured) reportError(error);}
      });
      binding = captured;
    }
    const values = context.preferences().effective;
    const seconds = (key: string, fallback: number) => {
      const value = values[key];
      return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value / 1000 : fallback;
    };
    const entry = state.entry, linear = state.playback.linear;
    const metadata: VideoMetadata = {
      itemId, sourceToken: binding.sourceToken, title: linear?.programme?.title ?? entry?.displayTitle ?? entry?.title ?? linear?.name ?? '',
      ...(linear?.name ? {subtitle: linear.name} : entry?.show?.name ? {subtitle: entry.show.name, seriesTitle: entry.show.name} : {}),
      // Position, rate and play state come from the native player itself; publishing
      // them here would call setVideo on every progress tick.
      durationSeconds: state.playback.duration,
      live: fence.kind === 'channel', canNext: state.capabilities.next, canPrevious: state.capabilities.previous,
      skipBackSeconds: seconds('playback.skipBackMs', 10), skipForwardSeconds: seconds('playback.skipForwardMs', 30),
    };
    const nextKey = JSON.stringify(metadata);
    if (nextKey !== metadataKey) {port.setVideo(metadata); published = true; metadataKey = nextKey;}
  };
  const changed = () => {try {update();} catch (error) {if (current()) reportError(error);}};
  const subscriptions: (() => void)[] = [];
  const stop = () => {
    if (disposed) return;
    disposed = true;
    for (const off of subscriptions) off();
    clear();
  };
  try {
    subscriptions.push(playback.subscribe(changed));
    subscriptions.push(context.subscribeAuthority(changed));
    subscriptions.push(context.subscribePreferences(changed));
    changed();
  } catch (error) {stop(); throw error;}
  return stop;
}
