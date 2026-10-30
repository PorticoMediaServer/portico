import React, {useEffect, useLayoutEffect, useMemo, useRef, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Button} from '../../kit/controls';
import {Menu} from '../../kit/overlays';
import {BulkEditor, SelectionBar} from '../../kit/selection';
import {bulkChangeSummary} from '../destinations/bulk-media';
import type {CardSelection} from '../../kit/cards/model';
import {useTheme} from '../../kit/theme';
import {ReadingCollectionPicker} from './ReadingCollectionPicker';
import {NativeReadingActions, type NativeReadingActionOffer} from './reading-actions';

const {t} = defaultI18n;
export function ReadingMenuAnchor({children}: {children: (anchor: React.RefObject<View | null>) => React.ReactNode}) {
  const anchor = useRef<View>(null);
  return <>{children(anchor)}</>;
}
export function useReadingActions(context: NativeShellBootstrapContext, runtime: NativeShellRuntime, owner: object) {
  const actions = useMemo(() => new NativeReadingActions(context, runtime.reportError), [context, runtime.reportError]);
  useLayoutEffect(() => {actions.arm(); return () => actions.release();}, [actions, owner]);
  // Navigation clears the opening; the retained owner can capture its route again on Back.
  useEffect(() => actions.reconcile());
  const state = useSyncExternalStore(actions.subscribe, actions.getSnapshot);
  const selection = (offer: NativeReadingActionOffer): CardSelection => ({active: state.selecting, selected: actions.selected(offer.item),
    onToggle: () => actions.toggle(offer), onEnter: () => actions.toggle(offer)});
  return {actions, state, selection};
}
export type ReadingActionsPort = ReturnType<typeof useReadingActions>;

export function ReadingItemMenu({port, runtime}: {port: ReadingActionsPort; runtime: NativeShellRuntime}) {
  const {actions, state} = port, offer = state.menu;
  return <Menu open={!!offer} onOpenChange={open => {if (!open) actions.dismiss(offer);}} title={offer?.item.title} anchorRef={offer?.anchorRef}
    items={offer ? [
      {id: 'read', label: t('next.reader.read'), icon: 'i-book', disabled: state.busy, onSelect: () => actions.open(offer)},
      ...(offer.download ? [{id: 'download', label: t('action.download'), icon: 'i-dl' as const, disabled: state.busy, onSelect: () => actions.download(offer)}] : []),
      ...(state.mark ? [{id: 'mark', label: t(state.mark.read ? 'next.reader.markUnread' : 'next.reader.markRead'), icon: 'i-check' as const,
        disabled: state.busy, closeOnSelect: false, onSelect: () => {void actions.personal(state, 'mark').catch(runtime.reportError);}}] : []),
      ...(state.listed ? [{id: 'list', label: t(state.listed.listed ? 'next.home.removeMyList' : 'next.home.addMyList'), icon: 'i-plus' as const,
        disabled: state.busy, closeOnSelect: false, onSelect: () => {void actions.personal(state, 'listed').catch(runtime.reportError);}}] : []),
      {id: 'select', label: t('next.selection.select'), disabled: state.busy, onSelect: () => actions.toggle(offer)},
    ] : []}/>;
}

/** Reading uses Read/Unread copy, not the AV selection bar's Watched actions. An owner's
 * Edit details comes first, in the Server blue (phone-read 2, desktop-read 2b). */
export function ReadingSelectionToolbar({port, runtime}: {port: ReadingActionsPort; runtime: NativeShellRuntime}) {
  const theme = useTheme(), {actions, state} = port;
  const owner = state.selecting && actions.owner(), bulk = state.edit, edit = bulk?.getSnapshot();
  if (!state.selecting) return theme.platform === 'phone' ? null : <View style={{alignItems: 'flex-end', paddingHorizontal: theme.gutter}}>
    <Button variant="text" onPress={actions.start}>{t('next.selection.select')}</Button>
  </View>;
  return <View>
    <SelectionBar count={state.selected.length} onCancel={actions.cancel} menu={null} onMenuChange={() => {}} actions={[]} onAction={() => {}}/>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 10, paddingHorizontal: theme.gutter, paddingVertical: 10}}>
      {owner ? <Button testID="reading-selection-edit" variant="glass" size="small" icon="i-edit" iconColor={theme.colors.accentStrong}
        textStyle={{color: theme.colors.accentStrong}} disabled={state.busy || !actions.canEdit(state)}
        onPress={() => actions.openEditor(state)}>{t('next.reader.editDetails')}</Button> : null}
      <Button testID="reading-selection-collection" variant="glass" size="small" icon="i-layers" disabled={state.busy || !actions.canCollect(state)}
        onPress={() => actions.openCollection(state)}>{t('next.home.collection')}</Button>
      {(['list', 'unlist', 'read', 'unread'] as const).map(action => <Button key={action} variant="glass" size="small" disabled={state.busy || !!bulk || !!state.collection || !state.selected.length}
        onPress={() => {void actions.apply(state, action).catch(runtime.reportError);}}>{t(action === 'list' ? 'next.home.addMyList' : action === 'unlist' ? 'next.home.removeMyList' : action === 'read' ? 'next.reader.markRead' : 'next.reader.markUnread')}</Button>)}
    </View>
    {state.collection ? <ReadingCollectionPicker controller={state.collection} runtime={runtime} onClose={() => actions.closeCollection(state.collection)} onView={() => actions.viewCollection(state.collection)}/> : null}
    {bulk && edit ? <BulkEditor open title={t('next.reader.editDetails')} count={state.selected.length} saveLabel={t('action.save')}
      schema={edit.preview?.schema ?? []} values={edit.values} changes={edit.changes} onChangesChange={bulk.setChanges}
      lockEdited={edit.lockEdited} onLockEditedChange={bulk.setLockEdited} onSave={() => {void bulk.save();}}
      pending={edit.phase === 'saving'} error={edit.error} result={edit.result}
      resultEntries={state.selected.map(offer => ({id: offer.item.id, title: offer.item.title}))}
      onRetry={ids => {void bulk.retry(ids);}} singleItemFields={edit.singleItemFields}
      fieldLabel={field => field.label} summary={bulkChangeSummary(edit)}
      ownerHeader={<Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('next.selection.selected', {count: state.selected.length})}</Text>}
      onOpenChange={open => {if (!open) actions.closeEditor(bulk);}} testID="reading-bulk-editor"/> : null}
  </View>;
}
