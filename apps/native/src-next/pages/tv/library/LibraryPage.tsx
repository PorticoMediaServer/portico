import React from 'react';
import {BrowseFrame, type BrowseHeaderProps} from '../../../kit/frames';
import {BrowseGrid} from '../../../kit/frames/BrowseGrid';
import {BrowseToolbar, FilteredEmpty, LibrarySwitcher} from '../../../kit/browse';
import {LibraryJumpSheet} from '../../../kit/browse/LibraryJumpSheet';
import {LibraryCard} from '../../../kit/browse/LibraryCard';
import {LibraryShelf} from '../../../kit/browse/LibraryShelf';
import {useLibraryFrameFilters} from '../../../kit/browse/LibraryFilterFields';
import {StateView} from '../../../kit/states';
import {Artwork} from '../../../kit/artwork';
import {Banner} from '../../../kit/overlays';
import {FocusItem} from '../../../kit/focus';
import {nativeLibraryEntryKey, useTVLibrary, useTVLibraryGrid, type LibraryProps} from '../../shared/library/useLibrary';

export function LibraryPage(props: LibraryProps) {
  const view = useTVLibrary(props), {state, active, t} = view;
  const {runtime, destination, actions, labels, owner, shelfSelection} = props;
  const filters = useLibraryFrameFilters({draft: view.filters, generation: view.generation, source: state, runtime, labels, owner, live: false});
  const header: BrowseHeaderProps = {title: state.library.name, tabs: view.tabs,
    switcher: <LibrarySwitcher libraries={view.choices} value={state.library.id} focusRegion={view.switcher}
      onValueChange={id => runtime.onDestination({kind: 'library', libraryId: id})}/>};
  const notice = view.summaryError && <Banner placement="inline" variant="notice" tone="warning" icon="i-alert"
    message={t('next.browse.librarySummariesUnavailable')} action={{label: t('next.states.tryAgain'), onPress: view.retrySummaries}}/>;
  if (state.collection && !view.failure) return <TVCatalogue {...props} view={view} header={header} filters={filters} notice={notice}/>;
  const loading = state.phase === 'loading' || state.phase === 'idle';
  const pageState = view.failure ?? view.emptyState;
  const shelfBar = shelfSelection?.render(path => path && <Artwork source={runtime.artwork(path)}/>);
  return <BrowseFrame testID="library-frame" header={header} notice={notice} toolbar={props.tvMusicDiscover ? shelfBar : undefined}
    state={pageState ? {...pageState, focus: view.pageState} : loading ? {kind: 'loading'} : undefined}
    grid={props.tvMusicDiscover ?? (loading && state.tab.shows !== 'rows')}>
    {!props.tvMusicDiscover && shelfBar}
    {state.discover.error ? <StateView {...runtime.error(state.discover.error, destination.retry, 'library')}/> : null}
    {view.shelves.map(row => <LibraryShelf key={row.id} title={row.title} items={row.entries}
      shape={row.shape === 'landscape' ? 'wide' : row.shape ?? 'poster'} focus={row.focus} getKey={nativeLibraryEntryKey} onVisibleRange={row.onVisibleRange}
      renderItem={(entry, cell, width, onCaptionLayout) => {
        const selected = shelfSelection?.item(entry);
        return <LibraryCard entry={entry} family={state.library.kind} width={width} wide={row.shape === 'landscape'} runtime={runtime} actions={actions}
          focusId={cell.focusId} onFocus={cell.onFocus} onBlur={cell.onBlur} selected={selected} onSelect={selected?.onEnter} onCaptionLayout={onCaptionLayout} disabled={!active.visible}/>;
      }}/>)}
  </BrowseFrame>;
}

function TVCatalogue(props: LibraryProps & {
  view: ReturnType<typeof useTVLibrary>; header: BrowseHeaderProps; filters: React.ComponentProps<typeof BrowseFrame>['filters']; notice: React.ReactNode;
}) {
  const {view, header, filters, notice, runtime, destination, labels, actions} = props;
  const {state, active} = view, grid = useTVLibraryGrid(props, view, view.gridId, view.toolbarId);
  const toolbar = <>{notice}<BrowseToolbar model={grid.model} labels={labels} onFilters={view.openFilters} filtersOpen={filters?.open}
    resultCount={grid.count} onQuickChange={destination.setQuick} onSortChange={grid.setSort} onReshuffle={grid.reshuffle}
    applied={grid.appliedChips} onRemoveFilter={grid.remove} onClearFilters={grid.clear}
    onJump={grid.openJump} jumpEnabled={grid.jump.letters.length > 0} region={grid.toolbar} appliedRegion={grid.applied}
    save={{canSave: view.canSave, onSave: view.save}}
    selection={grid.selected?.active && grid.selection?.render(path => path && <Artwork source={runtime.artwork(path)}/>)}/>
    {grid.data.status === 'error' && <StateView {...runtime.error(grid.data.error, destination.retry, 'library')}/>}</>;
  return <BrowseFrame testID="library-frame" header={header} toolbar={view.emptyState ? undefined : toolbar} filters={filters}
    gridState={view.emptyState && {...view.emptyState, focus: view.pageState}}
    grid={<>
      {grid.filteredEmpty ? <FilteredEmpty onClearFilters={grid.clear} focus={grid.region}/>
        : <BrowseGrid gridRef={grid.grid} testID="library-grid" shape={grid.shape} region={grid.region} initialIndex={grid.initialIndex}
          collection={grid.collection} getKey={nativeLibraryEntryKey} selection={grid.selected}
          onVisibleRange={grid.onVisibleRange} onScrollOffset={grid.onScrollOffset} onItemFocus={grid.onItemFocus}
          renderItem={(entry, cell, width) => <LibraryCard entry={entry} family={state.library.kind} width={width} runtime={runtime} actions={actions}
            focusId={cell.focusId} onFocus={cell.onFocus} onBlur={cell.onBlur} selected={cell.selection}
            onSelect={cell.selection?.onEnter ?? grid.selection?.start} disabled={!active.visible}/>}
          renderNativeSlot={(entry, cell, width, height) => <FocusItem focusId={cell.focusId} entityId={cell.entityId} waiting={cell.waiting}
            cell={{index: cell.index, pending: cell.waiting, generation: cell.source}}>
            <LibraryCard entry={entry} family={state.library.kind} width={width} placeholderHeight={height} runtime={runtime} actions={actions}
              focusId={cell.focusId} onFocus={cell.onFocus} onBlur={cell.onBlur} selected={cell.selection}
              onSelect={cell.selection?.onEnter ?? grid.selection?.start} disabled={!active.visible}/>
          </FocusItem>}/>}
      <LibraryJumpSheet {...grid.jump}/>
    </>}/>;
}
