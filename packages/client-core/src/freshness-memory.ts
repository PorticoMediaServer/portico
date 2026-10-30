/** Accounted transient history, not user content. Strings are charged at UTF-16 size
 * plus per-entry overhead (including Map/Set indexing), independent of engine layout. */
export const FRESHNESS_HISTORY_ENTRIES = 2048;
export const FRESHNESS_HISTORY_BYTES = 512 * 1024;
export const SHARED_RESULT_BYTES = 256 * 1024;

export const historyBytes = (key: string, value: string | number): number =>
  96 + key.length * 2 + (typeof value === 'string' ? value.length * 2 : 8);

export class FreshnessHistory {
  private entries = new Map<string, {value: string | number; bytes: number}>();
  private bytes = 0;
  get(key: string): string | number | undefined { return this.entries.get(key)?.value; }
  has(key: string): boolean { return this.entries.has(key); }
  usage(): Readonly<{entries: number; bytes: number}> { return {entries: this.entries.size, bytes: this.bytes}; }
  /** Returns whether information was evicted, including an unretainable new value. */
  set(key: string, value: string | number): boolean {
    const previous = this.entries.get(key);
    if (previous) { this.entries.delete(key); this.bytes -= previous.bytes; }
    const bytes = historyBytes(key, value);
    let evicted = bytes > FRESHNESS_HISTORY_BYTES;
    if (!evicted) { this.entries.set(key, {value, bytes}); this.bytes += bytes; }
    while (this.entries.size > FRESHNESS_HISTORY_ENTRIES || this.bytes > FRESHNESS_HISTORY_BYTES) {
      const [oldest, entry] = this.entries.entries().next().value!;
      this.entries.delete(oldest); this.bytes -= entry.bytes; evicted = true;
    }
    return evicted;
  }
  clear(): void { this.entries.clear(); this.bytes = 0; }
}

/** Bounded traversal: never stringify/copy a potentially oversized projection. Unknown
 * object shapes are delivered to current borrowers but are not retained as results. */
export function sharedResultBytes(value: unknown, limit = SHARED_RESULT_BYTES): number {
  let bytes = 0;
  const seen = new WeakSet<object>();
  const visit = (v: unknown, depth: number): void => {
    if (bytes > limit) return;
    if (typeof v === 'string') { bytes += 16 + v.length * 2; return; }
    if (v === null || typeof v === 'number' || typeof v === 'boolean' || v === undefined) { bytes += 16; return; }
    if (typeof v !== 'object' || depth > 64) { bytes = Infinity; return; }
    if (seen.has(v)) return;
    seen.add(v); bytes += 64;
    const prototype = Object.getPrototypeOf(v);
    if (prototype !== Object.prototype && prototype !== null && !Array.isArray(v) && !(v instanceof Error)) { bytes = Infinity; return; }
    for (const key of Reflect.ownKeys(v)) {
      bytes += 32 + String(key).length * 2;
      const property = Object.getOwnPropertyDescriptor(v, key)!;
      // V8 exposes native lazy Error.stack as an accessor. Materialize that
      // standard field; other accessors make a result unretainable.
      if (!('value' in property)) {
        if (key !== 'stack' || !(v instanceof Error)) { bytes = Infinity; return; }
        visit(v.stack, depth + 1);
      } else visit(property.value, depth + 1);
      if (bytes > limit) return;
    }
  };
  try { visit(value, 0); } catch { return Infinity; }
  return bytes;
}
