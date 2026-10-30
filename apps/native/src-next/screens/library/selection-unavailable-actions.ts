import {defaultI18n} from '../../../../../packages/i18n/src';
import type {SelectionSummary} from '../../../../../packages/client-core/src/selection-model';
import type {SelectionActionOffer} from '../../kit/selection/types';

type Summary = Pick<SelectionSummary, 'count' | 'every' | 'unavailableActions'>;
export function nativeSelectionUnavailableReason(reason: 'unsupported-sort' | 'preview-limit'): string {
  return defaultI18n.t(reason === 'unsupported-sort' ? 'next.selection.unsupportedSort' : 'next.selection.previewLimit');
}

/** Match only existing offers, preserving authority and surface-specific curation.
 * Queue stays an explanation menu; its two commands cannot execute. */
export function nativeSelectionUnavailableActions(actions: readonly SelectionActionOffer[], summary: Summary | undefined): SelectionActionOffer[] {
  return actions.map(action => {
    if (action.disabled && action.reason || action.id === 'delete' && (summary?.count ?? 0) > 200) return action;
    const reason = action.id === 'play' ? summary?.unavailableActions?.play
      : ['queue', 'playNext', 'playLater'].includes(action.id) ? summary?.unavailableActions?.queue
        : action.id === 'playlist' ? summary?.unavailableActions?.playlist
          : action.id === 'delete' ? summary?.unavailableActions?.delete
            : action.id === 'edit' ? summary?.unavailableActions?.metadataPreview : undefined;
    return reason ? {...action, ...(action.id === 'queue' ? {} : {disabled: true}), reason: nativeSelectionUnavailableReason(reason)} : action;
  });
}

/** The retained callback must refuse too, before obtaining a transport. */
export function nativeSelectionActionUnavailable(summary: Summary | undefined, action: 'play' | 'playNext' | 'playLater' | 'edit' | 'playlist' | 'delete'): boolean {
  return !!(action === 'play' ? summary?.unavailableActions?.play
    : action === 'playlist' ? summary?.unavailableActions?.playlist
      : action === 'delete' ? (summary?.count ?? 0) <= 200 && summary?.unavailableActions?.delete
        : action === 'edit' ? summary?.unavailableActions?.metadataPreview : summary?.unavailableActions?.queue);
}
