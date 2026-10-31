import {SlotSetupStore, type SetupSlot} from '../../../../packages/client-core/src/setup-slot-store';
import {NativeQueueStore} from './queue-storage';

/** What a slot holds once retired: the native store never writes nothing over damaged bytes, so retiring leaves this. */
const CLEARED = Object.freeze({cleared: true});
const damagedRecord = () => Object.assign(new Error('This setup record can no longer be read.'), {code: 'invalid_saved_connection'});
const held = (raw: unknown) => raw == null || typeof raw === 'object' && (raw as {cleared?: unknown}).cleared === true ? undefined : raw;

/** One protected Keychain record. A read reports damaged bytes; a change sees them as absent and replaces them. */
export function nativeSetupSlot(key: string, storage = new NativeQueueStore()): SetupSlot {
  return {
    assertAvailable: () => {},
    read: () => storage.change(key, raw => ({record: raw, value: held(raw)}), {error: damagedRecord}),
    change: async update => {
      await storage.change(key, raw => {const next = update(held(raw)); return {record: next === undefined ? CLEARED : next, value: undefined};}, {error: damagedRecord, replace: true});
    },
  };
}

/** This app's setup records for one server (B-DESIGN §6.3): the record and the attempt, each per server, since the app has
 * no tabs. An unreadable or older record reads as unreadable and is retired (R-13), never thrown forever. */
export function nativeSetupRecords(serverId: string): SlotSetupStore {
  return new SlotSetupStore(serverId, {record: nativeSetupSlot('native.setup.record.v2:' + serverId), attempt: nativeSetupSlot('native.setup.attempt.v2:' + serverId)});
}
