import type {DownloadChoice} from '../../../../../packages/contracts/src/next/api';

/** What a choice means for the person: the original file as it is, or a smaller copy the server makes first. */
export function downloadSheetMeaning(option: Pick<DownloadChoice, 'available' | 'quality' | 'prepared'>): 'downloads.quality.originalHelp' | 'downloads.quality.smaller' | undefined {
  if (!option.available) return undefined;
  if (option.quality === 'original') return 'downloads.quality.originalHelp';
  return option.prepared ? 'downloads.quality.smaller' : undefined;
}
