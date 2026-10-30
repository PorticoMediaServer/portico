import type {ReadingDiscover} from './reading.ts';

/** The current profile's own explicit completion mark on one series preview item. */
export type ReadingPreviewMark = Readonly<{itemId: string; read: boolean; revision: number}>;
export type ReadingPreviewMarks = ReadonlyMap<string, ReadingPreviewMark>;

const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const count = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;

/** The optional preview mark is this source's own display fact, so it is taken out of the
 *  payload before the canonical decoder and parsed here; every other unknown field still
 *  fails the decoder's own rejection. A mark is bound to its own item id, and a malformed one
 *  is dropped rather than failing the page — the tile then simply draws no bar. */
export function withReadingDiscoverMarks(raw: unknown, decode: (value: unknown) => ReadingDiscover): Readonly<{value: ReadingDiscover; marks: ReadingPreviewMarks}> {
  const strip = (item: unknown) => {if (!record(item)) return item; const copy = {...item}; delete copy.readingMark; return copy;};
  const plain = record(raw) && Array.isArray(raw.rows) ? {...raw, rows: raw.rows.map(row => {
    if (!record(row) || !Array.isArray(row.series)) return row;
    return {...row, series: row.series.map(series => {
      if (!record(series) || !Array.isArray(series.preview)) return series;
      return {...series, preview: series.preview.map(strip)};
    })};
  })} : raw;
  const value = decode(plain);
  const marks = new Map<string, ReadingPreviewMark>();
  /** A second, different mark for the same item is ambiguity, not a later truth: drop both. */
  const conflicted = new Set<string>();
  if (record(raw) && Array.isArray(raw.rows)) for (const row of raw.rows) {
    if (!record(row) || !Array.isArray(row.series)) continue;
    for (const series of row.series) {
      if (!record(series) || !Array.isArray(series.preview)) continue;
      for (const item of series.preview) {
        if (!record(item) || typeof item.id !== 'string' || item.id.length === 0) continue;
        const mark = item.readingMark;
        if (!record(mark) || Object.keys(mark).length !== 3 || mark.itemId !== item.id
          || typeof mark.read !== 'boolean' || !count(mark.revision)) continue;
        if (conflicted.has(item.id)) continue;
        const parsed = Object.freeze({itemId: item.id, read: mark.read, revision: mark.revision});
        const prior = marks.get(item.id);
        if (prior && (prior.read !== parsed.read || prior.revision !== parsed.revision)) {
          marks.delete(item.id); conflicted.add(item.id); continue;
        }
        marks.set(item.id, parsed);
      }
    }
  }
  return {value, marks};
}
