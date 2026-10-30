import {settingsRowIndex} from '../../../../../packages/client-core/src/presentation/settings-spec';
import type {LiveDefaults} from './live-owner-model';

export const liveStabilityRow = settingsRowIndex.get('live.stability')!.row;
export const liveRewindRow = settingsRowIndex.get('live.rewind')!.row;

/** A preset describes both served values exactly; unmatched tuning remains Custom. */
export function liveStabilityValue(draft: Pick<LiveDefaults, 'streamBufferMs' | 'retryWindowMs'>): string {
  const option = liveStabilityRow.control.options?.find(value => value.sets
    && value.sets['live.buffer'] === draft.streamBufferMs / 1000
    && value.sets['live.retry'] === draft.retryWindowMs / 1000);
  return option ? String(option.value) : 'custom';
}

export function liveStabilityPatch(value: string): Pick<LiveDefaults, 'streamBufferMs' | 'retryWindowMs'> | undefined {
  const sets = liveStabilityRow.control.options?.find(option => String(option.value) === value)?.sets;
  const buffer = sets?.['live.buffer'], retry = sets?.['live.retry'];
  return typeof buffer === 'number' && typeof retry === 'number' ? {streamBufferMs: buffer * 1000, retryWindowMs: retry * 1000} : undefined;
}
