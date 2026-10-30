import {ReaderError} from './errors.ts';
import type {ReaderFit, ReaderViewport} from './ports.ts';
export function validateViewport(viewport: ReaderViewport): void {
  if (!Number.isFinite(viewport.width) || !Number.isFinite(viewport.height) || viewport.width <= 0 || viewport.height <= 0)
    throw new ReaderError('invalid_preferences');
}
export type ValidReaderCrease = Readonly<{left: number; right: number}>;
/** The drawn72 pt gutter is the only band the reader accepts: the reported crease must
 * sit entirely inside it, and a compact window keeps the phone's one-page rule. */
export function validReaderCrease(viewport: ReaderViewport): ValidReaderCrease | undefined {
  const crease = viewport.crease, middle = viewport.width / 2;
  if (!crease || viewport.width < 600 || ![crease.left, crease.right].every(Number.isFinite) || crease.left <= 0 || crease.right < crease.left
    || crease.right > viewport.width || crease.left < middle - 36 || crease.right > middle + 36) return undefined;
  return Object.freeze({left: crease.left, right: crease.right});
}
/** A reported book fold makes a spread readable below the flat-landscape width rule. */
export function creaseSpread(viewport: ReaderViewport): boolean {return !!validReaderCrease(viewport);}
export type ReaderPane = Readonly<{x: number; width: number; height: number}>;
export type FixedVisualPane = Readonly<{page: number; pane: ReaderPane}>;
/** Physical panes around a valid crease, zipped with the planner's already
 * direction-ordered visual pages. A lone cover is recto, a lone last/only page verso. */
export function fixedVisualPanes(viewport: ReaderViewport, visualPages: readonly number[], direction: 'ltr'|'rtl', single?: 'cover'|'last'|'only'): readonly FixedVisualPane[] {
  const crease = validReaderCrease(viewport);
  if (!crease || visualPages.length < 1 || visualPages.length > 2) return [];
  const left = {x: 0, width: crease.left, height: viewport.height};
  const right = {x: crease.right, width: viewport.width - crease.right, height: viewport.height};
  if (visualPages.length === 2) return Object.freeze([{page: visualPages[0]!, pane: left}, {page: visualPages[1]!, pane: right}]);
  const recto = direction === 'rtl' ? left : right, verso = direction === 'rtl' ? right : left;
  return Object.freeze([{page: visualPages[0]!, pane: single === 'cover' ? recto : verso}]);
}
export function spreadFits(viewport: ReaderViewport): boolean {
  validateViewport(viewport);
  return viewport.width >= 1024 && viewport.width > viewport.height;
}
/** phone-read.html group10: the short dimension is compact after rotation. */
export function compactLandscape(viewport: ReaderViewport): boolean {
  validateViewport(viewport);
  return viewport.width < 1024 && viewport.height < 600 && viewport.width > viewport.height;
}
export type FixedLayoutInput = Readonly<{
  format: 'pdf' | 'cbz' | 'cbr'; viewport: ReaderViewport; page: number; pageCount: number;
  twoPages: boolean; firstPageAlone: boolean; direction: 'ltr' | 'rtl' | 'vertical'; fit: ReaderFit | 'auto';
}>;
/** Logical order never reverses: RTL changes presentation and input direction,
 * so Next still means advancing through the publication's own reading order. */
export function planFixedLayout(input: FixedLayoutInput) {
  const {viewport, page, pageCount, direction, firstPageAlone} = input;
  validateViewport(viewport);
  if (!Number.isSafeInteger(pageCount) || pageCount < 1 || !Number.isSafeInteger(page) || page < 0 || page >= pageCount)
    throw new ReaderError('invalid_location');
  const spread = input.twoPages && direction !== 'vertical' && (spreadFits(viewport) || creaseSpread(viewport) || compactLandscape(viewport) && (input.format === 'pdf' || direction === 'rtl'));
  const start = !spread ? page : firstPageAlone ? page === 0 ? 0 : 1 + 2 * Math.floor((page - 1) / 2) : 2 * Math.floor(page / 2);
  const count = spread && !(firstPageAlone && start === 0) ? Math.min(2, pageCount - start) : 1;
  const pages = Object.freeze(Array.from({length: count}, (_, i) => start + i));
  const fit = input.fit === 'auto' ? viewport.width < 1024 && !spread ? 'width' : 'height' : input.fit;
  return Object.freeze({pages, visualPages: Object.freeze(direction === 'rtl' ? [...pages].reverse() : [...pages]),
    spread, fit, direction, nextPage: start + count < pageCount ? start + count : null,
    previousPage: start === 0 ? null : spread ? Math.max(0, start - (firstPageAlone && start === 1 ? 1 : 2)) : start - 1});
}
