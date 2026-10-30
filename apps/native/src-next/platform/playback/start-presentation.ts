import type {ArtworkSource} from '../../kit/artwork';
import {playbackFence, sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import type {NativeBinding} from './NativePlaybackCoordinator';

export type NativeStartArtworkBounds = Readonly<{x: number; y: number; width: number; height: number}>;
/** The caller starts this actual native measurement on the original press.
 * Playback never waits for it and no dock/latest-source rectangle substitutes. */
export type NativeStartArtworkOffer = Readonly<{
  artwork: ArtworkSource; bounds: Promise<NativeStartArtworkBounds | undefined>;
  /** Only further restricts paint to the measured view/layout, never a command. */
  paintCurrent(): boolean;
  subscribe?(listener: () => void): () => void;
}>;
export type NativeStartPresentation = Readonly<{
  key: object; artwork: ArtworkSource; bounds: NativeStartArtworkBounds; fence: PlaybackFence;
  signal: AbortSignal; paintCurrent(): boolean; subscribe(listener: () => void): () => void;
}>;
const valid = (read: () => boolean) => {try {return read() === true;} catch {return false;}};
const validBounds = (value: NativeStartArtworkBounds | undefined): value is NativeStartArtworkBounds => !!value
  && [value.x, value.y, value.width, value.height].every(Number.isFinite) && value.width > 0 && value.height > 0;

/** One start's pixels, separate from its finished command/retired preparation.
 * This object carries no playback action, HTTP API or renderer ownership. */
export function captureNativeStartPresentation(offer: NativeStartArtworkOffer | undefined, original: Readonly<{
  current(): boolean; sourceCurrent(fence: PlaybackFence): boolean; subscribe(listener: () => void): () => void; changed(): void;
}>) {
  if (!offer || !valid(original.current)) return undefined;
  const key = Object.freeze({}), life = new AbortController(), listeners = new Set<() => void>();
  let bounds: NativeStartArtworkBounds | undefined, fence: PlaybackFence | undefined, view: NativeStartPresentation | undefined;
  const offs: (() => void)[] = [], off = () => {for (const stop of offs.splice(0)) stop();};
  let artwork: ArtworkSource, measured: NativeStartArtworkOffer['bounds'], originalArtCurrent: () => boolean;
  let subscribeArt: NativeStartArtworkOffer['subscribe'];
  try {
    const source = offer.artwork;
    artwork = Object.freeze({uri: source.uri, cacheKey: source.cacheKey, ...(source.headers ? {headers: Object.freeze({...source.headers})} : {})});
    measured = offer.bounds;
    originalArtCurrent = offer.paintCurrent;
    subscribeArt = offer.subscribe;
    if (!artwork.uri || !artwork.cacheKey || !valid(originalArtCurrent) || !valid(original.current)) return undefined;
  } catch {return undefined;}
  const paintCurrent = () => !life.signal.aborted && valid(original.current) && valid(originalArtCurrent)
    && (!fence || valid(() => original.sourceCurrent(fence!))) && valid(original.current) && !life.signal.aborted;
  const subscribe = (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};};
  const publish = () => {for (const listener of [...listeners]) listener(); original.changed();};
  const retire = () => {if (!life.signal.aborted) {life.abort();off();view = undefined;publish();}};
  const refresh = (notify = true) => {
    if (!paintCurrent()) {retire();return;}
    if (bounds && fence) view = Object.freeze({key, artwork, bounds, fence, signal: life.signal, paintCurrent, subscribe});
    if (notify) publish();
  };
  const check = () => {if (!paintCurrent()) retire();};
  try {
    offs.push(original.subscribe(check));
    if (paintCurrent() && subscribeArt) offs.push(subscribeArt(check));
  } catch {off();retire();return undefined;}
  // Registration callbacks may retire synchronously before returning their off.
  if (!paintCurrent()) {off();retire();return undefined;}
  void Promise.resolve(measured).then(value => {
    try {
      if (!paintCurrent() || !validBounds(value)) {retire();return;}
      bounds = Object.freeze({x: value.x, y: value.y, width: value.width, height: value.height});refresh();
    } catch {retire();}
  }, retire);
  return Object.freeze({retire, bind: (accepted: PlaybackFence, notify = true) => {
    if (!paintCurrent() || fence) return;
    fence = playbackFence(accepted);refresh(notify);
  }, read: (accepted: PlaybackFence | undefined): NativeStartPresentation | undefined =>
    view && accepted && sameFence(view.fence, accepted) && paintCurrent() ? view : undefined});
}

export type NativeStartPresentationOwner = NonNullable<ReturnType<typeof captureNativeStartPresentation>>;
export type NativeFirstVideoFrame = NativeBinding & Readonly<{engine: 'native' | 'mpv'; instanceId: string}>;

/** Only coordinator-admitted facts may reach here. Other readiness facts never
 * become first-frame proof, and no missing/mismatched binding is substituted. */
export function nativeFirstVideoFrame(fact: unknown, binding: NativeBinding | undefined, engine: string, instanceId: string | undefined): NativeFirstVideoFrame | undefined {
  if (!binding || !instanceId || !['native', 'mpv'].includes(engine) || !fact || typeof fact !== 'object' || Array.isArray(fact)) return undefined;
  const value = fact as {type?: unknown; instanceId?: unknown; binding?: Partial<NativeBinding>};
  if (value.instanceId !== instanceId) return undefined;
  if (value.type !== 'firstFrame' || !value.binding || typeof value.binding !== 'object' || Array.isArray(value.binding)
    || (['lease', 'intentId', 'sessionId', 'generation', 'itemId'] as const).some(field => value.binding![field] !== binding[field])) return undefined;
  return Object.freeze({...binding, engine: engine as 'native' | 'mpv', instanceId});
}
