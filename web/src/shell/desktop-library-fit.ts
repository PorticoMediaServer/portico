import {desktopLibraryLimit} from './model';

/** Fit a prefix in server order, reserving More whenever any library is outside it. */
export function desktopLibraryCount(widths: Readonly<{
  available: number; home: number; live: number | undefined; more: number; gap: number; libraries: readonly number[];
}>): number {
  let visible = 0, libraryWidth = 0;
  for (let count = 0; count <= Math.min(desktopLibraryLimit, widths.libraries.length); count++) {
    if (count > 0) libraryWidth += widths.libraries[count - 1];
    const overflow = count < widths.libraries.length;
    const items = 1 + count + Number(overflow) + Number(widths.live !== undefined);
    const required = widths.home + libraryWidth + (widths.live ?? 0) + (overflow ? widths.more : 0) + widths.gap * (items - 1);
    if (required <= widths.available) visible = count;
  }
  return visible;
}
