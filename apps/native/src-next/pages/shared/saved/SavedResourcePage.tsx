import React, {useId, useMemo} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ResourceContentQuery} from '../../../../../../packages/client-core/src/saved';
import {AmbientArt} from '../../../kit/artwork';
import {Button, Chip, Switch} from '../../../kit/controls';
import {useDockClearance} from '../../../kit/dock/DockClearance';
import {SavedResourceFrame} from '../../../kit/frames/SavedResourceFrame';
import {Icon} from '../../../kit/Icon';
import {usePageWidth} from '../../../kit/layout/page-inset';
import {RoundBarButton} from '../../../kit/layout/TopBar';
import {Banner, BottomSheet, Menu, ToastHost} from '../../../kit/overlays';
import {SavedConfirmSheet, SavedNameSheet} from '../../../kit/saved/SavedForms';
import {SavedLoading, SavedTail} from '../../../kit/saved/SavedLoading';
import {SavedReorderList} from '../../../kit/saved/SavedReorderList';
import {SavedResourceBand, SavedResourceHero, SavedResourceRow, SavedResourceViewGrid, viewSortLabel} from '../../../kit/saved/SavedResourceParts';
import {SavedSortMenu} from '../../../kit/saved/SavedSortMenu';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {nativeDestinationFocusIds} from '../../../screens/destinations/focus';
import {savedDuration, savedLibraryName, savedViewConditionCount, savedVisibilityHelp} from '../../../screens/saved/presentation';
import {resourceSortKeys, useSavedResource, type NativeSavedResourceProps} from './useSavedResource';

const t = defaultI18n.t;

export function SavedResourcePage(props: NativeSavedResourceProps) {
  const {route, runtime, actions, selection, libraries, back, condition} = props;
  const theme = useTheme(), pageWidth = usePageWidth(), instance = useId(), dockTop = useDockClearance();
  const {active, phase, playlist, resource, personal, isPlaylist, rows, media, canPlayResource, count, countText, duration,
    artwork, name, eyebrow, query, manual, offers, chooseQuery, reorderGranted, canReorder, visibility, dragging, setDragging,
    reordering, setReordering, infoOpen, setInfoOpen, busy, problem, clearProblem, stateError, retry, next,
    play, pin, setVisibility, openEntryMenu, moveRow, placeRow, openPageMenu, retryPersonal, filtersOpen, setFiltersOpen, filtersAnchor,
    pageMenu, menuItems, setPageMenu, rowMenu, rowMenuItems, closeRowMenu, renaming, saveName, nameDraft, closeName,
    deleting, confirmDelete, closeDelete, toast, clearToast} = useSavedResource(props, theme.isTV);
  const windowWidth = theme.isTV ? 1920 : pageWidth;
  const width = Math.max(1, windowWidth - theme.gutter * 2);
  const focusIds = useMemo(() => nativeDestinationFocusIds(runtime.getSnapshot().authority), [runtime]);
  const region = `saved.resource.${theme.isTV ? `${instance}.` : ''}${route.view === 'playlist' ? route.playlistId : `${route.view}:${route.resourceId ?? ''}`}`;
  const columns = resource?.kind === 'view' ? theme.isTV ? 7 : pageWidth >= 900 ? 5 : 3 : 1;
  const sheets = <>
    <Menu open={!stateError && filtersOpen} onOpenChange={setFiltersOpen} anchorRef={filtersAnchor} presentation="sheet" title={t('next.browse.filters')} testID="saved-resource-filter-menu"
      items={offers.filters.flatMap(filter => filter.options.map(option => ({id: `${filter.id}:${option.id}`, label: option.label, trailing: defaultI18n.number(option.count),
        selected: (query.filter ?? 'all') === option.id, onSelect: () => {setFiltersOpen(false); chooseQuery({...query, filter: option.id as ResourceContentQuery['filter']});}})))}/>
    <Menu open={!stateError && pageMenu} onOpenChange={setPageMenu} presentation={theme.isTV ? 'sideSheet' : 'sheet'} title={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')}
      items={menuItems} testID="saved-resource-page-menu"/>
    <Menu open={!stateError && !!rowMenu} onOpenChange={open => {if (!open) closeRowMenu();}} presentation={theme.isTV ? 'sideSheet' : 'sheet'} title={rowMenu?.entry.title ?? ''}
      secondaryTitle={rowMenu ? <View style={{flexDirection: 'row', gap: 8}}>{[t('title.kind', {kind: rowMenu.entry.kind}), savedLibraryName(libraries, rowMenu.entry.libraryId),
        // collections.html 19's lead ends with the page the item is in.
        ...(theme.isTV && name ? [t('web.saved.inList', {name})] : [])].filter(Boolean).map(part =>
        <Text key={part} style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{part}</Text>)}</View> : undefined}
      items={rowMenuItems} initialFocusId={rowMenu?.focus} testID="saved-resource-row-menu"/>
    <SavedNameSheet open={!stateError && !!renaming} title={t(isPlaylist ? 'web.saved.renamePlaylist' : resource?.kind === 'view' ? 'web.saved.renameView' : 'web.saved.renameCollection')}
      action={t(personal?.retryPending ? 'action.tryAgain' : 'action.save')} initialName={nameDraft?.name ?? name} initialSummary={nameDraft?.summary ?? (isPlaylist ? playlist?.summary : resource?.summary)} withSummary={isPlaylist ? playlist?.actions.includes('summary') === true : true}
      withVisibility={theme.isTV && resource?.kind === 'collection' && resource.role === 'owner' && resource.actions.includes('update')} initialVisibility={nameDraft?.visibility ?? resource?.visibility}
      busy={busy} locked={personal?.retryPending} error={problem || undefined} testID="saved-rename" onClose={closeName} onSave={saveName}/>
    <BottomSheet open={infoOpen} onOpenChange={setInfoOpen} title={t('web.saved.savedView')} testID="saved-view-info" fitContent>
      <View style={{gap: 10}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{resource ? viewSortLabel(resource) : ''}</Text>
        {!!(resource?.summary) && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{resource.summary}</Text>}
        <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('next.saved.viewsHelp')}</Text>
      </View>
    </BottomSheet>
    <SavedConfirmSheet open={!stateError && !!deleting} title={t('web.saved.deleteTitle', {name: name ?? ''})} destructive
      body={t(isPlaylist ? 'web.saved.deletePlaylistBody' : resource?.kind === 'view' ? 'web.saved.deleteViewBody' : 'web.saved.deleteCollectionBody')}
      action={t(personal?.retryPending ? 'action.tryAgain' : isPlaylist ? 'web.saved.deletePlaylistAction' : resource?.kind === 'view' ? 'web.saved.deleteViewAction' : 'web.saved.deleteCollectionAction')}
      busy={busy} error={problem || undefined} testID="saved-delete" onClose={closeDelete} onConfirm={confirmDelete}/>
    {theme.isTV ? toast?.action && <View style={{position: 'absolute', left: theme.gutter, right: theme.gutter, bottom: 80}} pointerEvents="box-none">
      <Banner variant="notice" tone="info" placement="overlay" message={toast.message} action={{label: toast.action.label, onPress: toast.action.onPress}} visible/>
    </View> : <ToastHost toast={toast} onDismiss={clearToast} bottomOffset={0} obstructionTop={dockTop}/>}
  </>;
  return <SavedResourceFrame testID={`native-saved-resource-${isPlaylist ? 'playlist' : 'resource'}`}
    visible={active.visible} region={region} rail={focusIds.rail}
    back={{label: t('saved.title'), onPress: back, testID: 'saved-resource-back'}}
    barTitle={reordering ? t('next.saved.reordering') : undefined}
    barAction={reordering ? <Button testID="saved-reorder-done" variant="glass" size="small" disabled={busy || dragging}
      onPress={() => setReordering(false)}>{t('order.done')}</Button>
      : !stateError && <RoundBarButton testID="saved-resource-menu" icon="i-more"
        label={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')} onPress={openPageMenu}/>}
    backdrop={artwork[0] && <AmbientArt source={runtime.ambient(artwork[0])} treatment="library"/>}
    columns={columns} rowStride={columns > 1 ? width / columns * 1.7 : theme.isTV ? 104 : 80}
    dragging={dragging} onNearEnd={next} sheets={sheets} footer={!reordering && selection?.render()}>
        {stateError ? <StateView variant={theme.isTV ? 'centered' : 'inline'} {...runtime.error(stateError, retry)}/> : !name ? <SavedLoading kind="resource"/> : <>
          {!reordering && <>
          <SavedResourceHero artwork={artwork} runtime={runtime} visible={active.visible} name={name} eyebrow={eyebrow}
            summary={isPlaylist ? playlist?.summary : resource?.summary} condition={condition} facts={<>
              {!!countText && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{countText}</Text>}
              {!!duration && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{savedDuration(duration)}</Text>}
              {!isPlaylist && visibility && (theme.isTV || resource?.role !== 'owner') && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
                <Icon name={visibility.icon} size={theme.isTV ? 22 : 13} color={visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary}/>
                <Text style={[theme.type.caption, {color: visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary}]}>{visibility.text}</Text>
              </View>}
            </>}/>
          {resource?.kind === 'collection' && resource.role === 'owner' && resource.actions.includes('update') && !theme.isTV && <View style={{paddingHorizontal: theme.gutter, paddingTop: 16, gap: 4}}>
            <Switch focusId="saved-visibility" checked={resource.visibility === 'server'} disabled={busy || personal?.pending} onCheckedChange={setVisibility}>
              {t('web.saved.visibleToAll')}
            </Switch>
            <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{savedVisibilityHelp(resource)}</Text>
          </View>}
          <SavedResourceBand>
            <Button testID="saved-play" focusId="saved-play" variant="primary" icon="i-play" disabled={busy || !canPlayResource}
              onPress={() => play()} style={theme.isTV ? undefined : {height: 48, flex: 1}}>{t(theme.isTV && resource?.kind === 'collection' ? 'next.cards.playAll' : 'action.play')}</Button>
            {/* collections.html 18/20 draw Shuffle and Pin as labelled glass buttons, and More as
                a 72 by 14-radius square: the kit's hero glass button is that geometry already. */}
            <Button testID="saved-shuffle" focusId="saved-shuffle" variant="glass" icon="i-shuffle" iconOnly={!theme.isTV} disabled={busy || !canPlayResource || (count ?? 0) < 2} onPress={() => play(true)}
              accessibilityLabel={t('title.shuffle')} style={theme.isTV ? undefined : {width: 48, height: 48, borderRadius: 24}}>{t('title.shuffle')}</Button>
            <Button testID="saved-pin" focusId="saved-pin" variant="glass" icon={playlist?.pinned || resource?.pinned ? 'i-bookmark-f' : 'i-bookmark'} iconOnly={!theme.isTV} disabled={busy || !(isPlaylist ? playlist?.actions.includes('pin') : resource?.actions.includes('pin'))}
              onPress={pin} accessibilityLabel={t(playlist?.pinned || resource?.pinned ? 'pins.unpin' : 'pins.pin')}
              style={theme.isTV ? undefined : {width: 48, height: 48, borderRadius: 24}}>{t(playlist?.pinned || resource?.pinned ? 'pins.unpin' : 'pins.pin')}</Button>
            {theme.isTV && <Button testID="saved-resource-menu" focusId="saved-resource-menu" variant="glass" icon="i-more" iconOnly
              accessibilityLabel={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')} accessibilityState={{expanded: pageMenu}}
              onPress={openPageMenu} style={{width: 72, height: 72, borderRadius: 14}}/>}
            {reorderGranted && !theme.isTV && <Button testID="saved-reorder" focusId="saved-reorder" variant={reordering ? 'primary' : 'glass'} icon={reordering ? 'i-check' : 'i-updown'}
              disabled={!canReorder} accessibilityHint={manual ? undefined : t('order.ownOrderOnly', {group: isPlaylist ? 'playlist' : 'collection'})}
              onPress={() => setReordering(value => !value)}>{t(reordering ? 'order.done' : 'order.reorder')}</Button>}
          </SavedResourceBand>
          </>}
          {(!theme.isTV || (!isPlaylist && resource?.kind === 'view')) && <SavedResourceBand>
            {!isPlaylist && resource?.kind === 'view' ? <>
              <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{viewSortLabel(resource)}</Text>
              <Chip focusId="saved-view-info" icon="i-info" count={savedViewConditionCount(resource)} countStyle="badge" onPress={() => setInfoOpen(true)}>{t('web.saved.savedView')}</Chip>
            </> : <>
              {!!offers.sorts.length && <SavedSortMenu scope="saved-resource" focusId="saved-resource-sort" sorts={offers.sorts} sort={query.sort ?? 'position'} direction={query.direction}
                label={value => value.id === 'position' ? t('web.group.ownOrder', {group: isPlaylist ? 'playlist' : 'collection'}) : t(resourceSortKeys[value.id as keyof typeof resourceSortKeys] ?? 'web.saved.sort.other')}
                onChange={(sort, direction) => chooseQuery({sort: sort as ResourceContentQuery['sort'], direction, filter: query.filter})}/>}
              {offers.filters.some(filter => filter.options.length > 1) && <View ref={filtersAnchor} collapsable={false}>
                <Chip testID="saved-resource-filters" focusId="saved-resource-filters" icon="i-filter" selected={(query.filter ?? 'all') !== 'all'} onPress={() => setFiltersOpen(true)}>{t('next.browse.filters')}</Chip>
              </View>}
            </>}
            {!theme.isTV && theme.platform !== 'phone' && !reordering && selection?.start && <Chip testID="saved-resource-select" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip>}
            <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginLeft: 'auto'}]}>{countText}</Text>
          </SavedResourceBand>}
          {!!problem && <View style={{paddingHorizontal: theme.gutter, paddingTop: 12}}><Banner testID="saved-resource-problem" variant="notice" tone="error" placement="inline" message={problem}
            action={personal?.retryPending ? {label: t('action.tryAgain'), onPress: retryPersonal} : {label: t('action.dismiss'), onPress: clearProblem}}/></View>}
          <View style={{marginTop: theme.isTV ? 24 : 8}}>
            {!rows.length && phase !== 'loading' && <StateView variant={theme.isTV ? 'centered' : 'inline'} icon={isPlaylist ? 'i-queue' : 'i-stack'}
              title={t(isPlaylist ? 'web.saved.playlistEmpty' : resource?.kind === 'view' ? 'web.saved.viewEmpty' : 'web.saved.collectionEmpty')}
              body={isPlaylist ? t('web.saved.playlistEmptyBody') : undefined}/>}
            {reordering && !theme.isTV ? <SavedReorderList rows={rows} runtime={runtime} disabled={busy || phase === 'loading' || !canReorder}
              onMove={moveRow} onPlace={placeRow} onDragging={setDragging}/>
              : !isPlaylist && resource?.kind === 'view'
                ? <SavedResourceViewGrid entries={media} runtime={runtime} actions={actions} selection={selection} columns={columns} windowWidth={windowWidth} gutter={theme.gutter}/>
                : rows.map(row => <SavedResourceRow key={row.id} row={row} width={width} runtime={runtime} actions={actions} selection={selection?.item(row.media)} library={savedLibraryName(libraries, row.media.libraryId)}
                  onMore={anchor => openEntryMenu(row, anchor)}/>)}
            {phase === 'loading' && rows.length > 0 && <View style={{paddingHorizontal: theme.gutter, paddingVertical: 12}}><SavedTail row={resource?.kind !== 'view'} width={resource?.kind === 'view' ? width / columns : width}/></View>}
          </View>
          {/* collections.html 18's note: on a remote a row's own menu is where it moves or leaves. */}
          {theme.isTV && (isPlaylist ? playlist?.actions.includes('remove') === true && playlist.actions.includes('reorder') === true : resource?.kind === 'collection' && resource.actions.includes('entries') && resource.actions.includes('reorder')) && <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: theme.gutter, paddingTop: 28}}>
            <Icon name="i-hold" size={22} color={theme.colors.textTertiary}/>
            <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, flexShrink: 1}]}>{t('next.saved.native.holdRowActions')}</Text>
          </View>}

        </>}

  </SavedResourceFrame>;
}
