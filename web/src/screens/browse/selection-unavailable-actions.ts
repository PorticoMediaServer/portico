import {defaultI18n} from '@i18n';
import type {SelectionSummary} from '@core/selection-model/index.ts';
import type {SelectionActions} from '../../kit/selection/types';

/** Apply only to already offered callbacks: an exclusion cannot grant authority
 * or reveal a control this surface does not otherwise support. */
export function selectionUnavailableActions(actions: SelectionActions, unavailable: SelectionSummary['unavailableActions']): SelectionActions {
  const result = {...actions};
  if (unavailable?.play && result.onPlay) {
    delete result.onPlay;
    result.playDisabledReason = defaultI18n.t('next.selection.unsupportedSort');
  }
  if (unavailable?.queue && result.onQueue) {
    delete result.onQueue;
    result.queueDisabledReason = defaultI18n.t('next.selection.unsupportedSort');
  }
  if (unavailable?.playlist && result.onPlaylist) {
    delete result.onPlaylist;
    result.playlistDisabledReason = defaultI18n.t('next.selection.unsupportedSort');
  }
  if (unavailable?.delete && result.onDelete && !result.deleteDisabledReason && !result.deleteExplanation) {
    delete result.onDelete;
    result.deleteDisabledReason = defaultI18n.t('next.selection.unsupportedSort');
  }
  if (unavailable?.metadataPreview && result.onEdit) {
    delete result.onEdit;
    result.editDisabledReason = defaultI18n.t('next.selection.previewLimit');
  }
  return result;
}
