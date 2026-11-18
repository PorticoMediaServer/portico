import React, {useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../packages/i18n/src';
import type {HomeSelectionController} from '../../../../packages/client-core/src/home-selection/index';
import {selectionFourthAction, type SelectionActionReason, type SelectionActionRule} from '../../../../packages/client-core/src/selection-model/index';
import {SelectionBar, SelectionPhoneActionBar, type SelectionBarProps} from '../kit/selection';
import {useTheme} from '../kit/theme';
import type {NativeShellRuntime} from './runtime';
import {tvFocusIds} from './tv-focus';
import {nativeSelectionCommand, nativeSelectionOffers} from './selection-actions';
import {selectedItemsLabel} from './selection-display';
import {queryDeleteLimit, queryDeleteSelector} from '../../../../packages/client-core/src/query/catalogue-query-delete';

const t = defaultI18n.t;
const reason = (value: SelectionActionReason): string => t(value === 'unsupported-sort' ? 'next.selection.unsupportedSort'
  : value === 'preview-limit' ? 'next.selection.previewLimit'
  : value === 'mixed-types' ? 'next.selection.native.mixedTitle'
  : value === 'no-bulk-fields' ? 'next.selection.native.noBulkFields'
    : value === 'one-library-required' ? 'next.selection.native.deleteLibraries'
      : value === 'delete-limit' ? 'next.selection.native.deleteLimit' : 'error.permission');

/** Controlled native presentation for the one shared HomeSelectionController/SelectionModel. */
export function HomeSelectionBars({controller, runtime, ownerTag, reportError}: {
  controller: HomeSelectionController; runtime?: NativeShellRuntime; ownerTag?: React.ReactNode; reportError: (error: unknown) => void;
}) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const theme = useTheme(), insets = useSafeAreaInsets();
  const [menu, setMenu] = useState<SelectionBarProps['menu']>(null);
  if (!snapshot.selection.active) return null;
  const {target, summary} = snapshot.selection;
  const query = target?.mode === 'query' ? target : undefined;
  const queryDelete = !!query && runtime?.getSnapshot().account.viewerRole === 'owner' && !!queryDeleteSelector(query);
  const queryDeleteOpen = !!query && !!snapshot.deletion;
  const deleteRule: SelectionActionRule = !queryDelete ? {state: 'hidden'}
    : query.total > queryDeleteLimit ? {state: 'explain', reason: 'delete-limit'}
    : summary.unavailableActions?.delete ? {state: 'disabled', reason: summary.unavailableActions.delete} : {state: 'ready'};
  const offers = nativeSelectionOffers(query ? {...snapshot.actions, download: {state: 'hidden'}, delete: deleteRule} : snapshot.actions, reason);
  const actions = queryDeleteOpen ? offers.map(offer => ({...offer, disabled: true})) : offers;
  const fourthSlot = selectionFourthAction(snapshot.selection.summary) === 'mark-watched' ? 'watched' : 'myList';
  const props: SelectionBarProps = {testID: theme.isTV && runtime ? tvFocusIds(runtime.getSnapshot().authority).surface('selection-bar') : undefined, count: summary.count, selectionLabel: selectedItemsLabel(summary), selectAllLabel: t('next.home.native.selectRow'),
    onSelectAll: () => {if (!queryDeleteOpen) controller.selectRow();},
    onCancel: () => {setMenu(null); if (controller.getSnapshot() === snapshot) {if (queryDeleteOpen) controller.closeDelete(); else controller.clear();}},
    onAction: action => {
      if (query) {
        if (action === 'download' || queryDeleteOpen) return;
        if (action === 'delete') {
          if (!queryDelete || deleteRule.state === 'disabled' || controller.getSnapshot() !== snapshot || snapshot.editor || snapshot.picker || snapshot.rating
            || snapshot.mixedEdit || snapshot.action.phase !== 'idle') return;
          setMenu(null); void controller.openDelete().catch(reportError); return;
        }
      }
      const command = nativeSelectionCommand(action);
      if (command) {setMenu(null); void controller.execute(command).catch(reportError);}
    },
    actions, menu, onMenuChange: setMenu, ownerTag,
    topInset: insets.top, fourthSlot};
  return <View pointerEvents="box-none" style={{position: 'absolute', inset: 0, zIndex: theme.layers.navigation + 1}}>
    <View style={theme.isTV ? {position: 'absolute', left: 200, right: 96, top: 58} : {position: 'absolute', left: 0, right: 0, top: 0}}><SelectionBar {...props}/></View>
    {!theme.isTV && <View style={{position: 'absolute', left: 0, right: 0, bottom: 0}}><SelectionPhoneActionBar actions={props.actions} onAction={props.onAction} onMenuChange={setMenu} fourthSlot={props.fourthSlot} bottomInset={Math.max(34, insets.bottom)}/></View>}
  </View>;
}
