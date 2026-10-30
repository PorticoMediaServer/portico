import type {CollectionSnapshot, FetchPage, Slot, WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';

export type NativeReadingWindowState<T> = Readonly<{
  items: readonly T[];
  cursor?: string;
  loading: boolean;
  error?: unknown;
}>;

/** WindowedGrid's sparse presentation over an existing, cursor-owned reading
 * list. This adapter never invents an offset request or a total: one trailing
 * placeholder stands for the source's real continuation. */
export class NativeReadingWindow<T> implements WindowedCollection<T> {
  readonly pageSize = 50;
  private readonly listeners = new Set<() => void>();
  private stop?: () => void;
  private readonly read: () => NativeReadingWindowState<T>;
  private readonly listen: (notify: () => void) => () => void;
  private readonly more: () => Promise<void>;
  private readonly key: (item: T) => string;
  private value: NativeReadingWindowState<T>;
  private snapshot: CollectionSnapshot;
  private revision = 0;
  private requested?: string;
  private failure?: unknown;
  private last = -1;
  private disposed = false;
  constructor(read: () => NativeReadingWindowState<T>, subscribe: (notify: () => void) => () => void,
    more: () => Promise<void>, key: (item: T) => string) {
    this.read = read; this.listen = subscribe; this.more = more; this.key = key;
    this.value = read();
    this.snapshot = this.project();
  }
  private project(): CollectionSnapshot {
    return Object.freeze({generation: 0, total: this.value.items.length + (this.value.cursor ? 1 : 0),
      totalIsLowerBound: !!this.value.cursor, status: this.failure || this.value.error ? 'error' : this.value.loading ? 'loading' : 'ready',
      error: this.failure ?? this.value.error, positionIndex: undefined, refreshing: false});
  }
  private changed = (): void => {
    if (this.disposed) return;
    this.accept(this.read());
    this.revision++; this.snapshot = this.project();
    for (const notify of [...this.listeners]) notify();
  };
  private accept(value: NativeReadingWindowState<T>): void {
    if (value.items.length < this.value.items.length || !value.cursor) {this.requested = undefined; this.failure = undefined;}
    this.value = value;
  }
  getSnapshot = (): CollectionSnapshot => {
    const value = this.read();
    if (!this.disposed && (value.items !== this.value.items || value.cursor !== this.value.cursor || value.loading !== this.value.loading || value.error !== this.value.error)) {
      this.accept(value); this.revision++; this.snapshot = this.project();
    }
    return this.snapshot;
  };
  subscribe = (notify: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(notify);
    this.stop ??= this.listen(this.changed);
    return () => {this.listeners.delete(notify); if (!this.listeners.size) {this.stop?.(); this.stop = undefined;}};
  };
  subscribePage = (_page: number, notify: () => void): (() => void) => this.subscribe(notify);
  getPageVersion = (_page: number): string => String(this.revision);
  pageOf = (index: number): number => Math.floor(Math.max(0, index) / this.pageSize);
  itemAt = (index: number): T | undefined => this.value.items[index];
  keyAt = (index: number): string => this.itemAt(index) === undefined ? `reading-waiting:${index}` : this.key(this.itemAt(index)!);
  slotAt = (index: number): Slot<T> => {
    const item = this.itemAt(index), key = this.keyAt(index);
    return item === undefined ? {kind: 'placeholder', index, key} : {kind: 'item', index, key, item};
  };
  ensureRange = (_first: number, last: number): void => {
    this.last = last;
    const cursor = this.value.cursor;
    if (this.disposed || !cursor || this.value.loading || this.value.error || this.failure || this.requested === cursor
      || last < this.value.items.length - 10) return;
    this.requested = cursor;
    void this.more().catch(error => {
      if (this.disposed) return;
      this.failure = error; this.changed();
    });
  };
  /** Every fetched item stays resident here (the source owns the list), so nothing needs pinning. */
  pin = (_index: number | undefined): void => {};
  indexOfLetter = (_letter: string): number | undefined => undefined;
  invalidate = (fetchPage?: FetchPage<T>): void => {
    if (fetchPage) throw new Error('reading_window_source_owned');
    this.failure = undefined; this.requested = undefined; this.changed();
  };
  revalidate = async (): Promise<void> => {throw new Error('reading_window_source_owned');};
  retry = (): void => {this.failure = undefined; this.requested = undefined; this.changed(); this.ensureRange(0, this.last);};
  resident = (): {pages: number; items: number} => ({pages: Math.ceil(this.value.items.length / this.pageSize), items: this.value.items.length});
  residentItems = (): readonly T[] => this.value.items;
  dispose = (): void => {if (!this.disposed) {this.disposed = true; this.stop?.(); this.stop = undefined; this.listeners.clear();}};
}
