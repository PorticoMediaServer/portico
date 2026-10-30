import {ReaderError, throwIfAborted} from '../errors.ts';
import type {ReaderIndexedSearchHit} from '../ports.ts';

/** Bounded page/work limits shared by the adapter and its pure vectors. */
export const indexedPageLimit = 50;
export const indexedOverallLimit = 500;
export const indexedTextLimit = 8 << 20;
export const indexedNodeLimit = 200000;
export const indexedDepthLimit = 256;
export const indexedTimeLimit = 15000;
/** One section's synchronous DOM walk + normalization budget (ms). */
export const indexedSectionBudget = 40;
/** The exact textBlock list from server text_count.go; case-sensitive. */
export const indexedTextBlocks: readonly string[] = Object.freeze(['address', 'article', 'aside', 'blockquote', 'br', 'dd', 'div', 'dl', 'dt', 'figcaption', 'figure', 'footer', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'header', 'hr', 'li', 'main', 'nav', 'ol', 'p', 'pre', 'section', 'table', 'td', 'th', 'tr', 'ul']);
const indexedBlockSet = new Set(indexedTextBlocks);
const xhtml = 'http://www.w3.org/1999/xhtml';

/** Go unicode.IsSpace (Unicode White_Space), not JavaScript \s: U+FEFF is not
 * space and U+0085 is. The server normalization uses this exact set. */
export function goSpace(code: number): boolean {
  return code === 0x09 || code === 0x0a || code === 0x0b || code === 0x0c || code === 0x0d || code === 0x20
    || code === 0x85 || code === 0xa0 || code === 0x1680 || (code >= 0x2000 && code <= 0x200a)
    || code === 0x2028 || code === 0x2029 || code === 0x202f || code === 0x205f || code === 0x3000;
}
export function searchWellFormed(value: string): boolean {
  for (let index = 0; index < value.length; index++) {
    const unit = value.charCodeAt(index);
    if (unit >= 0xd800 && unit <= 0xdbff) {
      if (++index >= value.length) return false;
      const low = value.charCodeAt(index);
      if (!(low >= 0xdc00 && low <= 0xdfff)) return false;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) return false;
  }
  return true;
}
export const searchSplit = (value: string, index: number) => index > 0 && index < value.length
  && (value.charCodeAt(index - 1) & 0xfc00) === 0xd800 && (value.charCodeAt(index) & 0xfc00) === 0xdc00;

export type IndexedSegment = Readonly<{start: number; end: number; node: Text | null}>;
export type IndexedRaw = Readonly<{raw: string; segments: readonly IndexedSegment[]}>;
export type IndexedNormalized = Readonly<{text: string; startRaw: Map<number, number>; endRaw: Map<number, number>}>;

/** Server-equivalent section text: XHTML html/body only, exact-case
 * script/style/template skipped, one ASCII space around each textBlock element,
 * text and CDATA concatenated in document order. A foreign namespace, ambiguous
 * body/root, doctype or any budget overrun declines the mapping. */
export function indexedRawText(doc: Document, signal: AbortSignal, deadline: number): IndexedRaw | null {
  const root = doc.documentElement, body = doc.body;
  if (!root || root.localName !== 'html' || root.namespaceURI !== xhtml || !body) return null;
  if (body.localName !== 'body' || body.namespaceURI !== xhtml || body.parentElement !== root
    || doc.getElementsByTagNameNS(xhtml, 'body').length !== 1 || doc.doctype) return null;
  let raw = '', nodes = 0;
  const segments: IndexedSegment[] = [];
  const append = (value: string, node: Text | null) => {
    if (!value) return;
    const start = raw.length;
    raw += value;
    if (raw.length > indexedTextLimit) throw new ReaderError('limit_exceeded');
    segments.push({start, end: raw.length, node});
  };
  const walk = (node: Node, depth: number): true | null => {
    if (depth > indexedDepthLimit || Date.now() > deadline) throw new ReaderError('limit_exceeded');
    // Iterate the live child list incrementally: never materialise an
    // arbitrarily large sibling list before the budget can stop us.
    const children = node.childNodes;
    for (let index = 0; index < children.length; index++) {
      throwIfAborted(signal);
      if (++nodes > indexedNodeLimit) throw new ReaderError('limit_exceeded');
      if ((nodes & 1023) === 0 && Date.now() > deadline) throw new ReaderError('limit_exceeded');
      const child = children[index]!;
      if (child.nodeType === Node.TEXT_NODE || child.nodeType === Node.CDATA_SECTION_NODE) {
        append((child as Text).data, child as Text);
        continue;
      }
      if (child.nodeType !== Node.ELEMENT_NODE) continue;
      const element = child as Element;
      if (element.namespaceURI !== xhtml) return null;
      const tag = element.localName; // exact case, matching the server walker
      if (tag === 'script' || tag === 'style' || tag === 'template') continue;
      const block = indexedBlockSet.has(tag);
      if (block) append(' ', null);
      const result = walk(child, depth + 1);
      if (result === null) return null;
      if (block) append(' ', null);
    }
    return true;
  };
  const held = walk(body, 2);
  return held === null ? null : {raw, segments};
}

/** Normalize raw text with the server's Go-space collapse/trim. Start and end
 * targets are collected independently; a missing target affects only that hit,
 * never its siblings. The work counter is independent of UTF-16 offsets. */
export function indexedNormalize(raw: string, starts: readonly number[], ends: readonly number[], signal: AbortSignal, deadline: number): IndexedNormalized | null {
  const startTargets = new Set(starts), endTargets = new Set(ends);
  const startRaw = new Map<number, number>(), endRaw = new Map<number, number>();
  let text = '', pending = false, index = 0, work = 0;
  while (index < raw.length) {
    if ((work++ & 4095) === 0) {
      throwIfAborted(signal);
      if (Date.now() > deadline) return null;
    }
    const code = raw.codePointAt(index)!;
    const length = code > 0xffff ? 2 : 1;
    if (goSpace(code)) { pending = true; index += length; continue; }
    if (pending) {
      if (text.length > 0) {
        text += ' ';
        if (endTargets.has(text.length)) endRaw.set(text.length, index);
      }
      pending = false;
    }
    if (startTargets.has(text.length) && !startRaw.has(text.length)) startRaw.set(text.length, index);
    text += String.fromCodePoint(code);
    index += length;
    if (text.length > indexedTextLimit) return null;
    if (endTargets.has(text.length)) endRaw.set(text.length, index);
  }
  return {text, startRaw, endRaw};
}

/** Binary-search the raw segment containing index; a text/CDATA segment yields
 * a real DOM point, a synthetic boundary space does not. */
export function indexedLocate(segments: readonly IndexedSegment[], index: number, preferNext: boolean): {node: Text; offset: number} | null {
  if (index < 0) return null;
  let low = 0, high = segments.length - 1, found = -1;
  while (low <= high) {
    const middle = (low + high) >> 1, segment = segments[middle]!;
    if (index < segment.start) high = middle - 1;
    else if (index > segment.end) low = middle + 1;
    else { found = middle; break; }
  }
  if (found < 0) {
    const last = segments[segments.length - 1];
    if (!last || index !== last.end) return null;
    found = segments.length - 1;
  }
  const segment = segments[found]!;
  if (segment.node === null) {
    if (index === segment.end) {
      const next = segments[found + 1];
      if (next?.node && next.start === index) return {node: next.node, offset: 0};
    }
    return null;
  }
  if (preferNext && index === segment.end) {
    const next = segments[found + 1];
    if (next?.node && next.start === index) return {node: next.node, offset: 0};
  }
  const offset = index - segment.start;
  if (offset < 0 || offset > segment.node.data.length) return null;
  return {node: segment.node, offset};
}

/** Build the real DOM Range only when this hit's own quote, offsets, surrogate
 * boundaries and both endpoints all resolve; otherwise the hit stays display-only. */
export function indexedRange(doc: Document, raw: IndexedRaw, normalized: IndexedNormalized, hit: ReaderIndexedSearchHit): Range | null {
  const quote = hit.excerpt.slice(hit.matchStart, hit.matchStart + hit.matchLength);
  if (!quote || quote.length !== hit.matchLength || !searchWellFormed(quote) || !searchWellFormed(hit.excerpt)) return null;
  if (hit.utf16Offset + hit.matchLength > normalized.text.length || normalized.text.slice(hit.utf16Offset, hit.utf16Offset + hit.matchLength) !== quote) return null;
  if (searchSplit(normalized.text, hit.utf16Offset) || searchSplit(normalized.text, hit.utf16Offset + hit.matchLength)) return null;
  const startRaw = normalized.startRaw.get(hit.utf16Offset), endRaw = normalized.endRaw.get(hit.utf16Offset + hit.matchLength);
  if (startRaw === undefined || endRaw === undefined || startRaw >= endRaw) return null;
  if (searchSplit(raw.raw, startRaw) || searchSplit(raw.raw, endRaw)) return null;
  const start = indexedLocate(raw.segments, startRaw, false), end = indexedLocate(raw.segments, endRaw, true);
  if (!start || !end) return null;
  const range = doc.createRange();
  range.setStart(start.node, start.offset);
  range.setEnd(end.node, end.offset);
  return range.collapsed ? null : range;
}
