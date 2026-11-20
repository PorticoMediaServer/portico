import React, {type ComponentProps, type ReactNode} from 'react';
import {SearchFrame} from '../../../kit/frames';
import {TVSearchQuery, TVSearchResults} from '../../../kit/search/TVSearchContent';

export function TVSearchPage({query, keyboard, results, overlay, visible}: {
  query: ComponentProps<typeof TVSearchQuery>; keyboard: ReactNode; results: ComponentProps<typeof TVSearchResults>;
  overlay: ReactNode; visible: boolean;
}) {
  return <SearchFrame field={<TVSearchQuery {...query}/>} keyboard={keyboard} list={<TVSearchResults {...results}/>}
    overlay={overlay} visible={visible} testID="native-tv-search-screen"/>;
}
