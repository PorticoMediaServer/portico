import {useCallback, useDeferredValue, useEffect, useMemo, useRef} from 'react';
import type {ScrollView} from 'react-native';
import {useFocusRegistry} from '../../../kit/focus';
import {retainRecognizerReturn} from '../../../kit/focus/recognizer-return';
import {useTheme} from '../../../kit/theme';
import {useSearch} from './useSearch';
import {sameAuthority} from '../../../shell/model';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {nativeDestinationFocusIds, type NativeDestinationActivationPort} from '../../../screens/destinations/focus';
import {useTVVoiceSearch} from '../../../screens/settings-live/use-tv-voice-search';
import type {NativeSearchDestination} from '../../../screens/search/model';
import type {NativeSearchActions, NativeSearchExplorePort, NativeSearchSelectionPort, NativeSearchTVCopy} from '../../../screens/search/types';

export type NativeTVSearchPageProps = Readonly<{
  focusScope?: string; destination: NativeSearchDestination; runtime: NativeShellRuntime; activation: NativeDestinationActivationPort;
  explore: NativeSearchExplorePort; actions: NativeSearchActions; selection?: NativeSearchSelectionPort; copy: NativeSearchTVCopy;
}>;

/** The actual Search destination keeps its own service, IDs and navigation intent.
 * Only the shared keyboard requests entry focus; result arrival never moves it. */
export function useTVSearch({focusScope, destination, runtime, activation, explore, actions, selection}: NativeTVSearchPageProps) {
  const theme = useTheme(), scroll = useRef<ScrollView>(null);
  // The keyboard row that last held focus. Kept in a ref, not state: moving between keys must not draw the page again, or
  // every result row is drawn again with it while results mount, and tvOS loses the presses made meanwhile. (On Android TV
  // each card still follows the keyboard region for its Left edge, as on main.)
  const keyboardRow = useRef<string | undefined>(undefined);
  const setKeyboardRow = useCallback((row: string) => {keyboardRow.current = row;}, []);
  const read = useSearch({destination, runtime, explore, activation, actions});
  // While the next query is read, the results already drawn stay until its answer replaces them. Emptied at every key,
  // the results' whole tree (hundreds of native views) went away and came back a moment later, and tvOS lost the key
  // presses made while it did ("the" typed as "thb").
  const drawn = useRef<Pick<typeof read, 'groups' | 'top'> | undefined>(undefined);
  const reading = read.typed && !read.problem && (read.state.search.phase === 'idle' || read.state.search.phase === 'loading');
  if (!reading) drawn.current = read.groups.length || read.top ? {groups: read.groups, top: read.top} : undefined;
  const held = reading && !read.groups.length ? drawn.current : undefined;
  const view = held ? {...read, groups: held.groups, top: held.top} : read;
  const {active, state, t} = view, authority = runtime.getSnapshot().authority;
  const ids = useMemo(() => nativeDestinationFocusIds(authority, focusScope), [authority, focusScope]), registry = useFocusRegistry();
  // History once, the genres each time the page shows, and the selection over the results (as the touch page does).
  const historyLoaded = useRef(false);
  useEffect(() => {
    if (!active.visible) return;
    if (!historyLoaded.current) {historyLoaded.current = true; void destination.refreshHistory().catch(runtime.reportError);}
    void explore.load().catch(runtime.reportError);
  }, [active.visible, destination, explore, runtime]);
  useEffect(() => selection?.register(state.search, state.groups), [selection, state.search, state.groups]);
  useEffect(() => {
    if (!active.visible) return;
    return runtime.registerTVPageBack?.(() => {
      const current = activation.getSnapshot();
      // Retained/hidden pages, overlays and the rail never lend Search their Back. Results now
      // live in their own regions under the surrounding one, so a captured descendant counts too.
      const captured = registry.capture()?.regionId;
      const inResults = (regionId: string | undefined): boolean => {
        for (let id = regionId; id; id = registry.config(id)?.parent) if (id === ids.searchResults) return true;
        return false;
      };
      if (!current.visible || current.revision !== active.revision || !inResults(captured)) return false;
      // focusRegion uses only current enabled native key hosts and their real row memory.
      scroll.current?.scrollTo({y: 0, animated: false});
      return registry.focusRegion(keyboardRow.current ?? ids.searchKeyboardRow(0));
    }, {kind: 'search'});
  }, [activation, active.visible, active.revision, ids, registry, runtime]);
  const voiceLifetime = useMemo(() => {
    const signal = runtime.retirementSignal ?? new AbortController().signal;
    return {signal, current: () => {
      if (signal.aborted) return false;
      const now = activation.getSnapshot();
      return now.visible && now.revision === active.revision && sameAuthority(authority, runtime.getSnapshot().authority);
    }};
  }, [runtime, activation, active.revision, authority]);
  const voice = useTVVoiceSearch(theme.isTV && active.visible && !!runtime.retirementSignal, voiceLifetime,
    text => destination.setText(text), runtime.reportError);
  // The key that last held focus, read when it is needed.
  const lastKey = useCallback(() => {
    const row = keyboardRow.current ?? ids.searchKeyboardRow(0), childId = registry.remembered(row);
    return childId ? {regionId: row, childId} : undefined;
  }, [ids, registry]);
  // The results are drawn from a deferred copy of the page: a keystroke draws the keyboard and the field at once, and the
  // results (hundreds of native views on arrival) follow in work React can interrupt, so the presses and focus moves made
  // meanwhile are handled as they come instead of after seconds ("the" typed as "thb" on Apple TV, "tha" on Android TV).
  // The copy changes only with what the results show, so a move between keys does not draw them again.
  const menu = actions.menuState?.getSnapshot();
  // eslint-disable-next-line react-hooks/exhaustive-deps -- view is new at every draw; these are what it is made from
  const shown = useMemo(() => view, [read.state, read.landing, read.active, held, menu, runtime.text]);
  const results = useDeferredValue(shown);
  // Where a vanished result section sends focus: the key that held focus when the results were last drawn.
  // eslint-disable-next-line react-hooks/exhaustive-deps -- read with the results they belong to
  const screenStart = useMemo(() => lastKey(), [lastKey, results]);
  useEffect(() => runtime.registerScrollToTop({kind: 'search'}, () => scroll.current?.scrollTo({y: 0, animated: !theme.reduceMotion})), [runtime, theme.reduceMotion]);
  const beginVoice = () => {
    const finish = retainRecognizerReturn(registry, lastKey());
    if (!voice.begin?.(finish)) finish(false);
  };
  // Left from the results and Up from voice aim at the keyboard itself, which enters on the key that last held focus.
  const keyboard = ids.searchKeyboard;
  return {view, results, active, state, t, ids, scroll, keyboard, screenStart, setKeyboardRow, voice, beginVoice};
}
