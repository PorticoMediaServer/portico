/**
 * Reading a server document without letting one part spoil the whole. A server may be newer than
 * the app reading it, so every response parser follows the same rules:
 *
 *  - a field this app does not know is ignored;
 *  - an enumerated value it does not know is dropped (an optional field), replaced by a stated
 *    neutral value, or takes only its own row with it;
 *  - a row of a list that cannot be read is dropped and the rest of the list is kept;
 *  - sizes, depths and lengths stay bounded, and identity stays exact: ids, scope, fences,
 *    revisions, cursors and anything that carries authority still refuse the whole document.
 *
 * What was dropped is counted by document and part, so diagnostics can say what was skipped.
 * Nothing from the response itself is kept in that record.
 */
export type SkippedPart = Readonly<{document: string; part: string; count: number}>;

const MAX_PARTS = 64;
const skipped = new Map<string, {document: string; part: string; count: number}>();
const listeners = new Set<(part: SkippedPart) => void>();
let version = 0;
const watchers = new Set<() => void>();
const changed = () => { version++; for (const watcher of [...watchers]) { try { watcher(); } catch { /* a screen never breaks a read */ } } };

/** Records that `count` parts of a document were left out. `document` and `part` are names from the parser, never response text. */
export function noteSkipped(document: string, part: string, count = 1): void {
  if (count < 1) return;
  const key = document + '\n' + part;
  let entry = skipped.get(key);
  if (!entry) {
    // Bounded: once full, the oldest part makes room, so a long-lived tab never grows this record.
    if (skipped.size >= MAX_PARTS) skipped.delete(skipped.keys().next().value as string);
    entry = {document, part, count: 0};
    skipped.set(key, entry);
  }
  entry.count = Math.min(Number.MAX_SAFE_INTEGER, entry.count + count);
  const snapshot = Object.freeze({...entry});
  for (const listener of [...listeners]) { try { listener(snapshot); } catch { /* a listener never breaks a read */ } }
  changed();
}

/** Everything left out since the app started (or since the last reset), most recent part last. */
export function skippedParts(): readonly SkippedPart[] {
  return Object.freeze([...skipped.values()].map(entry => Object.freeze({...entry})));
}

/** Hears each skip as it happens (a diagnostics panel, a development log). Returns the way to stop. */
export function onSkipped(listener: (part: SkippedPart) => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

export function resetSkippedParts(): void {
  skipped.clear();
  changed();
}

/** For a screen that shows the record (`useSyncExternalStore`): told of every change, and its version. */
export function subscribeSkipped(watcher: () => void): () => void {
  watchers.add(watcher);
  return () => { watchers.delete(watcher); };
}
export function skippedVersion(): number {
  return version;
}

/** The value when it is one this app knows, otherwise undefined. Never throws. */
export function oneOf<T extends string>(value: unknown, known: readonly T[]): T | undefined {
  return typeof value === 'string' && (known as readonly string[]).includes(value) ? value as T : undefined;
}

/** The value when it is one this app knows, otherwise `neutral`, noting the replacement when a value was present. */
export function oneOfOr<T extends string>(document: string, part: string, value: unknown, known: readonly T[], neutral: T): T {
  const found = oneOf(value, known);
  if (found !== undefined) return found;
  if (value !== undefined && value !== null) noteSkipped(document, part);
  return neutral;
}

/**
 * Reads each row with `read`; a row that throws or reads as undefined is left out and counted,
 * and every other row is kept in order. The caller bounds the list's length before calling.
 */
export function keepReadable<T>(document: string, part: string, rows: readonly unknown[], read: (row: unknown, index: number) => T | undefined): T[] {
  const kept: T[] = [];
  let dropped = 0;
  rows.forEach((row, index) => {
    let value: T | undefined;
    try { value = read(row, index); } catch { value = undefined; }
    if (value === undefined) dropped++; else kept.push(value);
  });
  if (dropped) noteSkipped(document, part, dropped);
  return kept;
}

/** Keeps the first row for each key and leaves later repeats out, counting them. */
export function firstOfEach<T>(document: string, part: string, rows: readonly T[], keyOf: (row: T) => string): T[] {
  const seen = new Set<string>();
  const kept: T[] = [];
  for (const row of rows) {
    const key = keyOf(row);
    if (seen.has(key)) continue;
    seen.add(key);
    kept.push(row);
  }
  if (kept.length !== rows.length) noteSkipped(document, part, rows.length - kept.length);
  return kept;
}

/** A union document whose `variant` this app has no name for comes from a newer server. It is not
 * guessed at, and it is not called broken: reading it fails with `unsupported_version`, which the
 * error presentation turns into "update the app". A body with no variant is left to the caller. */
export function refuseUnknownVariant(body: unknown, known: readonly string[]): void {
  if (typeof body === 'object' && body !== null && !Array.isArray(body)) {
    const variant = (body as Record<string, unknown>).variant;
    if (typeof variant === 'string' && !known.includes(variant)) {
      throw Object.assign(new Error('This needs a newer version of the app.'), {code: 'unsupported_version'});
    }
  }
}
