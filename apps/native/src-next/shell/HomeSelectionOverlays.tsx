import React, {useSyncExternalStore, type RefObject} from 'react';
import {StyleSheet, View} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import type {HomeSelectionController} from '../../../../packages/client-core/src/home-selection/index';
import type {SelectionKind} from '../../../../packages/client-core/src/selection-model/index';
import {presentError} from '../../../../packages/client-core/src/presentation/errors';
import {serverTextLabel} from '../../../../packages/client-core/src/presentation/server-text';
import {Menu, type MenuAction, type MenuEntry} from '../kit/overlays';
import {titleMenuItems} from '../kit/overlays/title-menu';
import {TouchMenuHeader} from '../kit/overlays/Menu';
import {DeleteMediaDialog, MixedEditExplainer, RatingDialog} from '../kit/selection';
import {Artwork} from '../kit/artwork';
import type {IconName} from '../kit/Icon';
import {ServerMark} from '../kit/shell';
import {useTheme} from '../kit/theme';
import {selectionHaptic} from '../platform/haptics';
import {memberTitle} from './selection-display';
import {HomeDestinationPicker} from './HomeDestinationPicker';
import {HomeBulkEditor} from './HomeBulkEditor';
import type {NativeShellRuntime} from './runtime';
import {tvFocusIds} from './tv-focus';

import type {NativeHomeQueryActions} from '../screens/library/selection-actions';
const noQuerySubscribe = (_listener: () => void) => () => {};
const t = defaultI18n.t;
const styles = StyleSheet.create({deletePhoneHeader: {paddingHorizontal: 20, paddingTop: 6, paddingBottom: 0, flexDirection: 'row', alignItems: 'center', gap: 8}});
const kindIcon = (kind: SelectionKind): IconName => kind === 'movie' ? 'i-film' : kind === 'show' || kind === 'season' || kind === 'episode' ? 'i-tvlib'
  : kind === 'song' || kind === 'album' || kind === 'artist' ? 'i-music' : kind === 'channel' ? 'i-tv' : kind === 'person' ? 'i-person' : 'i-book';

/** One controlled overlay tree, using the shared controller's genuine retained members. */
export function HomeSelectionOverlays({controller, runtime, ownerTag, reportError, anchorRef, queryActions}: {
  queryActions?: NativeHomeQueryActions; controller: HomeSelectionController; runtime?: NativeShellRuntime; ownerTag: React.ReactNode; reportError: (error: unknown) => void; anchorRef?: RefObject<View | null>;
}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme();
  const queryState = useSyncExternalStore(queryActions?.subscribe ?? noQuerySubscribe, () => queryActions?.getSnapshot()?.getSnapshot());
  const queryAction = queryActions?.getSnapshot();
  if (!runtime) return null;
  const ids = theme.isTV ? tvFocusIds(runtime.getSnapshot().authority) : undefined;
  const members = state.displayMembers, count = members.length;
  const error = (value: {code: string} | null) => value ? presentError(value, 'home', {operation: 'action'}).body : undefined;
  // The rows come from the one shared title menu (kit/overlays/title-menu); Home adds only
  // Select, and on TV the actual row's See all after the model's groups.
  const menu = state.menu;
  const select: MenuAction | undefined = menu ? {id: 'select', label: t('next.selection.select'), icon: 'i-check', onSelect: () => {
    const current = controller.getSnapshot();
    if (current.menu?.key !== menu.key || current.menu.windowId !== menu.windowId) return;
    // Pick before closing: a retained menu window may be released by closeMenu.
    if (!current.selection.selectedKeys.includes(menu.key)) {
      // The opening state must be read before the toggle: the controller's snapshot is
      // live, so reading it afterwards sees the selection it just made.
      const wasActive = current.selection.active;
      const result = controller.toggle(menu.windowId, menu.key);
      if (!result.ok) {reportError(Object.assign(new Error(result.reason), {code: result.reason})); return;}
      if (!wasActive && controller.getSnapshot().selection.active) void selectionHaptic().catch(reportError);
    }
    controller.closeMenu();
  }} : undefined;
  // tv-focus F-HOME-46..49: headings are not TV focus stops. See all is
  // the last action of the actual row member's Hold OK menu.
  const row = theme.isTV && menu ? runtime.homeMenuRow?.(menu.windowId) : undefined;
  const rowTitle = row ? serverTextLabel(defaultI18n, row.titleText, row.title) : '';
  const seeAll: MenuAction[] = row?.seeAll ? [{id: 'see-all', label: `${t('shelf.seeAll')} ${rowTitle}`, icon: 'i-grid',
    onSelect: () => {controller.closeMenu(); runtime.actions.seeAll(row.seeAll!, rowTitle);}}] : [];
  const items: MenuEntry[] = menu ? titleMenuItems(menu.groups, {tv: theme.isTV, run: action => {void controller.executeMenu(action).catch(reportError);},
    surface: select ? [select] : [], trailing: seeAll}) : [];
  const typeCounts = new Map<SelectionKind, number>();
  for (const member of members) typeCounts.set(member.item.kind, (typeCounts.get(member.item.kind) ?? 0) + 1);
  const groups = [...typeCounts].map(([kind, total]) => ({kind, count: total, label: t('next.home.native.selectionType', {kind}), icon: kindIcon(kind)}));
  const description = state.editExplanation === 'no-bulk-fields'
    ? t('next.selection.native.noBulkFields')
    : t('next.home.native.mixedSelection', {types: defaultI18n.list([...typeCounts].map(([kind, total]) => t('next.home.native.selectedType', {kind, count: total})))});
  const rating = state.rating;
  const ratingTitle = members[0] && memberTitle(members[0], runtime);
  const mutation = (value: number | null) => {controller.setRatingValue(value); void controller.submitRating().catch(reportError);};
  const menuEntry = state.menu?.member.entry;
  const menuKindKey = menuEntry?.kind === 'movie' ? 'search.kind.movie' : menuEntry?.kind === 'show' ? 'search.kind.show'
    : menuEntry?.kind === 'artist' ? 'search.kind.artist' : menuEntry?.kind === 'album' ? 'search.kind.album' : undefined;
  const menuArtwork = menuEntry?.posterUrl ? runtime.artwork(menuEntry.posterUrl) : undefined;
  return <>
    <Menu testID={theme.isTV && state.menu ? tvFocusIds(runtime.getSnapshot().authority).menu(state.menu.windowId, state.menu.key) : undefined}
      open={!!state.menu} onOpenChange={open => {if (!open) controller.closeMenu();}} title={state.menu ? memberTitle(state.menu.member, runtime) : undefined}
      items={items} anchorRef={anchorRef} touchContext={!theme.isTV} width={theme.isTV ? undefined : 300} presentation={theme.isTV ? 'sideSheet' : undefined}
      touchHeader={state.menu && !theme.isTV ? <TouchMenuHeader title={memberTitle(state.menu.member, runtime)} artwork={menuArtwork ? <Artwork source={menuArtwork} width={40} height={60}/> : undefined}
        kind={menuKindKey ? t(menuKindKey) : undefined} year={menuEntry?.year === undefined ? undefined : defaultI18n.number(menuEntry.year, {useGrouping: false})}/> : undefined}/>
    <HomeDestinationPicker controller={controller} runtime={runtime} reportError={reportError}/>
    <HomeBulkEditor controller={controller} runtime={runtime} reportError={reportError}/>
    <MixedEditExplainer testID={ids?.surface('mixed-edit')} open={state.mixedEdit} onOpenChange={open => {if (!open) controller.closeEditor();}} description={description} total={count} groups={groups}
      onKeepOnly={kind => {controller.keepOnly(kind as SelectionKind);}}/>
    {queryAction && queryState?.input?.action === 'delete' && <DeleteMediaDialog queryAction={queryAction} ownerHeader={ownerTag} onClose={() => queryActions?.close(queryAction)}/>}
    {!queryAction && state.deletion && <DeleteMediaDialog testID={ids?.surface('delete')} open onOpenChange={open => {if (!open) controller.closeDelete();}}
      ownerHeader={theme.layoutMode === 'phone' ? <View style={styles.deletePhoneHeader}><ServerMark variant="editorPhone"/>{ownerTag}</View> : ownerTag}
      owner={runtime.getSnapshot().account.viewerRole === 'owner'} selectionCount={count} preview={state.deletion.preview} loading={state.deletion.phase === 'loading'} error={error(state.deletion.error)}
      result={state.deletion.result ?? undefined} confirmation={state.deletion.confirmation} onConfirmationChange={value => controller.setDeleteConfirmation(value)}
      removeFiles={state.deletion.removeFiles} onRemoveFilesChange={value => controller.setDeleteRemoveFiles(value)} pending={state.deletion.phase === 'deleting'}
      onDelete={() => {void controller.confirmDelete().catch(reportError);}} renderArtwork={id => {
        const member = members.find(value => 'id' in value.item && value.item.id === id), source = runtime.artwork(member?.entry?.posterUrl);
        return source ? <Artwork source={source} width={24} height={36}/> : null;
      }}/>} 
    {rating && ratingTitle && <RatingDialog testID={ids?.surface('rating')} title={ratingTitle} value={rating.value} phase={rating.phase} error={error(rating.error)}
      onRate={mutation} onRetry={() => {void controller.retryRating().catch(reportError);}} onClose={() => controller.closeRating()}/>}
  </>;
}
