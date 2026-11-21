import React from 'react';
import {BrowseToolbar} from '../../../kit/browse';
import {HomeRowCard} from '../../../kit/browse/HomeRowCard';
import {FocusItem} from '../../../kit/focus';
import {BrowseFrame} from '../../../kit/frames';
import {BrowseGrid} from '../../../kit/frames/BrowseGrid';
import {StateView} from '../../../kit/states';
import {nativeLibraryEntryKey} from '../../../screens/library/model';
import {GenreFilters} from '../../../screens/search/GenreFilters';
import type {GenreBrowseProps} from '../../shared/search/useGenreBrowse';
import {useTVGenreBrowse} from '../../shared/search/useTVGenreBrowse';

/** A genre from Search's Browse by genre on TV: the kit browse page with "‹ Search" and the genre's name, the toolbar's
 * Filters, Sort and the count, then the genre's titles as the library's poster grid at the TV's card size. Focus starts on
 * Back once the titles are in; Down reaches the toolbar, then the grid. */
export function GenreBrowsePage(props: GenreBrowseProps) {
  const {runtime, actions, onBack} = props, view = useTVGenreBrowse(props), {t, focus} = view;
  return <>
    <BrowseFrame testID="native-search-genre" focus={focus.header} startFocus={view.ready ? focus.header.entry : undefined}
      header={{title: props.name, back: {label: t('web.shell.search'), onPress: onBack, testID: 'search-genre-back'}}}
      state={view.failure ?? (!view.ready ? {kind: 'loading', message: view.elapsed > 3000 ? t('next.states.stillLoading') : undefined} : undefined)}
      toolbar={view.model ? <BrowseToolbar testID="search-genre-toolbar" model={view.model} labels={view.labels} resultCount={view.count} region={focus.toolbar}
        onQuickChange={view.onQuickChange} onFilters={view.openFilters} filtersOpen={!!view.filters} onSortChange={view.onSort} onReshuffle={view.reshuffle}/> : undefined}
      grid={view.gridFailure ? <StateView {...view.gridFailure} variant="centered"/> : view.collection
        ? <BrowseGrid cardSizePercent={runtime.appearanceCardSizePercent} shape="poster" testID="search-genre-grid" collection={view.collection} region={focus.grid}
            getKey={nativeLibraryEntryKey} renderItem={(entry, cell, width, onCaptionLayout) => <FocusItem focusId={nativeLibraryEntryKey(entry)} cell={{index: cell.index}}>
              <HomeRowCard entry={entry} width={width} runtime={runtime} onCaptionLayout={onCaptionLayout} onOpen={() => actions.open(entry)}
                onPlay={() => actions.play(entry)} onMenu={anchor => actions.menu(entry, anchor)} onFocus={cell.onFocus} onBlur={cell.onBlur}/>
            </FocusItem>}/> : undefined}/>
    {view.filters && view.state.capabilities && view.state.sort && <GenreFilters draft={view.filters} capabilities={view.state.capabilities}
      sort={view.state.sort} fixed={view.fixed} runtime={runtime} owner={props.owner} labels={view.labels} onClose={view.closeFilters}/>}
  </>;
}
