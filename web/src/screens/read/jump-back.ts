import type {ReaderLocation} from '@portico/reader-core';

export type ReaderJumpKind = 'jump' | 'scrub' | 'link';
export type ReaderJumpBack = Readonly<{id: number; location: ReaderLocation; kind: ReaderJumpKind}>;

/** True when two observed locations name the same position in the book. */
export function sameReaderPlace(left: ReaderLocation, right: ReaderLocation): boolean {
  if (left.format !== right.format) return false;
  return left.format === 'epub' && right.format === 'epub' ? left.cfi === right.cfi : left.format !== 'epub' && right.format !== 'epub' && left.page === right.page;
}

/** An unrequested EPUB location jump (such as an internal link click inside the iframe) crosses a section or moves at least 2%. */
export function isImplicitJump(previous: ReaderLocation, next: ReaderLocation): boolean {
  if (previous.format !== 'epub' || next.format !== 'epub' || sameReaderPlace(previous, next)) return false;
  return previous.section !== next.section || Math.abs(next.progression - previous.progression) >= 0.02;
}

/** Keeps the original pre-scrub position during continuous slider drags; otherwise records the new jump origin. */
export function nextJumpBack(current: ReaderJumpBack | null, from: ReaderLocation, kind: ReaderJumpKind, nextId: number): ReaderJumpBack {
  if (kind === 'scrub' && current?.kind === 'scrub') return current;
  return Object.freeze({id: nextId, location: from, kind});
}

/** Human-readable description of the prior reading position using only observed location facts. */
export function jumpBackPlace(location: ReaderLocation, formatPage: (page: string) => string, formatPercent: (progression: number) => string, formatNumber: (value: number) => string): string {
  if (location.format !== 'epub') return formatPage(location.pageLabel ?? formatNumber(location.page + 1));
  if (location.chapter) return location.chapter;
  if (location.pageLabel) return formatPage(location.pageLabel);
  return formatPercent(location.progression);
}
