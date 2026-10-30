import type {BrowsePositionAnchor} from './browse.ts';

/** Bucket re-anchoring after a publication (W1, decision A).
 * For a title sort, pass the entry's `sortLetter` when it has one (`titleBucketValue`). 
 * A range with a stale revision and an `anchorId` on a non-title sort answers
 * 409 `stale_continuation` without ranking the library. The client then
 * re-navigates through the maintained bucket rails: it reads the fresh first
 * page's `positionIndex`, finds the anchor whose key matches the sort value of
 * the entry that was first visible before the reload, and jumps there with a
 * plain `range.start` (no `anchorId`). If no bucket matches, the caller keeps
 * the same index clamped to the new total.
 *
 * This is the shared pure match used by web `browse-page-source.ts` and Apple
 * `app/browse.ts` / `section-window.ts`. Pure (no React/session) so
 * node:test can exercise it. Never throws: unknown shapes return `undefined`
 * (the caller clamps), never a fabricated index. */

const ratingSorts = new Set(['communityRating', 'personalRating', 'rating', 'userRating', 'criticRating', 'audienceRating']);
const durationSorts = new Set(['duration', 'durationSeconds']);
const addedSorts = new Set(['added', 'lastPlayed', 'addedAt', 'lastPlayedAt', 'dateAdded', 'releaseDate']);
const yearSorts = new Set(['year', 'decade']);

function anchorMap(anchors: readonly BrowsePositionAnchor[]): Map<string, number> {
  const out = new Map<string, number>();
  for (const a of anchors) {
    if (!a || typeof a.key !== 'string' || typeof a.index !== 'number' || !Number.isSafeInteger(a.index) || a.index < 0) continue;
    if (!out.has(a.key)) out.set(a.key, a.index);
  }
  return out;
}

function isDecadeKey(key: string): boolean {
  return /^\d{3}0s$/.test(key);
}

function isYearKey(key: string): boolean {
  return /^\d{4}$/.test(key);
}

function isMonthKey(key: string): boolean {
  return /^\d{4}-\d{2}$/.test(key);
}

export function bucketIndexFor(
  positionIndex: readonly BrowsePositionAnchor[],
  sortField: string,
  value: unknown,
): number | undefined {
  if (!Array.isArray(positionIndex) || !positionIndex.length || typeof sortField !== 'string' || !sortField) return undefined;
  const byKey = anchorMap(positionIndex);
  if (!byKey.size) return undefined;

  if (sortField === 'title') {
    if (typeof value !== 'string' || !value) {
      return byKey.has('') ? byKey.get('') : undefined;
    }
    // The server's own letter for the entry (`sortLetter`, its sort title's: "The Wire" under W,
    // an owner's sort title honoured) is the key itself (B-07.10). A display title is only a guess.
    if (byKey.has(value)) return byKey.get(value);
    const folded=value.trim().normalize('NFD').replace(/\p{M}/gu,'').normalize('NFC').replace(/^[^\p{L}\p{N}]+/u,'');
    const first=[...folded][0]?.toUpperCase()??'#';
    const key = /\p{L}/u.test(first) ? first : '#';
    return byKey.get(key);
  }

  if (yearSorts.has(sortField)) {
    if (value === undefined || value === null || value === '' || value === 0) {
      return byKey.has('') ? byKey.get('') : undefined;
    }
    const asString = String(value);
    if (byKey.has(asString)) return byKey.get(asString);
    let year: number | undefined;
    if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 1000 && value <= 3000) {
      year = value;
    } else if (typeof value === 'string') {
      const decadeMatch = /^(\d{3}0)s$/.exec(value);
      if (decadeMatch) {
        const decadeKey = `${decadeMatch[1]}s`;
        if (byKey.has(decadeKey)) return byKey.get(decadeKey);
        const y = Number(decadeMatch[1]);
        if (Number.isSafeInteger(y)) year = y;
      } else {
        const yearMatch = /^(\d{4})/.exec(value);
        if (yearMatch) {
          const y = Number(yearMatch[1]);
          if (Number.isSafeInteger(y) && y >= 1000 && y <= 3000) year = y;
        }
      }
    }
    if (year === undefined) return undefined;
    const yearKey = String(year);
    if (byKey.has(yearKey)) return byKey.get(yearKey);
    const decadeKey = `${Math.floor(year / 10) * 10}s`;
    if (byKey.has(decadeKey)) return byKey.get(decadeKey);
    return undefined;
  }

  if (ratingSorts.has(sortField)) {
    if (value === undefined || value === null || value === '') {
      if (byKey.has('0')) return byKey.get('0');
      return byKey.has('') ? byKey.get('') : undefined;
    }
    const asString = String(value);
    if (byKey.has(asString)) return byKey.get(asString);
    const num = typeof value === 'number' ? value : Number(value);
    if (!Number.isFinite(num) || num < 0) return undefined;
    const bucket = String(Math.trunc(num));
    return byKey.get(bucket);
  }

  if (durationSorts.has(sortField)) {
    if (value === undefined || value === null || value === '') {
      if (byKey.has('0')) return byKey.get('0');
      return byKey.has('') ? byKey.get('') : undefined;
    }
    const asString = String(value);
    if (byKey.has(asString)) return byKey.get(asString);
    const num = typeof value === 'number' ? value : Number(value);
    if (!Number.isFinite(num) || num < 0) return undefined;
    const bucket = num >= 1000 ? Math.trunc(num / 1800) * 30 : Math.trunc(num / 30) * 30;
    return byKey.get(String(bucket));
  }

  if (addedSorts.has(sortField)) {
    if (value === undefined || value === null || value === '') {
      return byKey.has('') ? byKey.get('') : undefined;
    }
    const asString = String(value);
    if (byKey.has(asString)) return byKey.get(asString);
    const monthMatch = /^(\d{4}-\d{2})/.exec(asString);
    const yearMatch = /^(\d{4})/.exec(asString);
    let hasMonths = false;
    let hasYears = false;
    for (const key of byKey.keys()) {
      if (isMonthKey(key)) hasMonths = true;
      else if (isYearKey(key)) hasYears = true;
    }
    if (hasMonths && monthMatch && byKey.has(monthMatch[1])) return byKey.get(monthMatch[1]);
    if (hasYears && yearMatch && byKey.has(yearMatch[1])) return byKey.get(yearMatch[1]);
    if (monthMatch && byKey.has(monthMatch[1])) return byKey.get(monthMatch[1]);
    if (yearMatch && byKey.has(yearMatch[1])) return byKey.get(yearMatch[1]);
    return undefined;
  }

  const asString = typeof value === 'string' || typeof value === 'number' ? String(value) : undefined;
  if (asString !== undefined && byKey.has(asString)) return byKey.get(asString);
  return undefined;
}

/** What a title-sorted entry is matched to the letter rail by: the server's letter for it when the
 * page says (`sortLetter`), else its title. */
export function titleBucketValue(entry: Readonly<{title?: unknown; sortLetter?: unknown}>): unknown {
  return typeof entry.sortLetter === 'string' && entry.sortLetter ? entry.sortLetter : entry.title;
}

/** The complete title rail, with missing Latin keys disabled by their absent
 * index, followed by every additional server key in its original order. */
export function browseLetterRail(anchors:readonly BrowsePositionAnchor[]):readonly Readonly<{key:string;index?:number}>[]{
 const keys=['#',...'ABCDEFGHIJKLMNOPQRSTUVWXYZ'];const seen=new Set(keys);
 for(const anchor of anchors)if(!seen.has(anchor.key)){seen.add(anchor.key);keys.push(anchor.key);}
 const byKey=anchorMap(anchors);
 return Object.freeze(keys.map(key=>Object.freeze({key,...(byKey.has(key)?{index:byKey.get(key)!}:{})})));
}
