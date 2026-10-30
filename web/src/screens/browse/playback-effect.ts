import type {ShellBindings} from '../../shell/bindings';
import type {ScreenContext} from '../detail/context';

/** The shell builds this viewer's player from the session, so it exists whether or not Home
 * loaded. If it is still absent (the shell failed to build it), Play is refused out loud: an
 * AbortError would be presented as a cancel and the press would silently do nothing (P2-74). */
function playbackNotReady(): Error {
  return Object.assign(new Error('Playback is still loading'), {code: 'operation_unavailable'});
}

type Runtime = Pick<NonNullable<ShellBindings['playback']>['runtime'], 'playFromScreen'>;

/** The same real runtime owns admission and adoption. Capture its identity and
 * the original caller lifetime; never resolve a later runtime to finish a start. */
export function createScreenPlayEffect(input: Readonly<{isCurrent: () => boolean; runtime: () => Runtime | undefined}>): ScreenContext['effects']['play'] {
  const authorityCurrent = input.isCurrent.bind(input), getRuntime = input.runtime.bind(input);
  return async (target, signal, callerCurrent = () => true) => {
    signal.throwIfAborted();
    if (!authorityCurrent() || !callerCurrent()) throw new DOMException('Screen playback changed', 'AbortError');
    const runtime = getRuntime();
    if (!runtime) throw playbackNotReady();
    const current = () => !signal.aborted && authorityCurrent() && callerCurrent() && getRuntime() === runtime;
    if (!current()) throw new DOMException('Screen playback changed', 'AbortError');
    await runtime.playFromScreen(target, {signal, isCurrent: current});
    signal.throwIfAborted();
    if (!current()) throw new DOMException('Screen playback changed', 'AbortError');
  };
}

/** Whole selected season; the server snapshots canonical complete membership.
 * Workspace filters, viewer ordering and loaded item arrays never enter this effect. */
export function createScreenSeasonPlayEffect(input: Readonly<{isCurrent: () => boolean; runtime: () => Pick<NonNullable<ShellBindings['playback']>['runtime'], 'playFromQueue'> | undefined}>): NonNullable<ScreenContext['effects']['playSeason']> {
 const authorityCurrent = input.isCurrent.bind(input), getRuntime = input.runtime.bind(input);
 return async (seasonId, signal, callerCurrent) => {
  const capturedSeasonId = seasonId;
  signal.throwIfAborted();
  if (!authorityCurrent() || !callerCurrent()) throw new DOMException('Screen playback changed', 'AbortError');
  const runtime = getRuntime();
  if (!runtime) throw playbackNotReady();
  const current = () => !signal.aborted && authorityCurrent() && callerCurrent() && getRuntime() === runtime;
  if (!current()) throw new DOMException('Screen playback changed', 'AbortError');
  await runtime.playFromQueue({container: {kind: 'season', id: capturedSeasonId}}, {signal, isCurrent: current});
  signal.throwIfAborted();
  if (!current()) throw new DOMException('Screen playback changed', 'AbortError');
 };
}

/** The server's artist selector snapshots discography order and admits every
 * song. A search's loaded albums and text predicates are never membership. */
export function createScreenArtistPlayEffect(input: Readonly<{isCurrent: () => boolean; runtime: () => Pick<NonNullable<ShellBindings['playback']>['runtime'], 'playFromQueue'> | undefined}>): NonNullable<ScreenContext['effects']['playArtist']> {
  const authorityCurrent = input.isCurrent.bind(input), getRuntime = input.runtime.bind(input);
  return async (artistId, signal, callerCurrent, shuffle = false) => {
    const capturedArtistId = artistId, capturedShuffle = shuffle;
    signal.throwIfAborted();
    if (!authorityCurrent() || !callerCurrent()) throw new DOMException('Search playback changed', 'AbortError');
    const runtime = getRuntime();
    if (!runtime) throw playbackNotReady();
    const current = () => !signal.aborted && authorityCurrent() && callerCurrent() && getRuntime() === runtime;
    if (!current()) throw new DOMException('Search playback changed', 'AbortError');
    await runtime.playFromQueue({container: {kind: 'artist', id: capturedArtistId}}, {signal, isCurrent: current}, {shuffle: capturedShuffle});
    signal.throwIfAborted();
    if (!current()) throw new DOMException('Search playback changed', 'AbortError');
  };
}

/** Whole album in the server's disc/track order (or its own shuffle), not the
 * loaded track page. Keep the original page and runtime across admission. */
export function createScreenAlbumPlayEffect(input: Readonly<{isCurrent: () => boolean; runtime: () => Pick<NonNullable<ShellBindings['playback']>['runtime'], 'playFromQueue'> | undefined}>): NonNullable<ScreenContext['effects']['playAlbum']> {
  const authorityCurrent = input.isCurrent.bind(input), getRuntime = input.runtime.bind(input);
  return async (albumId, shuffle, signal, callerCurrent) => {
    const capturedAlbumId = albumId, capturedShuffle = shuffle;
    signal.throwIfAborted();
    if (!authorityCurrent() || !callerCurrent()) throw new DOMException('Album playback changed', 'AbortError');
    const runtime = getRuntime();
    if (!runtime) throw playbackNotReady();
    const current = () => !signal.aborted && authorityCurrent() && callerCurrent() && getRuntime() === runtime;
    if (!current()) throw new DOMException('Album playback changed', 'AbortError');
    await runtime.playFromQueue({container: {kind: 'album', id: capturedAlbumId}}, {signal, isCurrent: current}, {shuffle: capturedShuffle});
    signal.throwIfAborted();
    if (!current()) throw new DOMException('Album playback changed', 'AbortError');
  };
}

/** Radio membership and refill are the server's seeded selector. A screen never
 * ranks songs or constructs an endlessly growing item list in the browser. */
export function createScreenRadioPlayEffect(input: Readonly<{isCurrent: () => boolean; runtime: () => Pick<NonNullable<ShellBindings['playback']>['runtime'], 'playFromQueue'> | undefined}>): NonNullable<ScreenContext['effects']['playRadio']> {
  const authorityCurrent = input.isCurrent.bind(input), getRuntime = input.runtime.bind(input);
  return async (seedId, signal, callerCurrent) => {
    const capturedSeedId = seedId;
    signal.throwIfAborted();
    if (!authorityCurrent() || !callerCurrent()) throw new DOMException('Radio seed changed', 'AbortError');
    const runtime = getRuntime();
    if (!runtime) throw playbackNotReady();
    const current = () => !signal.aborted && authorityCurrent() && callerCurrent() && getRuntime() === runtime;
    if (!current()) throw new DOMException('Radio seed changed', 'AbortError');
    await runtime.playFromQueue({container: {kind: 'radio', id: capturedSeedId}}, {signal, isCurrent: current});
    signal.throwIfAborted();
    if (!current()) throw new DOMException('Radio seed changed', 'AbortError');
  };
}
