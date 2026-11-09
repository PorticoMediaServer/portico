import type {OutboxEntry, OutboxStore} from '../../../../packages/client-core/src/outbox';
import {NativeQueueStore} from './queue-storage';

const KEY = 'outbox';
const entriesOf = (raw: unknown): OutboxEntry[] => Array.isArray(raw) ? raw as OutboxEntry[] : [];

/** The outbox's entries in the app's durable record store, as one record. A record that would grow
 * past the store's size limit is refused, and the write that would have added to it reports that. */
export class NativeOutboxStore implements OutboxStore {
  private readonly records: Pick<NativeQueueStore, 'change'>;
  constructor(records: Pick<NativeQueueStore, 'change'> = new NativeQueueStore()) { this.records = records; }
  list(): Promise<OutboxEntry[]> { return this.records.change(KEY, raw => ({record: raw, value: entriesOf(raw)})); }
  put(entry: OutboxEntry): Promise<void> {
    return this.records.change(KEY, raw => ({record: [...entriesOf(raw).filter(each => each.id !== entry.id), entry], value: undefined}));
  }
  delete(id: string): Promise<void> {
    return this.records.change(KEY, raw => ({record: entriesOf(raw).filter(each => each.id !== id), value: undefined}));
  }
}
