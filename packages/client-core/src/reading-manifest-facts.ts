/** Optional descriptive facts. None of these fields confers a read or navigation grant. */
export type ReadingManifestSeries = Readonly<{id: string; name: string; libraryId: string; source: 'owner' | 'folder' | 'file-name' | 'file-metadata'; number?: number}>;
export type ReadingManifestFacts = Readonly<{wordCount?: number; estimatedSeconds?: number; pageCount?: number; openingLines?: string; series?: ReadingManifestSeries}>;
const record = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const integer = (v: unknown, min: number, max: number): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= min && v <= max;
const text = (v: unknown, max: number): v is string => typeof v === 'string' && v.length > 0 && new TextEncoder().encode(v).length <= max && !/[\x00-\x1f\x7f]/.test(v);
const keys = ['wordCount', 'estimatedSeconds', 'pageCount', 'openingLines', 'series'] as const;

/** Strip optional facts before the generated base decoder, then reconstruct only
 * known scalar/tuple facts. This remains valid when Release regenerates that decoder.
 * Existing manifest identity, source/transport validation and other capabilities stay with it.
 */
export function withReadingManifestFacts<T extends {metadata: object; format: string; pageNames?: readonly string[]}>(raw: unknown, decode: (value: unknown) => T): T & Readonly<{metadata: T['metadata'] & ReadingManifestFacts}> {
  const supplied = record(raw) && record(raw.metadata) ? raw.metadata : undefined;
  const plain = supplied ? {...supplied} : undefined;
  if (plain) for (const key of keys) delete plain[key];
  const base = decode(record(raw) && plain ? {...raw, metadata: plain} : raw);
  const facts: { -readonly [K in keyof ReadingManifestFacts]: ReadingManifestFacts[K] } = {};
  if (supplied) {
    if ((base.format === 'epub' || base.format === 'pdf') && integer(supplied.wordCount, 0, 67108864)) facts.wordCount = supplied.wordCount;
    if (facts.wordCount !== undefined && integer(supplied.estimatedSeconds, 0, 16106128) && supplied.estimatedSeconds === Math.ceil(facts.wordCount * 60 / 250)) facts.estimatedSeconds = supplied.estimatedSeconds;
    if ((base.format === 'cbz' || base.format === 'cbr') && integer(supplied.pageCount, 1, 4096) && base.pageNames?.length === supplied.pageCount) facts.pageCount = supplied.pageCount;
    else if (base.format === 'pdf' && integer(supplied.pageCount, 1, 4096)) facts.pageCount = supplied.pageCount;
    if (base.format === 'epub' && text(supplied.openingLines, 4096) && supplied.openingLines.trim()) facts.openingLines = supplied.openingLines;
    const series = supplied.series;
    if (record(series) && typeof series.id === 'string' && /^[A-Za-z0-9_-]{43}$/.test(series.id) && text(series.name, 4096) && text(series.libraryId, 256)
      && typeof series.source === 'string' && ['owner', 'folder', 'file-name', 'file-metadata'].includes(series.source)
      && (series.number === undefined || typeof series.number === 'number' && Number.isFinite(series.number) && series.number >= 0)) {
      facts.series = Object.freeze({id: series.id, name: series.name, libraryId: series.libraryId, source: series.source as ReadingManifestSeries['source'], ...(series.number === undefined ? {} : {number: series.number})});
    }
  }
  return Object.freeze({...base, metadata: Object.freeze({...base.metadata, ...facts})});
}
