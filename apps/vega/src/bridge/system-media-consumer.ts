import type {NativeShellBootstrapContext} from '../../../native/src-next/shell/bootstrap';
import {sameAuthority} from '../../../native/src-next/shell/model';
import type {NativeBinding} from '../../../native/src-next/platform/playback/NativePlaybackCoordinator';
import {audiobookPreferences} from '../../../../packages/client-core/src/audiobook-preferences';
import {PorticoSystemMedia, systemMediaCommandCurrent, systemMediaEvents} from './system-media';

const sameBinding = (a: NativeBinding, b: NativeBinding) => a.lease === b.lease && a.intentId === b.intentId
  && a.sessionId === b.sessionId && a.generation === b.generation && a.itemId === b.itemId;

/** One restored authority and the original adapter object, after TV group policy decorates it. */
export function bindVegaSystemMedia(context: NativeShellBootstrapContext): () => void {
  const adapter = context.playback, owner = {}, authority = Object.freeze({...context.authority()});
  let disposed = false;
  const current = () => !disposed && context.current() && context.playback === adapter && sameAuthority(context.authority(), authority);
  const source = () => {
    if (!current()) return;
    const state = adapter.getSnapshot(), binding = adapter.runtime.coordinator.getBinding();
    if (!sameAuthority(state.authority, authority) || state.engine !== 'native' || state.playback.nativeListening || !state.fence || !binding
      || state.lease !== binding.lease || state.playback.intentId !== binding.intentId || state.playback.itemId !== binding.itemId
      || state.playback.session?.id !== binding.sessionId || state.playback.session.generation !== binding.generation) return;
    return {state, binding};
  };
  const update = () => {
    const active = source();
    if (!active) {PorticoSystemMedia.clear(owner); return;}
    const preferences = context.preferences();
    let back: number | undefined, forward: number | undefined;
    if (active.state.dock.local?.kind === 'audiobook') {
      const book = audiobookPreferences(preferences);
      back = book.skipBackMs / 1000; forward = book.skipForwardMs / 1000;
    } else {
      const value = preferences.effective['playback.skipBackMs'];
      if (typeof value === 'number' && Number.isFinite(value) && value >= 0) back = forward = value / 1000;
    }
    PorticoSystemMedia.setVideo({itemId: active.binding.itemId, skipBackSeconds: back, skipForwardSeconds: forward}, owner, active.binding);
  };
  const subscription = systemMediaEvents.addListener('PorticoSystemMediaCommand', (command: unknown) => {
    if (!systemMediaCommandCurrent(command, owner)) return;
    const active = source();
    if (!active || !sameBinding(active.binding, command.source.binding)) return;
    const {state} = active, fence = state.fence!;
    // Explicit Play/Pause never invert an already satisfied intent or turn Ended into Replay.
    const ready = state.playback.phase !== 'ended' && state.playback.phase !== 'error';
    let operation: Promise<void> | undefined;
    try {
      switch (command.command) {
        case 'play': if (ready && state.capabilities.play && state.playback.intent === 'paused') operation = adapter.toggle(fence); break;
        case 'pause': if (ready && state.capabilities.pause && state.playback.intent === 'playing') operation = adapter.toggle(fence); break;
        case 'toggle': if (ready && (state.playback.intent === 'playing' ? state.capabilities.pause : state.capabilities.play)) operation = adapter.toggle(fence); break;
        case 'seek': if (state.capabilities.seek && typeof command.positionSeconds === 'number' && Number.isFinite(command.positionSeconds)) operation = adapter.seek(fence, command.positionSeconds); break;
        case 'skip': if (state.capabilities.seek && typeof command.deltaSeconds === 'number' && Number.isFinite(command.deltaSeconds)) operation = adapter.skip(fence, command.deltaSeconds); break;
        case 'next': if (state.capabilities.next) operation = adapter.next(fence); break;
        case 'previous': if (state.capabilities.previous) operation = adapter.previous(fence); break;
      }
      // Runtime and group policy own effects/refusals. Forwarding an SDK event is not a play receipt.
      void operation?.catch(() => {});
    } catch { /* A retired authority cannot dispatch a system request. */ }
  });
  const cleanups: (() => void)[] = [() => subscription.remove()];
  const retire = () => {
    if (disposed) return;
    disposed = true;
    for (const cleanup of cleanups.splice(0)) {try {cleanup();} catch { /* retire every original subscription */ }}
    PorticoSystemMedia.clear(owner);
  };
  try {
    cleanups.push(adapter.subscribe(update));
    cleanups.push(context.subscribePreferences(update));
    cleanups.push(context.subscribeAuthority(update));
    update();
  } catch (error) {retire(); throw error;}
  return retire;
}
