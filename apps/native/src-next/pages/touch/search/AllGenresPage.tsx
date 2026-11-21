import React from 'react';
import {BrowseFrame} from '../../../kit/frames';
import {GenreList} from '../../../kit/search/GenreList';
import {StateView} from '../../../kit/states';
import {useAllGenres, type AllGenresProps} from '../../shared/search/useAllGenres';

/** All genres on a phone or tablet (desktop-search's "All genres ›"): the kit browse page with "‹ Search" and Browse by genre,
 * then every genre as a row with how many titles; a row opens that genre's page. */
export function AllGenresPage(props: AllGenresProps) {
  const view = useAllGenres(props), {t, state} = view;
  return <BrowseFrame testID="native-search-all-genres" header={{title: t('next.search.browseByGenre'), back: {label: t('web.shell.search'), onPress: props.onBack, testID: 'search-all-genres-back'}}}
    state={view.failure ?? (state.phase === 'loading' && !state.genres.length ? {kind: 'loading', message: view.elapsed > 3000 ? t('next.states.stillLoading') : undefined} : undefined)}
    grid={<GenreList testID="all-genres-list" items={view.items}
      onOpen={item => view.open(item.genre)} onFocusChange={(item, focused) => view.focus(item.genre, focused)} onInView={view.inView}
      onScroll={view.onScroll} onNearStart={view.onNearStart} onNearEnd={view.onNearEnd} footer={view.footerFailure && <StateView {...view.footerFailure}/>}/>}/>;
}
