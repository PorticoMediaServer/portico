import type {GetContainerDownloadOptionsResponse} from '../../../../../packages/contracts/src/types';
import type {DownloadQualityOption, DownloadSource} from '../../../../../packages/client-core/src/downloads';

export type ContainerDownloadOption = GetContainerDownloadOptionsResponse['options'][number];

/** Quality IDs and labels do not prove what a copy means. In particular, an
 * optimized/Automatic row may offer the original file instead of a smaller one. */
export function downloadSheetMeaning(option: Pick<DownloadQualityOption, 'available' | 'kind' | 'sameAsOriginal' | 'estimatedBytes'>,
  source: Pick<DownloadSource, 'available' | 'bytes'>): 'downloads.quality.originalHelp' | 'quality.sameAsOriginal' | 'downloads.quality.smaller' | undefined {
  if (!option.available) return undefined;
  if (option.kind === 'source') return 'downloads.quality.originalHelp';
  if (option.sameAsOriginal) return 'quality.sameAsOriginal';
  if (source.available && source.bytes > 0 && option.estimatedBytes > 0 && option.estimatedBytes < source.bytes)
    return 'downloads.quality.smaller';
  return undefined;
}

/** The combined known bytes are not a whole-request size if any member's size
 * is unknown. Keep that absence rather than presenting a partial sum as a total. */
export function containerDownloadBytes(option: Pick<ContainerDownloadOption, 'unknownSizeCount' | 'estimatedBytes'>): number | undefined {
  return option.unknownSizeCount === 0 ? option.estimatedBytes : undefined;
}
