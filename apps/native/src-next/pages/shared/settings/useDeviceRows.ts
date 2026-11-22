import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, NativeModules, Platform} from 'react-native';
import {appleProfile, type AppleNativeFacts} from '../../../../../../packages/client-core/src/client-profile';
import {installationIdentity} from '../../../../../../packages/client-core/src/installation';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {passthroughFormats, type PassthroughFormat} from '../../../platform/playback/passthrough-formats';
import {nativeDeviceSettings} from '../../../platform/device-settings';
import {bindNativeDevicePreferences} from '../../../screens/settings/device-preference-bindings';
import type {NativeDevicePreferences} from '../../../screens/settings-live/device-preference-consumer';

/** This device's playback rows (Settings › This device and Quality): each reads its one installation setting and writes
 * it through the device record, one write at a time, re-reading what was kept; a write that a retired screen started
 * reports nothing. Moved from the six old row screens as they were. */
export const nativeFrameRateFocusId = 'settings-device-match-frame-rate';
export const nativeDynamicRangeFocusId = 'settings-device-match-dynamic-range';
export const nativePassthroughFocusId = 'settings-device-audio-passthrough';
export const nativeLossNoticesFocusId = 'settings-device-loss-notices';
export const nativeSafeModeFocusId = 'settings-quality-safe-mode';

/** One guarded write at a time, for as long as this row and its shell context are current. */
function useDeviceWrite(context: NativeShellBootstrapContext) {
  const live = useMemo(() => ({owner: context, current: false}), [context]);
  const pending = useRef(false), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, [live]);
  const run = async (write: () => Promise<unknown>, check?: () => boolean) => {
    if (!live.current || !live.owner.current() || pending.current) return;
    pending.current = true; setBusy(true); setError(undefined);
    try {await write(); if (check && live.current && live.owner.current() && !check()) throw new Error('native_device_preference_unreadable');}
    catch (failure) {if (live.current && live.owner.current()) setError(failure);}
    finally {pending.current = false; if (live.current && live.owner.current()) setBusy(false);}
  };
  return {busy, error, clearError: () => setError(undefined), run};
}

/** Match frame rate or Match dynamic range: Automatic or Off. */
export function useDisplayMatch(context: NativeShellBootstrapContext, key: 'matchFrameRate' | 'matchDynamicRange') {
  const source = useMemo(() => nativeDeviceSettings(context), [context]);
  const value = useSyncExternalStore(source.subscribe, () => source.read(key));
  const write = useDeviceWrite(context);
  return {...write, value: value === 'auto' || value === 'off' ? value : undefined,
    choose: (next: string) => {if (next === 'auto' || next === 'off') void write.run(() => source.write(key, next), () => source.read(key) === next);}};
}

/** Audio passthrough, for every format or one: Automatic, Off, or (for all) Custom when formats differ. */
export function usePassthrough(context: NativeShellBootstrapContext, format?: PassthroughFormat) {
  const source = useMemo(() => nativeDeviceSettings(context), [context]);
  const value = useSyncExternalStore(source.subscribe, () => format ? source.readPassthroughFormat(format) : source.read('disablePassthrough') === true ? 'off' : passthroughFormats.some(key => source.readPassthroughFormat(key) === 'off') ? 'custom' : 'auto');
  const write = useDeviceWrite(context);
  return {...write, value, choose: (next: string) => {
    if (next !== 'auto' && next !== 'off') return;
    void write.run(() => source.writePassthroughFormat(format ?? 'all', next), () => format ? source.readPassthroughFormat(format) === next : !passthroughFormats.some(key => source.readPassthroughFormat(key) !== next));
  }};
}

/** Show loss notices: one installation preference; Playback Info keeps every fact. */
export function useLossNotices(context: NativeShellBootstrapContext) {
  const source = useMemo(() => bindNativeDevicePreferences(context), [context]);
  const value = useSyncExternalStore(source.subscribe, () => source.read('device.lossNotices')?.current);
  const write = useDeviceWrite(context);
  return {...write, value: typeof value === 'boolean' ? value : undefined, set: (next: boolean) => {void write.run(() => source.write('device.lossNotices', next));}};
}

export function nativeSafeModeAvailable(source: NativeDevicePreferences): boolean {
  return typeof source.read('forceStereo') === 'boolean' && typeof source.read('disableHdr') === 'boolean';
}
/** Safe mode: a preset over two real committed fields (stereo and no HDR), never a third stored setting. */
export function useSafeMode(source: NativeDevicePreferences, current: () => boolean) {
  const inFlight = useRef(false), alive = useRef(true), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const snapshot = () => JSON.stringify([source.read('forceStereo'), source.read('disableHdr')]);
  const values = useSyncExternalStore(source.subscribe, snapshot);
  const [stereo, hdr] = JSON.parse(values) as unknown[];
  const apply = async (choice: string) => {
    if (inFlight.current || !current() || !['automatic', 'safe'].includes(choice)) return;
    inFlight.current = true; setBusy(true); setError(undefined);
    try {
      const asked = choice === 'safe';
      await source.write('forceStereo', asked);
      if (!alive.current || !current()) return;
      await source.write('disableHdr', asked);
      if (!alive.current || !current()) return;
      if (source.read('forceStereo') !== asked || source.read('disableHdr') !== asked) throw new Error('native_device_preference_unreadable');
    } catch (failure) {if (alive.current && current()) setError(failure);}
    finally {inFlight.current = false; if (alive.current && current()) setBusy(false);}
  };
  return {busy, error, clearError: () => setError(undefined), choose: (choice: string) => {void apply(choice);},
    value: typeof stereo !== 'boolean' || typeof hdr !== 'boolean' ? undefined : stereo && hdr ? 'safe' as const : !stereo && !hdr ? 'automatic' as const : 'custom' as const};
}

type Facts = Readonly<{eligible?: boolean; atmos: boolean}>;
/** Canonical Apple input only: the raw facts go through client-profile's own appleProfile, and Atmos is claimed only when
 * that normalised native engine actually carries eac3 objectAudio 'atmos'. A missing value is not evidence. */
function readFacts(value: unknown): Facts | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const facts = value as AppleNativeFacts;
  let atmos = false;
  try {
    const document = appleProfile({television: true, osVersion: String(Platform.Version), model: facts.model,
      appVersion: installationIdentity()?.appVersion}, facts);
    atmos = document.engines.native.audio?.some(audio => audio.codec === 'eac3' && (audio.objectAudio ?? []).includes('atmos')) === true;
  } catch {atmos = false;}
  return {eligible: typeof facts.audio?.eac3JocEligible === 'boolean' ? facts.audio.eac3JocEligible : undefined, atmos};
}
/** The Apple TV's passthrough capability, read again whenever the app comes back to the front. */
export function useApplePassthrough() {
  const [facts, setFacts] = useState<Facts | null>();
  const generation = useRef(0);
  useEffect(() => {
    let alive = true;
    const load = async () => {
      const read = ++generation.current;
      try {
        const value = await (NativeModules.PorticoCapabilities?.facts?.() as Promise<unknown> | undefined);
        if (!alive || read !== generation.current) return;
        setFacts(readFacts(value) ?? null);
      } catch {
        if (alive && read === generation.current) setFacts(null);
      }
    };
    void load();
    const subscription = AppState.addEventListener('change', value => {if (value === 'active') void load();});
    return () => {alive = false; subscription.remove();};
  }, []);
  return facts;
}
