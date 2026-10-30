import {fixedQualities, type FixedQuality} from '../../../packages/client-core/src/playback-v1/choices';
import type {QualityOption} from '../../../packages/client-core/src/playback-v1/quality';

/** Automatic may choose only a real offered canonical target. Neither a label,
 * resolution suffix nor an explicitly inapplicable/original-equivalent row is a grant. */
export function automaticSteps(qualities: readonly QualityOption[]): readonly FixedQuality[] {
  return fixedQualities.filter(step => qualities.some(option => option.mode === 'fixed' && option.id === step.id
    && option.applicable !== false && option.sameAsOriginal !== true));
}
