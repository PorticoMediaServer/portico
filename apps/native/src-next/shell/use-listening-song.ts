import {useCallback, useLayoutEffect, useRef, useState} from 'react';
import type {PlaybackFence} from '../../../../packages/player-core/src';
import type {NativePlaybackReadLifetime} from '../platform/playback/types';
import type {NativeListeningSong, NativeListeningSongDetail} from './listening-song-detail';

/** The current song's issued detail token, read under one listening opening and fence.
 * Nothing here defaults a favorite, and nothing acts on a token from another page. */
export type NativeListeningSongState = Readonly<{
  song?: NativeListeningSong;
  pending: boolean;
  favorite: (token: NativeListeningSong, value: boolean) => void;
}>;

type Opening = Readonly<{
  port: NativeListeningSongDetail;
  contentKey: string;
  fence: PlaybackFence;
  lifetime: NativePlaybackReadLifetime;
  /** The original opening's own route/fence proof, captured when this opening committed. */
  live: () => boolean;
}>;

/** The immutable tuple that admits one reading. It is keyed by the exact port, fence content
 * and lifetime, and holds the fence object from the render that first named that key. A
 * progress tick that repeats the same content under a new object identity is the same
 * opening, so it keeps this exact fence rather than reopening the read. */
type Admission = Readonly<{
  port: NativeListeningSongDetail;
  contentKey: string;
  fence: PlaybackFence;
  lifetime: NativePlaybackReadLifetime;
}>;
const retainAdmission = (held: Admission | undefined, next: Admission | undefined): Admission | undefined =>
  held && next && held.port === next.port && held.contentKey === next.contentKey && held.lifetime === next.lifetime ? held : next;

/** Read the exact queue song's detail while the original opening and fence are still current.
 * The opening is immutable and committed in the layout phase. Every read, dispatch and
 * settlement is checked against that exact opening and its ticket, never against a later one. */
export function useListeningSong(detail: NativeListeningSongDetail | undefined, fence: PlaybackFence | undefined,
  lifetime: NativePlaybackReadLifetime | undefined, current: () => boolean): NativeListeningSongState {
  const [state, setState] = useState<Readonly<{opening?: Opening; song?: NativeListeningSong; pending: boolean}>>(() => ({pending: false}));
  const [admission, setAdmission] = useState<Admission | undefined>(undefined);
  const openingRef = useRef<Opening | undefined>(undefined), pendingRef = useRef<Opening | undefined>(undefined), ticket = useRef(0);
  const tokens = useRef(new WeakMap<object, Opening>()), mounted = useRef(true), currentRef = useRef(current);
  currentRef.current = current;
  const fenceKey = fence ? JSON.stringify(fence) : '';
  // Bounded eligibility for exactly this fence and lifetime. It changes when the accepted window
  // arrives or retires, not on progress ticks, and opens or closes the reading with it.
  const canRead = !!detail && !!fence && !!lifetime && detail.canRead(fence, lifetime);
  const next = canRead && detail && fence && lifetime ? Object.freeze({port: detail, contentKey: fenceKey, fence, lifetime}) : undefined;
  const admitted = retainAdmission(admission, next);
  // Adjust during this render, before the layout effect below commits: React retries the render
  // with the retained admission, so the effect is keyed by the exact opening tuple while a
  // same-content progress tick keeps the original fence object and opens nothing.
  if (admitted !== admission) setAdmission(admitted);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {mounted.current = false; ticket.current += 1; openingRef.current = undefined; pendingRef.current = undefined;};
  }, []);
  useLayoutEffect(() => {
    // Retiring is the previous opening's own layout cleanup, not this setup's first act: it
    // runs before any other layout setup in the transition, and only then may this opening
    // publish. A same-content tick never reaches here because its admission did not change.
    const at = ++ticket.current;
    openingRef.current = undefined; pendingRef.current = undefined;
    if (mounted.current) setState(() => ({pending: false}));
    if (!admitted) return;
    const owner = currentRef.current;
    const opening: Opening = Object.freeze({...admitted, live: () => admitted.lifetime.isCurrent() && owner()});
    openingRef.current = opening;
    void admitted.port.read(admitted.fence, admitted.lifetime).then(song => {
      if (!mounted.current || openingRef.current !== opening || ticket.current !== at || !opening.live()) return;
      if (song) tokens.current.set(song, opening);
      setState({opening, ...(song ? {song} : {}), pending: false});
    }).catch(() => {});
    // Retiring is layout work: no state setter runs on unmount, only the refs leave.
    return () => {
      if (openingRef.current === opening) openingRef.current = undefined;
      if (pendingRef.current === opening) pendingRef.current = undefined;
    };
  }, [admitted]);
  const favorite = useCallback((song: NativeListeningSong, value: boolean) => {
    const opening = tokens.current.get(song);
    // The token itself names the opening that issued it: a retained press can never borrow
    // whatever opening is current now, and one command ticket runs at a time.
    if (!opening || openingRef.current !== opening || pendingRef.current || !opening.live()) return;
    pendingRef.current = opening;
    const at = ++ticket.current;
    if (mounted.current) setState(previous => previous.opening === opening ? {...previous, pending: true} : previous);
    void opening.port.favorite(song, value, opening.lifetime).then(accepted => {
      // Only this opening's own accepted answer may read again; a stale or refused command
      // never refreshes whatever opening happens to be current.
      if (!mounted.current || openingRef.current !== opening || ticket.current !== at || !opening.live()) return;
      if (accepted !== true) return;
      return opening.port.read(opening.fence, opening.lifetime).then(next => {
        if (!mounted.current || openingRef.current !== opening || ticket.current !== at || !opening.live()) return;
        if (next) tokens.current.set(next, opening);
        setState({opening, ...(next ? {song: next} : {}), pending: false});
      });
    }).catch(() => {}).finally(() => {
      if (!mounted.current || openingRef.current !== opening || ticket.current !== at) return;
      if (pendingRef.current === opening) pendingRef.current = undefined;
      // A vanished lifetime releases its own ticket and opening without publishing a UI state
      // for a page that is no longer the original opening.
      if (!opening.live()) {
        if (openingRef.current === opening) openingRef.current = undefined;
        return;
      }
      setState(previous => previous.opening === opening && previous.pending ? {...previous, pending: false} : previous);
    });
  }, []);
  return {...(canRead && state.opening && state.song ? {song: state.song} : {}), pending: canRead && state.opening ? state.pending : false, favorite};
}
