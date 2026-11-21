import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {SavedListFilter} from '../../../../../../packages/client-core/src/saved';
import {BrowseFrame} from '../../../kit/frames';
import {Button, Chip} from '../../../kit/controls';
import {PosterCard, SquareCard} from '../../../kit/cards';
import {Menu} from '../../../kit/overlays';
import {StateView} from '../../../kit/states';
import {AlphabetRail} from '../../../kit/layout';
import {savedCardWidth, TVSavedCardCell, TVSavedDirectoryPane, TVSavedEmpty, TVSavedGrid, TVSavedHeading, TVSavedHistoryDay,
  TVSavedHistoryRow, TVSavedResults, TVSavedScroll, TVSavedSection, TVSavedToolbar, TVSavedToolbarDivider} from '../../../kit/saved/TVSavedParts';
import {SavedConfirmSheet} from '../../../screens/saved/SavedForms';
import {SavedTVLoading} from '../../../screens/saved/SavedTVLoading';
import {SavedTVDirectory} from '../../../screens/saved/SavedTVDirectory';
import {dayLabel, factLine, filters, historyFocusable, historyLine, itemID, periods, periodLabel, sorts, useTVSavedPage, type TVSavedPageProps} from '../../shared/saved/useTVSavedPage';

const t = defaultI18n.t;

export function TVSavedPage(props: TVSavedPageProps) {
  const {controller, runtime, actions, activation, openSaved, openEntry, browseHome, selection} = props;
  const {snapshot, ids, active, tab, sort, setSort, filter, setFilter, period, setPeriod, sortOpen, setSortOpen, filterOpen, setFilterOpen,
    sortAnchor, filterAnchor, tabs, posterRegion, squareRegion, historyRegion, posters, squares, posterCount, squareCount, gridFocus,
    page, contentRoot, directoryPage, scrollY, setScrollY, viewportHeight, setViewportHeight, contentHeight, setContentHeight, count,
    measurement, scope, posterGeometry, squareGeometry, firstSection, directoryView, historyHeight, publishedFilters, quickFilters, emptyEntry,
    alphabet, run, openClear, clearView, closeClear, confirmClear, clearing, sortLabel} = useTVSavedPage(props);
  const measure = (section: 'posters' | 'squares', entry: ContentEntry, child: React.ReactNode, top?: number) => <TVSavedCardCell key={`${scope}:${entry.id}`} onTop={y => {measurement.cards.set(entry.id, {section, y: top ?? y});}}>{child}</TVSavedCardCell>;
  const card = (entry: ContentEntry, onFocus?: () => void, top?: number) => measure('posters', entry, <PosterCard disabled={!active.visible} onFocus={onFocus} testID={`saved-poster-${encodeURIComponent(entry.id)}`} width={savedCardWidth} title={entry.displayTitle ?? entry.title} caption={factLine(historyLine(entry))} artwork={runtime.artwork(entry.posterUrl)}
    onNavigate={() => openEntry(entry)} onMore={() => actions.menu(entry, undefined, select?.(entry))} moreExpanded={actions.isMenuOpen(entry)} selection={selection?.item(entry)}/>, top);
  // tv-more3: on a saved album or audiobook, OK plays it; hold OK still opens its menu. A square
  // that has nothing to play keeps opening its own page.
  const square = (entry: ContentEntry, onFocus?: () => void, top?: number) => measure('squares', entry, <SquareCard disabled={!active.visible} onFocus={onFocus} testID={`saved-square-${encodeURIComponent(entry.id)}`} width={savedCardWidth} title={entry.displayTitle ?? entry.title} caption={factLine(entry.artist?.name ?? entry.author ?? '')} artwork={runtime.artwork(entry.posterUrl)}
    {...(entry.playback ? {primaryAction: 'play' as const, onPlay: () => actions.play(entry)} : {onNavigate: () => openEntry(entry)})}
    onMore={() => actions.menu(entry, undefined, select?.(entry))} moreExpanded={actions.isMenuOpen(entry)} selection={selection?.item(entry)}/>, top);
  /** The menu's Select starts this screen's own canonical Saved selection on that title. */
  const select = selection ? (entry: ContentEntry) => () => {void selection.item(entry)?.onEnter();} : undefined;
  const gridProps = {scrollY, viewportHeight, coordinateRoot: contentRoot, layoutRevision: contentHeight};
  const body = snapshot.phase === 'error' ? <StateView testID="saved-error" variant="centered"
    {...runtime.error(snapshot.error, () => run(controller.select(tab, {sort, filter, period})))}/>
    : snapshot.phase !== 'ready' ? <SavedTVLoading key={`${scope}:${period}`} shape={tab === 'history' ? 'history' : 'posters'}/>
    : tab === 'history' ? (snapshot.entries.length ? <TVSavedGrid shape="history" {...gridProps}
      key={`${scope}:history:${period}`} items={snapshot.entries} getKey={itemID} getRowHeight={historyHeight}
      focus={{...gridFocus(historyRegion, entry => entry.id), getFocusId: row => `saved-history-${encodeURIComponent(row.id)}`, isFocusable: historyFocusable}}
      renderItem={(row, cell) => {
        const index = cell.index, day = dayLabel(row.updatedAt, runtime.text.locale);
        const previous = index ? dayLabel(snapshot.entries[index - 1]?.updatedAt, runtime.text.locale) : undefined;
        return <>
          {day && day !== previous && <TVSavedHistoryDay day={day} first={index === 0}/>}
          {row.media && <TVSavedHistoryRow disabled={!active.visible} entry={row.media} updatedAt={row.updatedAt} positionSeconds={row.positionSeconds}
            completed={row.completed} runtime={runtime} actions={actions} openEntry={openEntry} onFocus={cell.onFocus}/>}
        </>;
      }}/> : <TVSavedEmpty tab="history" browseHome={browseHome}/>)
    : tab === 'watchlist' || tab === 'favorites' ? (posters.length || squares.length ? <TVSavedSection onTop={top => {measurement.section = top;}}>
      {!!posters.length && <>
        {!!squareCount && <TVSavedHeading title={t('saved.postersHeading')} count={posterCount}/>}
        <TVSavedGrid shape="posters" {...gridProps} focus={gridFocus(posterRegion, entry => `saved-poster-${encodeURIComponent(entry.id)}`)}
          key={`${scope}:posters`} items={posters} getKey={itemID} onLayout={event => {measurement.grids.set('posters', event.nativeEvent.layout.y);}}
          onGeometry={posterGeometry} renderItem={(entry, cell) => card(entry, cell.onFocus, cell.top)}/>
      </>}
      {!!squares.length && <>
        {!!posterCount && <TVSavedHeading title={t('saved.squaresHeading')} count={squareCount}/>}
        <TVSavedGrid shape="squares" {...gridProps} focus={gridFocus(squareRegion, entry => `saved-square-${encodeURIComponent(entry.id)}`)}
          key={`${scope}:squares`} items={squares} getKey={itemID} onLayout={event => {measurement.grids.set('squares', event.nativeEvent.layout.y);}}
          onGeometry={squareGeometry} renderItem={(entry, cell) => square(entry, cell.onFocus, cell.top)}/>
      </>}
    </TVSavedSection> : <TVSavedEmpty tab={tab} browseHome={browseHome}/>) : null;
  return <BrowseFrame testID="native-saved-tv-frame" header={{title: t('saved.title'), tabs}}
    alphabet={alphabet && <AlphabetRail {...alphabet} testID="saved-alphabet"/>} grid={<>
    <TVSavedScroll node={page} root={contentRoot} onExtent={setContentHeight} onHeight={setViewportHeight} onOffset={setScrollY}>
      {directoryView ? <TVSavedDirectoryPane>
        <SavedTVDirectory view={directoryView} destination={controller.source(directoryView)} runtime={runtime} libraries={runtime.getSnapshot().libraries}
          pageRef={directoryPage} enabled={active.visible} activation={activation} regionId={ids.savedGrid} edges={{up: ids.savedTabs, left: ids.rail}} select={route => {if (route.view === 'playlists') run(controller.select('playlists', {sort: route.sort, direction: route.direction, filter: route.filter}));}}
          openPlaylist={card => openSaved({view: 'playlist', playlistId: card.id})} openResource={value => openSaved({view: 'resource', resourceId: value.id})}/>
      </TVSavedDirectoryPane> : <TVSavedSection onTop={value => {measurement.region = value;}}>
      <TVSavedToolbar region={{id: ids.savedToolbar, enabled: active.visible, orientation: 'horizontal',
        entry: tab === 'history' ? 'saved-period-7d' : 'saved-sort', edges: {up: ids.savedTabs, left: ids.rail, down: ids.savedGrid}}}
        count={t('count.titles', {count})} action={tab === 'history' ? <Button focusId="saved-clear-history" variant="glass" icon="i-trash"
          size="compact" onPress={openClear}>{t('web.saved.clearHistory')}</Button> : undefined}>
        {tab !== 'history' ? <>
          <Button focusId="saved-sort" ref={sortAnchor} variant="glass" icon="i-sort" size="compact" onPress={() => setSortOpen(true)} accessibilityLabel={t('saved.sort')}>{t(sortLabel)}</Button>
          <Button focusId="saved-filter" ref={filterAnchor} variant="glass" icon="i-filter" size="compact" onPress={() => setFilterOpen(true)} accessibilityLabel={t('filter.title')}>{t('filter.title')}</Button>
          <TVSavedToolbarDivider/>
          {quickFilters.length
            ? quickFilters.map(option => <Chip focusId={`saved-filter-${option.id}`} key={option.id} selected={filter === option.id}
              onPress={() => {const next = filter === option.id ? 'all' as const : option.id as SavedListFilter; setFilter(next); run(controller.select(tab, {sort, filter: next, period}));}}>{option.label}</Chip>)
            : filters.filter(value => value.id !== 'all').map(value => <Chip focusId={`saved-filter-${value.id}`} key={value.id} selected={filter === value.id}
              onPress={() => {const next = filter === value.id ? 'all' as const : value.id; setFilter(next); run(controller.select(tab, {sort, filter: next, period}));}}>{t(value.label)}</Chip>)}
        </> : <>
          <Button variant="glass" icon="i-sort" size="compact" disabled accessibilityLabel={t('saved.sort')}>{t('web.saved.sort.recent')}</Button>
          {periods.map(value => <Chip focusId={`saved-period-${value}`} key={value} selected={period === value} onPress={() => {setPeriod(value); run(controller.select(tab, {sort, filter, period: value}));}}>
            {value === '30d' || value === '90d' ? t('web.saved.days', {count: value === '30d' ? 30 : 90}) : t(periodLabel[value])}
          </Chip>)}
        </>}
      </TVSavedToolbar>
      <TVSavedResults region={{id: ids.savedGrid, enabled: active.visible, entry: emptyEntry, entryRegion: firstSection,
        edges: {up: ids.savedToolbar, left: ids.rail}}} onTop={value => {measurement.body = value;}}>{body}</TVSavedResults>
      </TVSavedSection>}
    </TVSavedScroll>
    <Menu testID={`${ids.savedGrid}.sort-menu`} open={sortOpen} onOpenChange={setSortOpen} anchorRef={sortAnchor} title={t('saved.sort')} placement="bottom-start" items={sorts.map(value => ({id: value.id, label: t(value.label), selected: value.id === sort,
      onSelect: () => {setSort(value.id); run(controller.select(tab, {sort: value.id, filter, period}));}}))}/>
    <Menu testID={`${ids.savedGrid}.filter-menu`} open={filterOpen} onOpenChange={setFilterOpen} anchorRef={filterAnchor} title={t('filter.title')} placement="bottom-start" items={publishedFilters.length
      ? publishedFilters.map(option => ({id: option.id, label: option.label, trailing: defaultI18n.number(option.count), selected: option.id === filter,
        onSelect: () => {setFilter(option.id as SavedListFilter); run(controller.select(tab, {sort, filter: option.id as SavedListFilter, period}));}}))
      : filters.map(value => ({id: value.id, label: t(value.label), selected: value.id === filter,
        onSelect: () => {setFilter(value.id); run(controller.select(tab, {sort, filter: value.id, period}));}}))}/>
    {/* tv-more3 draws History's Clear history confirm as the saved pages' own destructive
        confirm: Cancel first, then the red verb, and the sentence naming what stays. */}
    <SavedConfirmSheet open={clearView !== undefined} title={t('web.saved.clearHistoryTitle')} body={t('web.saved.clearHistoryBody')} action={t('web.saved.clearHistoryAction')}
      busy={clearing} destructive testID="saved-clear-history" onClose={() => {if (clearView) closeClear(clearView);}}
      onConfirm={() => {if (clearView) confirmClear(clearView);}}/>
    {selection?.render()}
  </>}/>;
}
