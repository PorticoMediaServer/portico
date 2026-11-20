import React from 'react';
import {LibraryMenu, LibrarySwitcher, BrowseToolbar, FilteredEmpty} from '../../../kit/browse';
import {LibraryCard} from '../../../kit/browse/LibraryCard';
import {LibraryListRow} from '../../../kit/browse/LibraryListRow';
import {LibraryShelf} from '../../../kit/browse/LibraryShelf';
import {useLibraryFrameFilters} from '../../../kit/browse/LibraryFilterFields';
import {BrowseFrame} from '../../../kit/frames';
import {useBrowseScroll} from '../../../kit/frames/useBrowseScroll';
import {useLibraryMixAnchor} from '../../../kit/browse/LibraryShelf';
import {BrowseGrid} from '../../../kit/frames/BrowseGrid';
import {AlphabetRail} from '../../../kit/layout';
import {Artwork, AmbientArt} from '../../../kit/artwork';
import {Banner} from '../../../kit/overlays';
import {StateView} from '../../../kit/states';
import {useLibrary, useLibraryGrid, nativeLibraryEntryKey, type LibraryProps} from '../../shared/library/useLibrary';

export function LibraryPage(props: LibraryProps) {
  const {runtime, destination, actions, labels, owner, shelfSelection, mixAnchor} = props;
  const view = useLibrary(props), {state, t} = view;
  const discover = state.tab.shows === 'rows';
  const scroll = useBrowseScroll(runtime, {kind: 'library', libraryId: state.library.id}, discover);
  const anchor = useLibraryMixAnchor(scroll.ref, !!mixAnchor && view.active.visible && view.active.intent !== 'restore');
  const mixes = discover && props.mixes?.({onReady: anchor.reveal, onLayout: anchor.layout});
  const filters = useLibraryFrameFilters({draft: view.filters, generation: view.generation, source: state, runtime, labels, owner,
    live: false, onSave: draft => actions.saveView(destination, draft)});
  const header = {title: state.library.name, tabs: view.tabs,
    switcher: <LibrarySwitcher libraries={view.choices} value={state.library.id} onValueChange={id => runtime.onDestination({kind: 'library', libraryId: id})}/>,
    menu: view.menu && <LibraryMenu {...view.menu}/>,
    ambient: view.ambient ? <AmbientArt source={view.ambient} treatment="library"/> : undefined,
    back: {label: t('next.browse.native.libraries'), onPress: () => runtime.onDestination({kind: 'libraries'})},
  };
  const notice = view.summaryError ? <Banner placement="inline" variant="notice" tone="warning" icon="i-alert"
    message={t('next.browse.librarySummariesUnavailable')} action={{label: t('next.states.tryAgain'), onPress: view.retrySummaries}}/> : undefined;
  return <>
    {state.collection && !view.failure && !view.emptyState
      ? <LibraryCatalogue {...props} view={view} header={header} filters={filters} notice={notice}/>
      : <BrowseFrame testID="library-frame" header={header} filters={filters} notice={notice} scroll={discover ? scroll : undefined} onRowsLayout={anchor.rowsLayout}
          grid={state.tab.shows !== 'rows' && !view.emptyState && !view.failure}
          state={view.failure ?? view.emptyState ?? (state.phase === 'loading' || state.phase === 'idle' ? {kind: 'loading'} : undefined)}>
        {shelfSelection?.render(path => path && <Artwork source={runtime.artwork(path)}/>)}
        {state.discover.error ? <StateView {...runtime.error(state.discover.error, destination.retry, 'library')}/> : null}
        {view.rows.map((row, index) => <React.Fragment key={row.id}><LibraryShelf key={row.id} title={row.title} items={row.entries}
          shape={row.shape === 'landscape' ? 'wide' : row.shape ?? 'poster'}
          getKey={nativeLibraryEntryKey} onVisibleRange={row.onVisibleRange} onSeeAll={row.onSeeAll}
          renderItem={(entry, cell, width, onCaptionLayout) => {
            const selected = shelfSelection?.item(entry);
            return <LibraryCard entry={entry} family={state.library.kind} width={width} wide={row.shape === 'landscape'} runtime={runtime} actions={actions}
              focusId={cell.focusId} onFocus={cell.onFocus} onBlur={cell.onBlur} selected={selected} onSelect={selected?.onEnter} onCaptionLayout={onCaptionLayout}/>;
          }}/>{index === 0 ? mixes : null}</React.Fragment>)}
        {!view.rows.length ? mixes : null}
      </BrowseFrame>}
  </>;
}

function LibraryCatalogue(props: LibraryProps & {
  view: ReturnType<typeof useLibrary>;
  header: React.ComponentProps<typeof BrowseFrame>['header'];
  filters: React.ComponentProps<typeof BrowseFrame>['filters'];
  notice: React.ReactNode;
}) {
  const {view, header, filters, notice, runtime, destination, labels, actions} = props;
  const {state} = view, grid = useLibraryGrid(props, state);
  const list = grid.view.value === 'list', alphabet = grid.hasAlphabet && !list;
  const toolbar = <>{notice}<BrowseToolbar model={grid.model} labels={labels} onFilters={view.openFilters} filtersOpen={filters?.open}
    resultCount={undefined} resultTotal={grid.data.total} onQuickChange={destination.setQuick} onSortChange={grid.setSort} onReshuffle={grid.reshuffle}
    applied={grid.appliedChips} onRemoveFilter={grid.remove} onClearFilters={grid.clear}
    selection={grid.selected?.active && grid.selection?.render(path => path && <Artwork source={runtime.artwork(path)}/>)}
    view={{value: grid.view.value, onValueChange: grid.onValueChange}}/>
    {grid.data.status === 'error' && <StateView {...runtime.error(grid.data.error, destination.retry, 'library')}/>}</>;
  return <BrowseFrame testID="library-frame" header={header!} toolbar={toolbar} filters={filters}
    alphabet={alphabet && grid.data.positionIndex && grid.data.total !== undefined ? <AlphabetRail positionIndex={grid.data.positionIndex}
      total={grid.data.total} firstVisible={grid.range.first} lastVisible={grid.range.last} testID="library-alphabet"
      onJump={index => grid.grid.current?.scrollToIndex(index)}/> : undefined}
    grid={grid.filteredEmpty ? <FilteredEmpty onClearFilters={grid.clear}/> : <BrowseGrid key={grid.view.revision} gridRef={grid.grid}
      testID={list ? 'library-list' : 'library-grid'} shape={list ? 'list' : grid.shape} alphabet={alphabet}
      collection={grid.collection} getKey={nativeLibraryEntryKey} selection={grid.selected}
      onVisibleRange={grid.onVisibleRange} onScrollOffset={grid.onScrollOffset} refreshControl={grid.refresh}
      initialIndex={grid.initialIndex} renderItem={(entry, cell, width, onCaptionLayout) => {
        const item = {entry, width, runtime, actions, focusId: cell.focusId ?? nativeLibraryEntryKey(entry), onFocus: cell.onFocus, onBlur: cell.onBlur,
          selected: cell.selection, onSelect: cell.selection?.onEnter ?? grid.selection?.start, disabled: !view.active.visible};
        return list ? <LibraryListRow {...item}/> : <LibraryCard {...item} family={state.library.kind} onCaptionLayout={onCaptionLayout}/>;
      }}/>}/>;
}
