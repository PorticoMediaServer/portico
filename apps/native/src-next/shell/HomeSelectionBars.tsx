import React, {useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../packages/i18n/src';
import type {HomeSelectionController} from '../../../../packages/client-core/src/home-selection/index';
import {selectionFourthAction, type SelectionActionReason} from '../../../../packages/client-core/src/selection-model/index';
import {SelectionBar, SelectionPhoneActionBar, type SelectionBarProps} from '../kit/selection';
import {useTheme} from '../kit/theme';
import type {NativeShellRuntime} from './runtime';
import {tvFocusIds} from './tv-focus';
import {nativeSelectionCommand, nativeSelectionOffers} from './selection-actions';
import {selectedItemsLabel} from './selection-display';
import type {NativeHomeQueryActions} from '../screens/library/selection-actions';
const noQuerySubscribe = (_listener: () => void) => () => {};
const noQuerySnapshot = () => undefined;

const t = defaultI18n.t;
const reason = (value: SelectionActionReason): string => t(value === 'unsupported-sort' ? 'next.selection.unsupportedSort'
  : value === 'preview-limit' ? 'next.selection.previewLimit'
  : value === 'mixed-types' ? 'next.selection.native.mixedTitle'
  : value === 'no-bulk-fields' ? 'next.selection.native.noBulkFields'
    : value === 'one-library-required' ? 'next.selection.native.deleteLibraries'
      : value === 'delete-limit' ? 'next.selection.native.deleteLimit' : 'error.permission');

/** Controlled native presentation for the one shared HomeSelectionController/SelectionModel. */
export function HomeSelectionBars({controller, runtime, ownerTag, reportError, queryActions}: {
  queryActions?: NativeHomeQueryActions; controller: HomeSelectionController; runtime?: NativeShellRuntime; ownerTag?: React.ReactNode; reportError: (error: unknown) => void;
}) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const queryAction = useSyncExternalStore(queryActions?.subscribe ?? noQuerySubscribe, queryActions?.getSnapshot ?? noQuerySnapshot);
  const theme = useTheme(), insets = useSafeAreaInsets();
  const [menu, setMenu] = useState<SelectionBarProps['menu']>(null);
  if (!snapshot.selection.active && !queryAction) return null;
  let actions = [...nativeSelectionOffers(snapshot.actions, reason)];
  for (const kind of ['delete', 'download'] as const) if (queryActions?.offer(kind, snapshot.selection)) {
    actions = actions.filter(offer => offer.id !== kind); actions.push({id: kind});
  }
  if (queryAction) actions = actions.map(offer => ({...offer, disabled: true}));
  const summary = queryAction?.target.summary ?? snapshot.selection.summary;
  const fourthSlot = selectionFourthAction(snapshot.selection.summary) === 'mark-watched' ? 'watched' : 'myList';
  const props: SelectionBarProps = {testID: theme.isTV && runtime ? tvFocusIds(runtime.getSnapshot().authority).surface('selection-bar') : undefined, count: summary.count, selectionLabel: selectedItemsLabel(summary), selectAllLabel: t('next.home.native.selectRow'),
    onSelectAll: () => {if (!queryAction) controller.selectRow();}, onCancel: () => {setMenu(null); if (queryAction) queryActions?.close(queryAction); else if (controller.getSnapshot() === snapshot) controller.clear();},
    onAction: action => {if (queryAction) return; if ((action === 'delete' || action === 'download') && snapshot.selection.target?.mode === 'query') {setMenu(null); queryActions?.open(action, snapshot.selection); return;} const command = nativeSelectionCommand(action); if (command) {setMenu(null); void controller.execute(command).catch(reportError);}},
    actions, menu, onMenuChange: setMenu, ownerTag,
    topInset: insets.top, fourthSlot};
  return <View pointerEvents="box-none" style={{position: 'absolute', inset: 0, zIndex: theme.layers.navigation + 1}}>
    <View style={theme.isTV ? {position: 'absolute', left: 200, right: 96, top: 58} : {position: 'absolute', left: 0, right: 0, top: 0}}><SelectionBar {...props}/></View>
    {!theme.isTV && <View style={{position: 'absolute', left: 0, right: 0, bottom: 0}}><SelectionPhoneActionBar actions={props.actions} onAction={props.onAction} onMenuChange={setMenu} fourthSlot={props.fourthSlot} bottomInset={Math.max(34, insets.bottom)}/></View>}
  </View>;
}
