import React from 'react';
import {BrowseFrame} from '../../../kit/frames';
import {GenreList} from '../../../kit/search/GenreList';
import {StateView} from '../../../kit/states';
import {useAllGenres, type AllGenresProps} from '../../shared/search/useAllGenres';

/** All genres on TV (desktop-search's "All genres ›", reached from the See all stop after Search's genre tiles): the kit
 * browse page with "‹ Search" and Browse by genre, then every genre as a row. Focus starts on the first genre; Up from the
 * first reaches Back (the list's region is free, so Up finds it by position: an edge to the page's region resolves to the row
 * it remembers). The regions stay enabled while the page is covered (it is not drawn then): the layer stack gives focus back
 * to the row it left as the page is uncovered, before the page has drawn itself as in front. */
export function AllGenresPage(props: AllGenresProps) {
  const view = useAllGenres(props), {t, state} = view, scope = props.focusScope ?? 'search-all', first = state.genres[0];
  return <BrowseFrame testID="native-search-all-genres" focus={{id: scope, entry: 'back', edges: {down: `${scope}.list`}}}
    startFocus={first ? `genre:${first.id}` : undefined}
    header={{title: t('next.search.browseByGenre'), back: {label: t('web.shell.search'), onPress: props.onBack, testID: 'search-all-genres-back'}}}
    state={view.failure ?? (state.phase === 'loading' && !state.genres.length ? {kind: 'loading', message: view.elapsed > 3000 ? t('next.states.stillLoading') : undefined} : undefined)}
    grid={<GenreList testID="all-genres-list" region={{id: `${scope}.list`, entry: first ? `genre:${first.id}` : undefined, orientation: 'free'}}
      items={view.items}
      onOpen={item => view.open(item.genre)} onFocusChange={(item, focused) => view.focus(item.genre, focused)} onInView={view.inView}
      onScroll={view.onScroll} onNearStart={view.onNearStart} onNearEnd={view.onNearEnd} footer={view.footerFailure && <StateView {...view.footerFailure}/>}/>}/>;
}
