import {useEffect, useRef} from 'react';
import {Skeleton} from '../../kit/states';
import {redirectOnce, type RedirectMemory} from '../../shell/navigation';
import type {LibraryScreenSearch} from '../browse/route-search';
import {authorPredicate} from '@core/presentation/library-tabs.ts';

/** An author's card in a Reading library opens the books grid filtered to them (the name is
 * remembered from the Authors list), replacing this address so Back returns to Authors. It asks
 * once per author: the library page may redraw while the router is on its way (#3259's rule). */
export function ReadingAuthorOpen({navigate, pivot, authorId}: Readonly<{navigate: (value: LibraryScreenSearch, options?: {replace?: boolean}) => void; pivot?: string; authorId: string}>) {
  const memory = useRef<RedirectMemory>({});
  useEffect(() => {
    redirectOnce(memory.current, `${authorId}|${pivot ?? ''}`, () => navigate({view: 'browse', ...(pivot ? {pivot} : {}), query: authorPredicate(authorId)}, {replace: true}));
  }, [navigate, pivot, authorId]);
  return <Skeleton quiet height={480}/>;
}
