import {useCallback, useEffect, useMemo, useRef} from 'react';
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
  // every result row is drawn again with it while results mount, and tvOS loses the presses made meanwhile.
  const keyboardRow = useRef<string | undefined>(undefined);
  const setKeyboardRow = useCallback((row: string) => {keyboardRow.current = row;}, []);
  const view = useSearch({destination, runtime, explore, activation, actions});
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
  // Where a vanished result section sends focus: that key. One object whose fields are read when used, so the sections are
  // never drawn again for a key move.
  const screenStart = useMemo(() => ({
    get regionId() {return lastKey()?.regionId ?? '';},
    get childId() {return lastKey()?.childId ?? '';},
  }), [lastKey]);
  useEffect(() => runtime.registerScrollToTop({kind: 'search'}, () => scroll.current?.scrollTo({y: 0, animated: !theme.reduceMotion})), [runtime, theme.reduceMotion]);
  const beginVoice = () => {
    const finish = retainRecognizerReturn(registry, lastKey());
    if (!voice.begin?.(finish)) finish(false);
  };
  // Left from the results and Up from voice aim at the keyboard itself, which enters on the key that last held focus.
  const keyboard = ids.searchKeyboard;
  return {view, active, state, t, ids, scroll, keyboard, screenStart, setKeyboardRow, voice, beginVoice};
}
