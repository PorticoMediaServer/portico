import {useEffect} from 'react';
import Animated, {useAnimatedRef} from 'react-native-reanimated';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {ShellDestination} from '../../shell/model';
import {useTheme} from '../theme';

export function useBrowseScroll(runtime: NativeShellRuntime, destination: Extract<ShellDestination, {kind: 'library'}> | {kind: 'libraries'}, enabled = true) {
  const scroll = useAnimatedRef<Animated.ScrollView>(), {reduceMotion} = useTheme();
  const kind = destination.kind, libraryId = destination.kind === 'library' ? destination.libraryId : undefined;
  useEffect(() => enabled ? runtime.registerScrollToTop(libraryId ? {kind: 'library', libraryId} : {kind: 'libraries'}, () => scroll.current?.scrollTo({y: 0, animated: !reduceMotion})) : undefined,
    [enabled, kind, libraryId, reduceMotion, runtime, scroll]);
  return {ref: scroll};
}
