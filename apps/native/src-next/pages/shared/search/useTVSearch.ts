import {useEffect, useMemo, useRef, useState} from 'react';
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
export function useTVSearch({focusScope, destination, runtime, activation, explore, actions}: NativeTVSearchPageProps) {
  const theme = useTheme(), scroll = useRef<ScrollView>(null), [keyboardRow, setKeyboardRow] = useState<string>();
  const view = useSearch({destination, runtime, explore, activation, actions});
  const {active, state, t} = view, authority = runtime.getSnapshot().authority;
  const ids = useMemo(() => nativeDestinationFocusIds(authority, focusScope), [authority, focusScope]), registry = useFocusRegistry();
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
      return registry.focusRegion(keyboardRow ?? ids.searchKeyboardRow(0));
    }, {kind: 'search'});
  }, [activation, active.visible, active.revision, ids, keyboardRow, registry, runtime]);
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
  const keyboard = keyboardRow ?? ids.searchKeyboardRow(0), keyboardKey = registry.remembered(keyboard);
  useEffect(() => runtime.registerScrollToTop({kind: 'search'}, () => scroll.current?.scrollTo({y: 0, animated: !theme.reduceMotion})), [runtime, theme.reduceMotion]);
  const beginVoice = () => {
    const finish = retainRecognizerReturn(registry, keyboardKey ? {regionId: keyboard, childId: keyboardKey} : undefined);
    if (!voice.begin?.(finish)) finish(false);
  };
  return {view, active, state, t, ids, scroll, keyboard, keyboardKey, setKeyboardRow, voice, beginVoice};
}
