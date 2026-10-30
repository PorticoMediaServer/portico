import {defaultI18n} from '../../../../../packages/i18n/src';
import type {BrowseSelectionContext, SelectionSnapshot} from '../../../../../packages/client-core/src/selection-model';
import type {SelectionActionOffer} from '../../kit/selection/types';

/** These actions have no large-query producer. Preserve the server's capabilities
 * and show the existing unavailable state; never approximate membership locally. */
export function nativeWholeQueryUnavailableActions(state: Pick<SelectionSnapshot<BrowseSelectionContext>, 'target' | 'summary'> | undefined): readonly SelectionActionOffer[] {
  if (state?.target?.mode !== 'query' || state.summary.count <= 200) return [];
  const actions: SelectionActionOffer[] = [];
  if (state.summary.every.download) actions.push({id: 'download', disabled: true, wholeQueryUnavailable: true, reason: defaultI18n.t('error.unavailable')});
  if (state.summary.every.delete) actions.push({id: 'delete', disabled: true, wholeQueryUnavailable: true, reason: defaultI18n.t('next.selection.tooMany')});
  return actions;
}
