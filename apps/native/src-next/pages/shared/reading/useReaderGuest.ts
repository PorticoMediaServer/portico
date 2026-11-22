import {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {NativeModules, useWindowDimensions} from 'react-native';
import type WebView from 'react-native-webview';
import type {PreferenceSnapshot} from '../../../../../../packages/client-core/src/preferences';
import type {ReadingHostSearchAll, ReadingHostContentsTarget} from '../../../../../../packages/client-core/src/reading-host';
import {usePlatformPosture} from '../../../platform/platformPosture';
import type {ReaderHintMemory} from '../../../platform/reader-hint-memory';
import {readerAssetEntry} from '../../../screens/read/asset-origin';
import {createReadingGuestBridge} from '../../../kit/reading/ReadingGuestView';
import type {createReadingSessionOwner} from '../../../screens/read/reading-session-owner';

type Owner = ReturnType<typeof createReadingSessionOwner>;
type Reader = ReturnType<typeof createReadingGuestBridge>;

/** One disposable rendering ticket over an already accepted logical session. */
export type ReaderGuestProps = {
  owner: Owner; active: boolean; preferences: Readonly<{getSnapshot: () => PreferenceSnapshot; subscribe: (listener: () => void) => () => void}>;
  onBack: () => void; onSettings: () => void; onSearchAll?: ReadingHostSearchAll; contentsTarget?: ReadingHostContentsTarget; onGuestState?: (mounted: boolean, incarnation: object) => void; activeCurrent?: () => boolean;
  /** The first-run hint, remembered on this device (the guest's own storage is off): read when the guest starts. */
  hint?: ReaderHintMemory;
};

export function useReaderGuest({owner, active, preferences, onBack, onSettings, onSearchAll, contentsTarget, onGuestState, activeCurrent, hint}: ReaderGuestProps) {
  const view = useRef<WebView>(null), reader = useRef<Reader | undefined>(undefined);
  const dimensions = useWindowDimensions(), posture = usePlatformPosture();
  const crease = posture.status === 'available' && posture.window?.width === dimensions.width && posture.window.height === dimensions.height
    ? posture.folds.find(value => value.orientation === 'vertical' && value.bounds.left > 0 && value.bounds.left <= value.bounds.right && value.bounds.right <= dimensions.width)?.bounds : undefined;
  const creaseRef = useRef(crease); creaseRef.current = crease;
  const disposeGuest = useRef<(() => void) | undefined>(undefined);
  const callbacks = useRef({active, onBack, onSettings, onSearchAll, hint}); callbacks.current = {active, onBack, onSettings, onSearchAll, hint};
  /** One pure presence boolean: the bridge port and the guest effect's key read the same value. */
  const hasSearchAll = !!onSearchAll;
  const [state, setState] = useState<Readonly<{url?: string; reader?: Reader; failed?: boolean}>>({});
  const [attempt, setAttempt] = useState(0);
  const mounted = useRef(false);
  const latest = useRef({owner, state, attempt, active, activeCurrent}); latest.current = {owner, state, attempt, active, activeCurrent};
  const retryTicket = useMemo(() => ({owner, state, attempt, spent: false}), [owner, state, attempt]);
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false;};}, []);
  /** Owner-intent port: the committed observer for this layout turn. It is published in the
   * layout phase before the guest setup below, and its cleanup clears only its own port. */
  const guestObserver = useRef<{report?: (mounted: boolean, incarnation: object) => void} | undefined>(undefined);
  useLayoutEffect(() => {
    const port = {report: onGuestState};
    guestObserver.current = port;
    return () => {if (guestObserver.current === port) guestObserver.current = undefined;};
  }, [onGuestState]);
  const retryOriginal = () => {
    const same = () => mounted.current && !retryTicket.spent && latest.current.owner === owner && latest.current.state === state
      && latest.current.attempt === attempt && latest.current.active && latest.current.activeCurrent === activeCurrent
      && callbacks.current.active && state.failed === true;
    if (!same()) return;
    try {if (!owner.current() || (activeCurrent && !activeCurrent()) || !owner.current()) return;} catch {return;}
    if (!same()) return; retryTicket.spent = true; setAttempt(value => value + 1);
  };
  useLayoutEffect(() => {
    let alive = true, owned: Reader | undefined, ticket: ReturnType<Owner['attach']> | undefined, offPreferences = () => {};
    const current = () => {if (!alive || !ticket?.current()) return false;
      return owner.current() && alive && !!ticket?.current();};
    const dispose = () => {if (!alive) return; alive = false; offPreferences(); owned?.dispose();
      if (reader.current === owned) reader.current = undefined; ticket?.release();};
    const report = guestObserver.current?.report, incarnation = {}; report?.(true, incarnation);
    disposeGuest.current = dispose; setState({});
    void (async () => {
      ticket = owner.attach(); ticket.onRetire(dispose); if (!current()) throw new Error('reading_retired');
      const bundle: unknown = await NativeModules.PorticoReaderAssets?.bundle();
      const url = bundle && typeof bundle === 'object' && 'url' in bundle ? bundle.url : undefined;
      if (!readerAssetEntry(url) || !current()) throw new Error('reader_assets_unavailable');
      owned = createReadingGuestBridge({session: ticket.session, initialProgress: ticket.initialProgress,
        preferences: preferences.getSnapshot, current, initialContentsTarget: contentsTarget,
        ...(creaseRef.current ? {crease: {left: creaseRef.current.left, right: creaseRef.current.right}} : {}),
        onBack: () => {if (current() && callbacks.current.active) callbacks.current.onBack();},
        onSettings: () => {if (current() && callbacks.current.active) callbacks.current.onSettings();},
        ...(hasSearchAll ? {onSearchAll: (query, signal, isLive) => current() && callbacks.current.active && !!callbacks.current.onSearchAll
          ? callbacks.current.onSearchAll(query, signal, isLive) : false} : {}),
        onUnavailable: () => {if (current()) setState({failed: true}); dispose();},
        ...(callbacks.current.hint ? {hintSeen: callbacks.current.hint.seen(), onHintSeen: () => {if (current()) callbacks.current.hint?.markSeen();}} : {}),
      }, view, () => {});
      if (!current()) {owned.dispose(); throw new Error('reading_retired');}
      reader.current = owned; owned.setActive(callbacks.current.active);
      offPreferences = preferences.subscribe(() => {if (current()) owned?.confirm();});
      if (!current()) throw new Error('reading_retired');
      setState({url, reader: owned});
    })().catch(() => {if (current()) setState({failed: true}); dispose();});
    return () => {if (disposeGuest.current === dispose) disposeGuest.current = undefined; dispose(); report?.(false, incarnation);};
  }, [owner, preferences, attempt, hasSearchAll, contentsTarget]);
  useLayoutEffect(() => {reader.current?.setActive(active);}, [active, state.reader]);
  useLayoutEffect(() => {reader.current?.setCrease(crease ? {left: crease.left, right: crease.right} : undefined);}, [crease, state.reader]);
  const failure = () => {if (!state.reader || reader.current !== state.reader) return; setState({failed: true}); disposeGuest.current?.();};
  const back = () => {if (callbacks.current.active) onBack();};
  return {state, view, currentReader: () => reader.current, onFailure: failure, back, retryOriginal};
}
