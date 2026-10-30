import type {DownloadPreparation, DownloadTitle} from '@core/downloads.ts';
import type {SavedLocalDownload, SavedLocalDownloadGroup} from './downloads-port.ts';

export type LocalDownloadGroup = Readonly<{
  id: string;
  title?: string;
  kind: 'show' | 'movies' | 'music' | 'audiobooks' | 'server' | 'other';
  items: readonly SavedLocalDownload[];
}>;

/** The grouping reads canonical scalar metadata; it never parses joined subtitles
 * or treats the length of a fetched page as a whole-show/album count. */
export function localDownloadGroups(items: readonly SavedLocalDownload[]): readonly LocalDownloadGroup[] {
  const groups = new Map<string, {id: string; title?: string; kind: LocalDownloadGroup['kind']; items: SavedLocalDownload[]}>();
  for (const item of items) {
    const title = item.title;
    const kind: LocalDownloadGroup['kind'] = !item.onServer ? 'server' : title.kind === 'episode' && title.parentId && title.parentTitle ? 'show'
      : ['song', 'album'].includes(title.kind) ? 'music' : ['book', 'book_part', 'audiobook', 'audiobook_file', 'chapter'].includes(title.kind) ? 'audiobooks' : title.kind === 'movie' ? 'movies' : 'other';
    const id = kind === 'show' ? `show:${title.parentId}` : kind;
    let group = groups.get(id);
    if (!group) { group = {id, kind, ...(kind === 'show' ? {title: title.parentTitle} : {}), items: []}; groups.set(id, group); }
    group.items.push(item);
  }
  // Orphaned files remain available and are shown in the drawn last section.
  return [...groups.values()].sort((a, b) => Number(a.kind === 'server') - Number(b.kind === 'server'));
}

export function downloadShape(title: DownloadTitle): 'wide' | 'square' | 'poster' {
  return title.kind === 'episode' ? 'wide' : ['song', 'album', 'book', 'book_part', 'audiobook', 'audiobook_file', 'chapter'].includes(title.kind) ? 'square' : 'poster';
}

export function measuredPercent(percent: number | null | undefined): number | undefined {
  return typeof percent === 'number' && Number.isFinite(percent) && percent >= 0 && percent <= 100 ? percent : undefined;
}

/** Exact complete-native aggregate bytes, never the sum of a loaded page. */
export function localGroupPercent({bytes, bytesDone}: Pick<SavedLocalDownloadGroup, 'bytes' | 'bytesDone'>): number | undefined {
  return Number.isSafeInteger(bytes) && bytes > 0 && Number.isSafeInteger(bytesDone) && bytesDone >= 0 && bytesDone <= bytes
    ? measuredPercent(bytesDone / bytes * 100) : undefined;
}

export function preparationPercent(preparation: DownloadPreparation): number | undefined {
  return preparation.progress.bytesTotalEstimated || preparation.progress.bytesTotal <= 0 ? undefined : measuredPercent(preparation.progress.percent);
}

/** Disk measurements are OS/native observations. Missing or inconsistent facts
 * suppress the bar rather than implying a full drive, free space or a default cap. */
export function diskFractions(storage: Readonly<{totalBytes: number; freeBytes: number; usedBytes: number}>): Readonly<{portico: number; other: number}> | undefined {
  const {totalBytes, freeBytes, usedBytes} = storage;
  if (![totalBytes, freeBytes, usedBytes].every(value => Number.isSafeInteger(value) && value >= 0) || !totalBytes || freeBytes > totalBytes || usedBytes > totalBytes - freeBytes) return undefined;
  return {portico: usedBytes / totalBytes, other: (totalBytes - freeBytes - usedBytes) / totalBytes};
}
