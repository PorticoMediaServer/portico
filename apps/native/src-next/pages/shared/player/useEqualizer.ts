import {useEffect, useMemo, useRef, useSyncExternalStore} from 'react';
import {nativeEqualizer, type DeviceEqualizerStore} from '../../../platform/playback/equalizer';
import {useTVKeyEvent} from '../../../platform/tv-keys';
import {useTheme} from '../../../kit/theme';
import {nativeEqualizerControls, type NativeEqualizerTarget} from '../../../screens/watch/equalizer-controls';

export function useEqualizer({store = nativeEqualizer, reportError, active = true}: {
  store?: DeviceEqualizerStore; reportError: (error: unknown) => void; active?: boolean;
}) {
  const {isTV} = useTheme();
  const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot);
  const commands = useMemo(() => nativeEqualizerControls(store), [store]);
  const focused = useRef<NativeEqualizerTarget | undefined>(undefined);
  useEffect(() => {
    if (store.getSnapshot().supported && !store.getSnapshot().ready) void store.refresh().catch(reportError);
  }, [store, reportError]);
  useTVKeyEvent(event => {
    if (!isTV || !active || focused.current === undefined || event.phase === 'long-end' || !['up', 'down'].includes(event.key)) return;
    if (store.getSnapshot().value?.on) void commands.nudge(focused.current, event.key === 'up' ? 1 : -1).catch(reportError);
  });
  const execute = (promise: Promise<unknown>) => {void promise.catch(reportError);};
  return {
    snapshot,
    onEnable: (on: boolean) => execute(commands.enable(on)),
    onReset: () => execute(commands.reset()),
    onGain: (target: NativeEqualizerTarget, gain: number) => execute(commands.gain(target, gain)),
    onNudge: (target: NativeEqualizerTarget, direction: -1 | 1) => execute(commands.nudge(target, direction)),
    onBandFocus: (target: NativeEqualizerTarget) => {focused.current = target;},
    onBandBlur: (target: NativeEqualizerTarget) => {if (focused.current === target) focused.current = undefined;},
  };
}
