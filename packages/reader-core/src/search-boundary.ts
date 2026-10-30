/** Deterministic whole-word boundaries for reader text search.
 *
 * A word/token is a maximal run of Unicode letters, numbers, combining marks
 * and connector punctuation (the same rune classes the reading count rule uses).
 * Apostrophes join a word only when they sit between two word characters, so
 * "don't" is one token while a leading/trailing apostrophe is a boundary.
 *
 * Scripts that need dictionary word segmentation (for example Han, Thai or
 * Khmer) cannot be split by these deterministic character classes. They keep
 * the same token rule rather than pretending an ASCII or locale dictionary
 * boundary: a query inside such a run is not reported as a whole word. The
 * reader must not guess a boundary it cannot prove.
 *
 * This module owns only boundary classification. Query normalization, literal
 * contiguous matching, offsets, CFIs and result caps stay with the adapter.
 */
const word = /[\p{L}\p{N}\p{M}\p{Pc}]/u;

const isWord = (codePoint: number): boolean => word.test(String.fromCodePoint(codePoint));
const isApostrophe = (codePoint: number): boolean => codePoint === 0x27 || codePoint === 0x2019;

function codePointAt(text: string, index: number): number | undefined {
  if (index < 0 || index >= text.length) return undefined;
  return text.codePointAt(index);
}

function codePointBefore(text: string, index: number): number | undefined {
  if (index <= 0 || index > text.length) return undefined;
  const low = text.charCodeAt(index - 1);
  if (low >= 0xdc00 && low <= 0xdfff && index >= 2) {
    const high = text.charCodeAt(index - 2);
    if (high >= 0xd800 && high <= 0xdbff) return ((high - 0xd800) << 10) + (low - 0xdc00) + 0x10000;
  }
  return low;
}

/** True when the UTF-16 span [start,end) is bounded as a complete word/phrase.
 * The caller has already proved the literal text matches; this only decides the
 * outer word edges. Punctuation at an edge is itself a boundary, while a word
 * character just outside the span makes the hit part of a longer word. */
export function isWholeWordPhrase(text: string, start: number, end: number): boolean {
  if (start < 0 || end <= start || end > text.length) return false;
  const first = codePointAt(text, start);
  const last = codePointBefore(text, end);
  if (first === undefined || last === undefined) return false;

  if (isWord(first)) {
    const before = codePointBefore(text, start);
    if (before !== undefined) {
      if (isWord(before)) return false;
      if (isApostrophe(before)) {
        const beforeApostrophe = codePointBefore(text, start - 1);
        if (beforeApostrophe !== undefined && isWord(beforeApostrophe)) return false;
      }
    }
  } else if (isApostrophe(first)) {
    const before = codePointBefore(text, start);
    const after = codePointAt(text, start + 1);
    if (before !== undefined && isWord(before) && after !== undefined && isWord(after)) return false;
  }

  if (isWord(last)) {
    const after = codePointAt(text, end);
    if (after !== undefined) {
      if (isWord(after)) return false;
      if (isApostrophe(after)) {
        const afterApostrophe = codePointAt(text, end + 1);
        if (afterApostrophe !== undefined && isWord(afterApostrophe)) return false;
      }
    }
  } else if (isApostrophe(last)) {
    const after = codePointAt(text, end);
    const before = codePointBefore(text, end - 1);
    if (after !== undefined && isWord(after) && before !== undefined && isWord(before)) return false;
  }

  return true;
}
