import type {HomeSelectionPicker} from '../../../../packages/client-core/src/home-selection/types';

/** Receipt counts already cover DISTINCT public ids across all retry rounds. Never sum job.done. */
export function nativePickerCounts(picker: Pick<HomeSelectionPicker, 'phase' | 'receipt'>) {
  const receipt = picker.receipt;
  if (!receipt) return undefined;
  return {inserted: receipt.added, alreadyIn: receipt.alreadyPresent,
    processed: receipt.added + receipt.alreadyPresent + receipt.failed,
    total: receipt.complete ? receipt.added + receipt.alreadyPresent + receipt.failed : undefined,
    complete: receipt.complete && (picker.phase === 'added' || picker.phase === 'partial')};
}
