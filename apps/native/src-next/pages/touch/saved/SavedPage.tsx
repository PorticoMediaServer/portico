import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {SavedListFilter} from '../../../../../../packages/client-core/src/saved';
import {BrowseFrame} from '../../../kit/frames';
import {Chip} from '../../../kit/controls';
import {Menu} from '../../../kit/overlays';
import {savedGridCardWidth, savedShelfWidth, SavedGrid, SavedGridControls, SavedLink, SavedLinkGroup, SavedMyListCard, SavedMyListRow, SavedPinnedHeading, SavedRowHeading, SavedScroll, SavedSection, SavedShelf, SavedSurface} from '../../../kit/saved/SavedParts';
import {StateView} from '../../../kit/states';
import {openNativeSavedDownload} from '../../../screens/downloads/playback';
import {SavedDownloadCard} from '../../../screens/saved/SavedDownloadCard';
import {SavedLoading, SavedTail} from '../../../screens/saved/SavedLoading';
import {useSavedPage, type SavedPageProps} from '../../shared/saved/useSavedPage';

const t = defaultI18n.t;

export function SavedPage(props: SavedPageProps) {
  const {destination, runtime, device, actions, selection, onThisPhone, downloadsCopy, downloads, tabBody} = props;
  const page = useSavedPage(props);
  const {active, tablet, snapshot, deviceState, admitted, documentFacts, transportFailure, retainedDocument, report,
    savedTab, chooseTab, tabItems, downloadsOpen, otherTab, scroll, pull, viewport, nextPosters, nextShelf,
    myListColumns, gridWidth, setGridWidth, listMode, setListMode, filterRef, sortRef, filterOpen, setFilterOpen, sortOpen, setSortOpen,
    posterProjection, posterEntries, squareEntries, summary, myListCount, filterOptions, sortItems, filterItems, applyMyList, localGroups, rows, showDownloads} = page;
  const loading = snapshot.phase === 'idle' || snapshot.phase === 'loading';
  const itemSelection = admitted ? selection?.item : undefined;
  const cardWidth = savedGridCardWidth(gridWidth, myListColumns);

  const myList = tablet ? <>
    <SavedGridControls list={listMode} onList={setListMode} count={myListCount === undefined ? undefined : t('count.titles', {count: myListCount})}
      filter={admitted ? {anchor: filterRef, open: () => setFilterOpen(true)} : undefined}
      sort={admitted ? {anchor: sortRef, open: () => setSortOpen(true)} : undefined}
      select={admitted && selection?.start ? () => selection.start?.(snapshot.myList) : undefined}/>
    {!!filterOptions.length && <SavedShelf>{filterOptions.map(option => <Chip key={`${option.field}:${option.id}`} variant="quick"
      selected={posterProjection?.query.category === option.id} count={option.count} onPress={() => applyMyList({filter: option.id as SavedListFilter})}>{option.label}</Chip>)}</SavedShelf>}
    {!posterProjection || (snapshot.phase !== 'ready' && snapshot.phase !== 'loading') ? (loading && <SavedLoading columns={myListColumns}/>)
      : !posterEntries.length && !squareEntries.length ? <StateView variant="inline" title={t('web.saved.empty.watchlist')} body={t('web.saved.empty.watchlistBody')}/>
      : listMode ? posterEntries.map(entry => <SavedMyListRow key={entry.id} entry={entry} runtime={runtime} actions={actions} selection={itemSelection?.(entry)}/>)
      : <SavedGrid columns={myListColumns} onWidth={setGridWidth}>{posterEntries.map(entry => <SavedMyListCard key={entry.id}
        entry={entry} runtime={runtime} actions={actions} selection={itemSelection?.(entry)} width={cardWidth}/>)}</SavedGrid>}
    {snapshot.myList.phase === 'loading' && !!posterEntries.length && <SavedTail width={cardWidth ?? savedShelfWidth.poster}/>}
    {!!squareEntries.length && <SavedShelf onScroll={(...geometry) => nextShelf('squares', ...geometry)}>
      {squareEntries.map(entry => <SavedMyListCard key={entry.id} entry={entry} runtime={runtime} actions={actions} width={savedShelfWidth.square} selection={itemSelection?.(entry)}/>)}
      {snapshot.myListSquares.phase === 'loading' && <SavedTail width={savedShelfWidth.square} square/>}
    </SavedShelf>}
  </> : <SavedSection first>
    <SavedRowHeading testID="saved-my-list" title={t('saved.tab.watchlist')} detail={summary ? t('next.selection.titles', {
      count: summary.myList.posters + summary.myList.squares,
    }) : undefined} onPress={() => actions.openView({view: 'watchlist'})}/>
    {loading && !posterEntries.length && <SavedLoading kind="shelf"/>}
    {!!posterEntries.length && <SavedShelf onScroll={(...geometry) => nextShelf('posters', ...geometry)}>
      {posterEntries.map(entry => <SavedMyListCard key={entry.id} entry={entry} runtime={runtime} actions={actions} selection={itemSelection?.(entry)}/>)}
      {snapshot.myList.phase === 'loading' && <SavedTail width={savedShelfWidth.poster}/>}
    </SavedShelf>}
  </SavedSection>;

  const body = downloadsOpen ? downloads?.(runtime) : otherTab ? tabBody?.(otherTab, runtime)
    : <SavedScroll node={scroll} refresh={pull}
      onHeight={height => {viewport.current.height = height; nextPosters();}}
      onExtent={height => {viewport.current.extent = height; nextPosters();}}
      onOffset={offset => {viewport.current.offset = offset; nextPosters();}}>
      {documentFacts && (!transportFailure || !retainedDocument) && <StateView variant="inline" title={t('saved.loadFailed')} body={documentFacts.body}
        primaryAction={{id: 'retry', label: t('saved.retry'), onPress: () => {void destination.load().catch(report);}}}/>}
      {myList}
      {!tablet && deviceState.supported && <SavedSection>
        <SavedRowHeading testID="saved-downloads" title={t('downloads.title')} detail={t(onThisPhone, {size: defaultI18n.bytes(deviceState.usedBytes)})} onPress={showDownloads}/>
        {!!localGroups.length && <SavedShelf>{localGroups.map(group => <SavedDownloadCard key={group.key} group={group} device={device}
          copy={downloadsCopy} onOpen={() => openNativeSavedDownload(device, group, showDownloads, report)}/>)}</SavedShelf>}
      </SavedSection>}
      {!tablet && <SavedLinkGroup>{rows.map((row, index) => <SavedLink key={row.id} testID={`saved-view-${row.id}`}
        label={t(row.label)} icon={row.icon} value={row.count === undefined ? undefined : defaultI18n.number(row.count)}
        separator={index > 0} onPress={() => actions.openView(row.view)}/>)}</SavedLinkGroup>}
      <SavedPinnedHeading onEdit={actions.editPins}/>
      {!!snapshot.pins.length && <SavedLinkGroup>{snapshot.pins.map((pin, index) => <SavedLink key={JSON.stringify([pin.kind, pin.id])}
        testID={`saved-pin-${pin.kind}-${encodeURIComponent(pin.id)}`} label={pin.name}
        caption={pin.kind === 'view' ? t('title.savedView') : t('title.kind', {kind: pin.kind})}
        icon={pin.kind === 'playlist' ? 'i-queue' : pin.kind === 'collection' ? 'i-stack' : 'i-filter'} separator={index > 0}
        onPress={() => actions.openPin(pin)}/>)}</SavedLinkGroup>}
    </SavedScroll>;

  return <SavedSurface visible={active.visible}><BrowseFrame header={{title: t('saved.title'), tabs: tablet ? {items: tabItems, value: savedTab, onValueChange: chooseTab} : undefined}} grid={<>
    {body}
    {!downloadsOpen && !otherTab && admitted && selection?.render()}
    <Menu testID="saved-my-list-filter-menu" open={admitted && filterOpen} onOpenChange={setFilterOpen} anchorRef={filterRef}
      returnFocusRef={filterRef} title={t('filter.title')} items={filterItems}/>
    <Menu testID="saved-my-list-sort-menu" open={admitted && sortOpen} onOpenChange={setSortOpen} anchorRef={sortRef}
      returnFocusRef={sortRef} title={t('next.browse.native.sort')} items={sortItems}/>
  </>}/></SavedSurface>;
}
