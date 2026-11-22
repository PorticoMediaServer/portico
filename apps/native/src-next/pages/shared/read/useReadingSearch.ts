import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {FlatList, TextInput} from 'react-native';
import type {ReadingIndexedSearchHit} from '../../../../../../packages/client-core/src/reading-indexed-search';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeReadingSearchPort, ReadingSearchOpening} from '../../../screens/search/reading-search';

/** Root54: the reader's own Search all books. The literal words are searched as typed; only the server's indexed answer is
 * shown, one bounded continuation at a time. Moved from the old screen as it was. */
export function useReadingSearch({port, opening, runtime, query}: Readonly<{
  port: NativeReadingSearchPort; opening: ReadingSearchOpening | undefined; runtime: NativeShellRuntime; query: string;
}>) {
  const t = runtime.text.t, input = useRef<TextInput>(null), list = useRef<FlatList<ReadingIndexedSearchHit>>(null);
  const state = useSyncExternalStore(port.subscribe, port.getSnapshot);
  const [text, setText] = useState(query), [viewport, setViewport] = useState({width: 0, height: 0}), [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    if (state.phase !== 'loading') {setElapsed(0); return;}
    const start = Date.now(), timer = setTimeout(() => setElapsed(Math.max(300, Date.now() - start)), 300);
    return () => clearTimeout(timer);
  }, [state.phase]);
  // The literal words are never rewritten. This local draft revision only stops a
  // queued Submit from an older render sending words the field no longer holds.
  const draft = useRef(0), revision = draft.current;
  const edit = (value: string) => {draft.current += 1; setText(value);};
  const submit = () => {
    if (draft.current !== revision) return;
    port.search(opening, text);
    list.current?.scrollToOffset({offset: 0, animated: false});
  };
  const hits = state.phase === 'loading' ? undefined : state.hits;
  return {
    t, input, list, state, text, edit, submit, hits, elapsed, viewport, setViewport,
    retry: () => {if (state.phase === 'error' && state.query) port.search(opening, state.query, state.witness);},
    open: (hit: ReadingIndexedSearchHit) => port.open(opening, state.witness, hit),
    more: () => {if (state.phase === 'ready' && !state.moreError) port.more(opening, state.witness);},
    retryMore: () => port.retry(opening, state.witness),
    cover: (hit: ReadingIndexedSearchHit) => runtime.artwork(hit.coverUrl),
  };
}
