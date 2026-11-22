import React from 'react';
import {ReadingSearchFrame, ReadingSearchHead, ReadingSearchResults} from '../../../kit/reading/ReadingSearchParts';
import {StateView} from '../../../kit/states';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeReadingSearchPort, ReadingSearchOpening} from '../../../screens/search/reading-search';
import {useReadingSearch} from '../../shared/read/useReadingSearch';

/** Root54: Search all books, the reader's own search across the books it can read. Only the indexed answer is shown: the
 * excerpt and its match, the title and cover when the server gave them, and one continuation at a time. No total, place,
 * page or progress is implied. */
export function ReadingSearchPage({port, opening, runtime, query, onBack}: Readonly<{
  port: NativeReadingSearchPort; opening: ReadingSearchOpening | undefined; runtime: NativeShellRuntime; query: string; onBack: () => void;
}>) {
  const view = useReadingSearch({port, opening, runtime, query}), {t, state} = view;
  return <ReadingSearchFrame>
    <ReadingSearchHead title={t('next.reader.searchAll')} back={{label: t('action.back'), onPress: onBack}} value={view.text} onChange={view.edit}
      onSubmit={view.submit} placeholder={t('web.search.placeholder')} scope={t('next.reader.indexedSearchScope')} inputRef={view.input}/>
    {state.phase === 'error' ? <StateView {...runtime.error(state.error, view.retry)}/>
      : <ReadingSearchResults listRef={view.list} hits={view.hits} elapsed={view.elapsed} viewport={view.viewport} onViewport={view.setViewport}
        cover={view.cover} onOpen={view.open} onEnd={view.more} extraData={state}
        empty={state.phase === 'ready' ? <StateView variant="search" title={t('next.reader.noIndexedMatches')} body={t('next.reader.indexedSearchScope')}/> : null}
        footer={state.phase === 'more' ? 'loading' : state.phase === 'ready' && state.moreError ? <StateView variant="search" {...runtime.error(state.moreError, view.retryMore)}/> : null}/>}
  </ReadingSearchFrame>;
}
