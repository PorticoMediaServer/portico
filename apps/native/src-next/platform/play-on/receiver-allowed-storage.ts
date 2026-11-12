import type {AllowedDevices} from '../../../../../packages/client-core/src/remote-playback';
import {NativeQueueStore} from '../queue-storage-legacy';

/**
 * The devices this TV's person allowed to play on it and control it (area 8, rule 23), each with
 * the name it had when allowed. The TV remembers them itself, so a restart of the server does not
 * ask again, and This device lists them with Forget. They belong to this TV's sign-in: signing
 * the TV out and in again starts a new list, and a sending device that signs in again is a new
 * sign-in and is asked again.
 *
 * Held in memory and kept in the device's protected record store behind it. A store that cannot
 * be read or written only means the person is asked again. The TV receiver and the settings row
 * share one list per sign-in, so a device forgotten in Settings is asked again at once.
 */
const KEY = 'receiver.allowed:v2';
/** A TV meets a handful of phones; the oldest are let go beyond this. */
const LIMIT = 64;
const store = new NativeQueueStore();

type Entry = Readonly<{signInId: string; name: string}>;
type Saved = Readonly<{scope: string; devices: readonly Entry[]}>;
const read = (raw: unknown, scope: string): Entry[] => {
  const v = raw as Partial<Saved> | null;
  if (!v || v.scope !== scope || !Array.isArray(v.devices)) return [];
  return v.devices.filter((d): d is Entry => !!d && typeof d.signInId === 'string' && !!d.signInId && d.signInId.length <= 128 && typeof d.name === 'string' && d.name.length <= 200).slice(-LIMIT);
};

export type NativeAllowedDevices = AllowedDevices & Readonly<{list(): readonly Entry[]; forget(signInId: string): void; subscribe(listener: () => void): () => void}>;
const lists = new Map<string, Promise<NativeAllowedDevices>>();

/** The list for one TV sign-in (`scope`: the server, the account and the sign-in). Ready once
 * what was saved has been read. */
export function receiverAllowedDevices(scope: string): Promise<NativeAllowedDevices> {
  let list = lists.get(scope);
  if (!list) {
    for (const key of [...lists.keys()]) lists.delete(key);
    list = open(scope);
    lists.set(scope, list);
  }
  return list;
}

async function open(scope: string): Promise<NativeAllowedDevices> {
  const held = new Map<string, string>((await store.change(KEY, raw => ({record: raw, value: read(raw, scope)})).catch(() => [] as Entry[])).map(d => [d.signInId, d.name]));
  const changed = new Set<() => void>(), forgot = new Set<() => void>();
  let snapshot: readonly Entry[] = Object.freeze([...held].map(([signInId, name]) => Object.freeze({signInId, name})));
  const save = () => {
    snapshot = Object.freeze([...held].map(([signInId, name]) => Object.freeze({signInId, name})));
    for (const fn of [...changed]) fn();
    void store.change(KEY, () => ({record: {scope, devices: snapshot}, value: undefined})).catch(() => {});
  };
  return {
    has: id => held.has(id),
    add: (id, name = '') => {
      if (!id || id.length > 128) return;
      held.delete(id); held.set(id, name.slice(0, 200));
      while (held.size > LIMIT) held.delete(held.keys().next().value as string);
      save();
    },
    list: () => snapshot,
    forget: id => {if (!held.delete(id)) return; save(); for (const fn of [...forgot]) fn();},
    onForget: fn => {forgot.add(fn); return () => {forgot.delete(fn);};},
    subscribe: fn => {changed.add(fn); return () => {changed.delete(fn);};},
  };
}
