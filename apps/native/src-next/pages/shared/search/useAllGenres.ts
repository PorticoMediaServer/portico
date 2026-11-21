import {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSearchGenreList} from '../../../screens/search/explore';
import type {NativeSearchGenre} from '../../../screens/search/types';
import {browseFailure} from '../library/useLibrary';

export type AllGenresProps = Readonly<{focusScope?: string; explore: NativeSearchGenreList; runtime: NativeShellRuntime; visible: boolean;
  current: () => boolean; onBack: () => void}>;

/** All genres (desktop-search's "All genres ›"): the same canonical genre read as Browse by genre, genres without tile
 * artwork too, in the server's order. It reads a page while the page is in front, follows the returned cursor as the list
 * scrolls either way (never reranking or expanding a library), reports which genres are in view, and opens one. */
export function useAllGenres({explore, runtime, visible, current}: AllGenresProps) {
  const t = runtime.text.t, state = useSyncExternalStore(explore.subscribe, explore.getSnapshot);
  const scroll = useRef(0), direction = useRef<'next' | 'previous' | undefined>(undefined), [elapsed, setElapsed] = useState(0);
  useEffect(() => {if (visible && current()) void explore.load();}, [current, explore, visible]);
  useEffect(() => {
    if (state.phase !== 'loading') {setElapsed(0); return;}
    const start = Date.now(), timer = setTimeout(() => setElapsed(Math.max(300, Date.now() - start)), 300);
    return () => clearTimeout(timer);
  }, [state.phase]);
  // The list's rows: one object per read, so a list drawn again (Back from a genre) keeps the same data.
  const items = useMemo(() => state.genres.map(genre => ({id: genre.id, name: genre.name, count: t('next.selection.titles', {count: genre.count}), genre})), [state.genres, t]);
  const inView = useCallback((ids: readonly string[]) => explore.setVisible([...ids]), [explore]);
  /** The list's scroll: past its end reads the next page, back at its top the previous one. */
  const onScroll = (y: number, height: number, contentHeight: number) => {
    const previous = scroll.current; scroll.current = y;
    direction.current = y > previous ? 'next' : y < previous ? 'previous' : undefined;
    if (!current() || state.phase !== 'ready') return;
    if (direction.current === 'next' && y + height >= contentHeight - 1) void explore.next();
    else if (direction.current === 'previous' && y <= 1) void explore.previous();
  };
  const onNearStart = () => {if (current() && state.phase === 'ready' && direction.current === 'previous') void explore.previous();};
  const onNearEnd = () => {if (current() && state.phase === 'ready' && direction.current === 'next') void explore.next();};
  const retry = () => {if (current()) void explore.load();};
  const failure = state.phase === 'error' ? runtime.error(state.error, retry) : undefined;
  return {t, state, items, elapsed, inView, onScroll, onNearStart, onNearEnd,
    /** The whole page failed (nothing read yet), or a later page failed under the genres already shown. */
    failure: failure && !state.genres.length ? browseFailure(failure) : undefined, footerFailure: failure && state.genres.length ? failure : undefined,
    open: (genre: NativeSearchGenre) => {if (current()) explore.openGenre(genre);},
    focus: (genre: NativeSearchGenre, focused: boolean) => explore.setFocused(genre.id, focused)};
}
