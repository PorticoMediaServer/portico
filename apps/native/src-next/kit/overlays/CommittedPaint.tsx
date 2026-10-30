import React, {createContext, useCallback, useContext, useLayoutEffect, useRef, useState} from 'react';
import {useOverlayPaintMode} from './OverlayPaintContext';

type Packet = Readonly<{scope: string; data: unknown; actions: unknown; current: () => boolean}>;
type PaintMap = ReadonlyMap<object, ReadonlyMap<string, Packet>>;
type Publish = (owner: object, channel: string, value: Packet) => () => void;
const Publication = createContext<Publish | undefined>(undefined);
const Values = createContext<PaintMap>(new Map());

/** useMemo's semantic inputs decide when to issue a fresh UI token. None of
 * those inputs (especially a live service) is retained inside the token. */
export function committedPaintScope(..._inputs: readonly unknown[]): object {return Object.freeze({});}

/** Authority-local presentation plumbing. Live publishers own every operation;
 * pure leaves keep only their own last committed display after retirement. */
export function CommittedPaintProvider({children}: {children: React.ReactNode}) {
  const [values, setValues] = useState<PaintMap>(() => new Map());
  const publish = useCallback<Publish>((owner, channel, value) => {
    setValues(current => {
      const channels = new Map<string, Packet>(current.get(owner));channels.set(channel, value);
      return new Map(current).set(owner, channels);
    });
    return () => setValues(current => {
      const channels = current.get(owner);
      if (channels?.get(channel) !== value) return current;
      const nextChannels = new Map(channels);nextChannels.delete(channel);
      const next = new Map(current);
      if (nextChannels.size) next.set(owner, nextChannels);else next.delete(owner);
      return next;
    });
  }, []);
  return <Publication.Provider value={publish}><Values.Provider value={values}>{children}</Values.Provider></Publication.Provider>;
}

/** Semantic is a UI lifecycle token, not authority/admission. Ordinary display
 * updates keep that original lifecycle; actual replacement/effect replay gets a
 * fresh activation. A queued callback never reads a successor's actions. */
export function useCommittedPaintPublication<Data, Actions>(owner: object | undefined, channel: string, scope: string, semantic: object,
  value: Readonly<{data: Data; actions: Actions}> | undefined) {
  const publish = useContext(Publication);
  const lifetime = useRef<Readonly<{semantic: object; live: {value: boolean}}> | undefined>(undefined);
  useLayoutEffect(() => {
    if (!owner) return;
    const original = {semantic, live: {value: true}};lifetime.current = original;
    return () => {original.live.value = false;if (lifetime.current === original) lifetime.current = undefined;};
  }, [owner, channel, scope, semantic]);
  useLayoutEffect(() => {
    const original = lifetime.current;
    if (!publish || !owner || !value || !original || original.semantic !== semantic) return;
    return publish(owner, channel, {scope, data: value.data, actions: value.actions, current: () => original.live.value});
  }, [publish, owner, channel, scope, semantic, value]);
}

/** Data must be an audited display projection: no service, controller, lease or
 * resolver. It stays in this leaf's committed UI ref, never in frozen props.
 * Actions are live only and are deliberately absent from that retained ref. */
export function useCommittedPaint<Data, Actions>({owner, channel, scope, value}: {
  owner?: object; channel: string; scope: string; value?: Readonly<{data: Data; actions: Actions}>;
}) {
  const values = useContext(Values), mode = useOverlayPaintMode();
  const packet = mode.interactive && owner ? values.get(owner)?.get(channel) : undefined;
  const live = mode.interactive && !!packet && packet.scope === scope && packet.current();
  const direct = !owner && value;
  const committed = useRef<Readonly<{owner: object | undefined; scope: string; data: Data}> | undefined>(undefined);
  const previous = committed.current;
  const data = !mode.visible ? undefined : live ? packet.data as Data : mode.interactive && direct ? direct.data
    : previous && previous.owner === owner && previous.scope === scope ? previous.data : undefined;
  const interactive = mode.interactive && (live || !!direct);
  useLayoutEffect(() => {
    if (mode.visible && interactive && data !== undefined) committed.current = {owner, scope, data};
  }, [mode.visible, interactive, owner, scope, data]);
  return {data, interactive, actions: live ? packet.actions as Actions : direct ? direct.actions : undefined,
    current: () => mode.interactive && (owner ? !!packet && packet.scope === scope && packet.current() : !!value)};
}
