import React from 'react';
import {BrowseToolbar} from '../../../kit/browse';
import {HomeRowCard} from '../../../kit/browse/HomeRowCard';
import {BrowseFrame} from '../../../kit/frames';
import {BrowseGrid} from '../../../kit/frames/BrowseGrid';
import {StateView} from '../../../kit/states';
import {nativeLibraryEntryKey} from '../../../screens/library/model';
import {GenreFilters} from '../../../kit/search/GenreFilterSheet';
import {useGenreBrowse, type GenreBrowseProps} from '../../shared/search/useGenreBrowse';

/** A genre from Search's Browse by genre on a phone or tablet: the kit browse page with "‹ Search", the genre's name, Filters
 * and Sort with the count, then its titles as the library's poster grid. */
export function GenreBrowsePage(props: GenreBrowseProps) {
  const {runtime, actions, onBack} = props, view = useGenreBrowse(props), {t} = view;
  return <>
    <BrowseFrame testID="native-search-genre" header={{title: props.name, back: {label: t('web.shell.search'), onPress: onBack, testID: 'search-genre-back'}}}
      state={view.failure ?? (!view.ready ? {kind: 'loading', message: view.elapsed > 3000 ? t('next.states.stillLoading') : undefined} : undefined)}
      toolbar={view.model ? <BrowseToolbar testID="search-genre-toolbar" model={view.model} labels={view.labels} resultCount={undefined} resultTotal={view.total} onQuickChange={view.onQuickChange}
        onFilters={view.openFilters} filtersOpen={!!view.filters} onSortChange={view.onSort} onReshuffle={view.reshuffle}/> : undefined}
      grid={view.gridFailure ? <StateView {...view.gridFailure} variant="centered"/> : view.collection
        ? <BrowseGrid cardSizePercent={runtime.appearanceCardSizePercent} shape="poster" testID="search-genre-grid" collection={view.collection}
            getKey={nativeLibraryEntryKey} renderItem={(entry, cell, width, onCaptionLayout) => <HomeRowCard entry={entry} width={width} runtime={runtime}
              onCaptionLayout={onCaptionLayout} onOpen={() => actions.open(entry)} onPlay={() => actions.play(entry)}
              onMenu={anchor => actions.menu(entry, anchor)} onFocus={cell.onFocus} onBlur={cell.onBlur}/>}/> : undefined}/>
    {view.filters && view.state.capabilities && view.state.sort && <GenreFilters draft={view.filters} capabilities={view.state.capabilities}
      sort={view.state.sort} fixed={view.fixed} runtime={runtime} owner={props.owner} labels={view.labels} onClose={view.closeFilters}/>}
  </>;
}
