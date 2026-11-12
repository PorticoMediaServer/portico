import type {AllowedDevices} from '../../../../../packages/client-core/src/remote-playback';
import {NativeQueueStore} from '../queue-storage-legacy';

/**
 * The devices this TV's person allowed to play on it and control it (area 8, rule 23). The TV
 * remembers them itself, so a restart of the server does not ask again. They belong to this TV's
 * sign-in: signing the TV out and in again starts a new list, and a sending device that signs in
 * again is a new sign-in and is asked again.
 *
 * Held in memory and kept in the device's protected record store behind it. A store that cannot
 * be read or written only means the person is asked again.
 */
const KEY = 'receiver.allowed:v1';
/** A TV meets a handful of phones; the oldest are let go beyond this. */
const LIMIT = 64;
const store = new NativeQueueStore();

type Saved = Readonly<{scope: string; ids: readonly string[]}>;
const read = (raw: unknown, scope: string): string[] => {
  const v = raw as Partial<Saved> | null;
  return v && v.scope === scope && Array.isArray(v.ids) ? v.ids.filter((id): id is string => typeof id === 'string' && !!id && id.length <= 128).slice(-LIMIT) : [];
};

/** The list for one TV sign-in (`scope`: the server, the account and the sign-in). Ready once
 * what was saved has been read. */
export async function receiverAllowedDevices(scope: string): Promise<AllowedDevices> {
  const held = new Set<string>(await store.change(KEY, raw => ({record: raw, value: read(raw, scope)})).catch(() => []));
  return {
    has: id => held.has(id),
    add: id => {
      if (!id || id.length > 128) return;
      held.delete(id); held.add(id);
      while (held.size > LIMIT) held.delete(held.values().next().value as string);
      void store.change(KEY, () => ({record: {scope, ids: [...held]}, value: undefined})).catch(() => {});
    },
  };
}
