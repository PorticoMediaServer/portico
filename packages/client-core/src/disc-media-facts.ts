/** The measured disc title behind one physical asset's published timed media.
 * These facts never identify an alternate playable version or grant a mutation. */
export type DiscMediaFacts = Readonly<{
  kind: 'bluray' | 'dvd'; packaging: 'iso' | 'folder'; titleNumber: number; chapterCount: number;
}>;

/** Optional observations fail to unknown without breaking an otherwise valid
 * file/options read. Copy only closed, bounded display facts. */
export function parseDiscMediaFacts(value: unknown): DiscMediaFacts | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return;
  const {kind, packaging, titleNumber, chapterCount} = value as Record<string, unknown>;
  if ((kind !== 'bluray' && kind !== 'dvd') || (packaging !== 'iso' && packaging !== 'folder')
    || typeof titleNumber !== 'number' || !Number.isSafeInteger(titleNumber) || titleNumber < 1 || titleNumber > 4096
    || typeof chapterCount !== 'number' || !Number.isSafeInteger(chapterCount) || chapterCount < 0 || chapterCount > 65535) return;
  return Object.freeze({kind, packaging, titleNumber, chapterCount});
}
