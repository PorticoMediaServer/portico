import {useNativeStackHosted, useNativeStackPlacement} from '../../kit/navigation/stack-slot';
import React, {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {preferenceValue, ReadingError, type PreferenceSnapshot, type ReadingSession} from '../../../../../packages/client-core/src';
import type {ReadingHostSession} from '../../../../../packages/client-core/src/reading-host';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {createNativeDownloadsDevice} from '../downloads/device';
import {offlineDownloads} from '../../../platform/offline-downloads';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute, NativePresentationBinding} from '../../shell/routes';
import {LoadingPaint, StateView, SkeletonBlock} from '../../kit/states';
import {Button} from '../../kit/controls/Button';
import {useTheme} from '../../kit/theme';
import {NativeReadingGuestHost} from './NativeReadingGuestHost';
import {closeReadingResource, createReadingSessionOwner} from './reading-session-owner';
import {NativeAnnotationJournal, nativeAnnotationIdentity, nativeAnnotationLeaseOf, type NativeAnnotationFormat} from './annotation-journal';
import {nativeAnnotationBridge} from './annotation-session';
import {NativeAnnotationSync, type NativeAnnotationLivePort} from './annotation-sync';
import {NATIVE_OFFLINE_ANNOTATION_AUTHORING} from './annotation-authoring';

type Target = Readonly<{itemId: string; sourceId: string; downloadId?: string; expectedSourceRevision?: string; contentsTarget?: Readonly<{sourceId: string; sourceRevision: string; index: number}>}>;
type Session = ReadingHostSession & Pick<ReadingSession, 'close'>;
const cancelled = () => new ReadingError('cancelled');

/** The live adapter owns the original session and its authority. A guest never owns it. */
export function ReadNativeHost({context, target, active, preferences, onBack, onOpenSettings, resident = true, binding, onGuestState}: {
  context: NativeShellBootstrapContext; target: Target; active: boolean; preferences: Readonly<{getSnapshot: () => PreferenceSnapshot; subscribe: (listener: () => void) => () => void}>;
  onBack: () => void; onOpenSettings: () => void; resident?: boolean; binding?: NativePresentationBinding; onGuestState?: (mounted: boolean, incarnation: object) => void;
}) {
  const theme = useTheme(), dimensions = useWindowDimensions(), insets = useSafeAreaInsets();
  const place = useNativeStackPlacement(target), stacked = useNativeStackHosted();
  const callbacks = useRef({active, onBack, onOpenSettings}); callbacks.current = {active, onBack, onOpenSettings};
  const [retry, setRetry] = useState(0);
  const mounted = useRef(false);
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false;};}, []);
  const journalOwner = useMemo(() => ({context, journal: new NativeAnnotationJournal()}), [context]);
  const journal = journalOwner.journal;
  const annotations = useMemo(() => new NativeAnnotationSync(context, journal), [context, journal]);
  const quietRetry = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const livePort = useRef<NativeAnnotationLivePort | undefined>(undefined);
  const annotationOwner = useRef<object | undefined>(undefined);
  useEffect(() => () => {clearTimeout(quietRetry.current); annotations.dispose();}, [annotations]);
  useEffect(() => {
    if (!NATIVE_OFFLINE_ANNOTATION_AUTHORING) return;
    if (active && !target.downloadId) void annotations.flush(livePort.current);
    const subscription = AppState.addEventListener('change', next => {if (!target.downloadId && next === 'active' && callbacks.current.active) void annotations.flush(livePort.current);});
    return () => subscription.remove();
  }, [active, annotations, target.downloadId]);

  const ownerBinding = useMemo(() => {
    const captured = context.session();
    const authorityCurrent = () => {if (!captured || !context.current()) return false;
      const actual = context.session();
      return !!actual && actual.sessionFamilyId === captured.sessionFamilyId
        && (['authority', 'serverId', 'accountId', 'profileId', 'role'] as const).every(key => actual.viewer[key] === captured.viewer[key])
        && context.current();};
    const current = () => authorityCurrent() && (!binding || binding.retainedCurrent()) && authorityCurrent();
    const sessionOwner = createReadingSessionOwner({current, subscribe: listener => binding ? binding.subscribe(listener) : context.subscribeAuthority(listener), acquire: async (signal, ownerCurrent) => {
      if (!captured || !ownerCurrent()) throw cancelled();
      const downloads = target.downloadId ? createNativeDownloadsDevice(context) : undefined;
      // The original authority, not the retired guest, permits only the bounded
      // close of this captured session. Authority replacement still refuses IO.
      const clientLife = new AbortController();
      const client = target.downloadId ? undefined : context.api.reading({viewer: captured.viewer, isCurrent: authorityCurrent, signal: clientLife.signal});
      let original: Session | undefined, released = false;
      const annotationTicket = {};
      const release = () => {if (released) return; released = true;
        if (annotationOwner.current === annotationTicket) {annotationOwner.current = undefined; livePort.current = undefined; journal.unbind();}
        clientLife.abort(); client?.dispose(); downloads?.dispose();};
      try {
        if (target.downloadId) {
          if (!downloads) throw new Error('reading_unavailable');
          await downloads.device.open(); if (!ownerCurrent()) throw cancelled();
          const entry = offlineDownloads.getSnapshot().entries.find(value => value.id === target.downloadId && value.itemId === target.itemId);
          if (!entry || offlineDownloads.publicationSource(entry).sourceId !== target.sourceId) throw new Error('reading_unavailable');
          const paused = () => {try {return preferenceValue(preferences.getSnapshot(), 'privacy.pauseWatchHistory') !== false;} catch {return true;}};
          original = await offlineDownloads.readingSession(entry, {signal, current: ownerCurrent, paused});
        } else {
          if (!client) throw new Error('reading_unavailable');
          const versions = await client.versions(target.itemId, signal); if (!ownerCurrent()) throw cancelled();
          if (!versions.versions.some(value => value.sourceId === target.sourceId && value.inventoryAvailable)) throw new Error('reading_unavailable');
          original = await client.open({itemId: target.itemId, sourceId: target.sourceId}, signal);
        }
        if (!ownerCurrent()) throw cancelled();
        const revision = original.manifest.conversion?.originalRevision ?? original.manifest.revision;
        if (target.expectedSourceRevision !== undefined && revision !== target.expectedSourceRevision) throw new ReadingError('source_changed');
        if (!target.downloadId && NATIVE_OFFLINE_ANNOTATION_AUTHORING) {
          annotationOwner.current = annotationTicket;
          livePort.current = original.manifest.canAnnotate === true && typeof original.annotations === 'function' && typeof original.mutateAnnotation === 'function' ? original as NativeAnnotationLivePort : undefined;
          journal.bind(nativeAnnotationIdentity(captured.viewer), nativeAnnotationLeaseOf(original));
          if (ownerCurrent()) void annotations.flush(livePort.current);
        }
        const issued = original.manifest.format;
        const format: NativeAnnotationFormat | undefined = issued === 'epub' || issued === 'pdf' || issued === 'cbz' ? issued : undefined;
        const bridge = !target.downloadId && format ? nativeAnnotationBridge({session: original, journal, identity: nativeAnnotationIdentity(captured.viewer),
          source: {itemId: target.itemId, sourceId: target.sourceId, sourceRevision: original.manifest.revision, format}, fresh: ownerCurrent,
          onQueued: () => {if (quietRetry.current || !NATIVE_OFFLINE_ANNOTATION_AUTHORING) return;
            quietRetry.current = setTimeout(() => {quietRetry.current = undefined; if (ownerCurrent()) void annotations.flush(livePort.current);}, 15_000);}}) : original;
        const accepted = original;
        const session: Session = {manifest: bridge.manifest, signal: bridge.signal, headPDF: bridge.headPDF, readRange: bridge.readRange,
          readAsset: bridge.readAsset, page: bridge.page, progress: bridge.progress, saveProgress: bridge.saveProgress,
          ...(bridge.openSequential ? {openSequential: bridge.openSequential} : {}), ...(bridge.annotations ? {annotations: bridge.annotations} : {}),
          ...(bridge.mutateAnnotation ? {mutateAnnotation: bridge.mutateAnnotation} : {}),
          close: async closeSignal => {if (authorityCurrent()) await accepted.close(closeSignal);}};
        if (!ownerCurrent()) throw cancelled();
        return {session, release};
      } catch (error) {
        if (original && authorityCurrent()) {try {await closeReadingResource({session: original, release});} catch { /* Preserve the original admission failure. */ }}
        else release();
        throw error;
      }
    }});
    return {retry, owner: sessionOwner};
  }, [context, target, preferences, retry, journal, annotations, binding]);
  const owner = ownerBinding.owner;
  // Render replacement retires the old original before a late callback can borrow the new one.
  const rendered = useRef(owner);
  if (rendered.current !== owner) {void rendered.current.dispose().catch(() => {}); rendered.current = owner;}
  const snapshot = useSyncExternalStore(owner.subscribe, owner.snapshot, owner.snapshot);
  const latest = useRef({owner, snapshot, retry, active, resident}); latest.current = {owner, snapshot, retry, active, resident};
  const retryTicket = useMemo(() => ({spent: false, owner, snapshot}), [owner, snapshot]);
  const retryOriginal = () => {
    const same = () => mounted.current && !retryTicket.spent && rendered.current === owner && latest.current.owner === owner
      && latest.current.snapshot === snapshot && latest.current.retry === retry && latest.current.active && latest.current.resident
      && callbacks.current.active && snapshot.phase === 'failed' && !snapshot.session;
    if (!same()) return;
    try {if (!owner.current() || !context.current() || (binding && !binding.activeCurrent()) || !owner.current() || owner.snapshot() !== snapshot) return;} catch {return;}
    if (!same()) return; retryTicket.spent = true; setRetry(value => value + 1);
  };
  useEffect(() => {void Promise.resolve().then(() => owner.ensure()).catch(() => {}); return () => {void owner.dispose().catch(() => {});};}, [owner]);
  const onSearchAll = (query: string, signal: AbortSignal, isLive: () => boolean) => {
    if (!owner.current() || signal.aborted) return false;
    let live = false; try {live = isLive();} catch {live = false;}
    if (!live || !owner.current() || (binding && !binding.activeCurrent()) || context.navigation.getSnapshot().route !== target) return false;
    const destination: NativeShellRoute = {kind: 'search-reading', query};
    return context.navigation.allows(destination) && owner.current() && context.navigation.open(destination, context.authority());
  };
  if (snapshot.phase === 'ready' && resident) return place(<NativeReadingGuestHost owner={owner} active={active} preferences={preferences}
    onBack={onBack} onSettings={onOpenSettings} onSearchAll={onSearchAll} contentsTarget={target.contentsTarget} onGuestState={onGuestState} activeCurrent={binding?.activeCurrent}/>);
  if (!resident) return null;
  const failed = snapshot.phase === 'failed' || snapshot.phase === 'retired';
  return place(<View pointerEvents={active ? 'auto' : 'none'} accessibilityElementsHidden={!active} importantForAccessibility={active ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, backgroundColor: theme.colors.ink, opacity: active || stacked ? 1 : 0}}>
    {failed ? <StateView variant="centered" title={defaultI18n.t('next.states.pageFailed')} body={defaultI18n.t('next.states.pageFailedBody')}
      primaryAction={{id: 'retry', label: defaultI18n.t('next.states.tryAgain'), disabled: !active || snapshot.phase === 'retired' || !!snapshot.session || !context.current(), onPress: retryOriginal}}/>
      : <LoadingPaint ready={false} skeleton={<SkeletonBlock width="100%" style={{flex: 1}}/>}/>}
    <View pointerEvents="box-none" style={{position: 'absolute', left: 12, top: insets.top + 8, zIndex: 20}}>
      <Button testID="native-read-host-back" variant="icon" size="compact" icon="i-left" iconOnly accessibilityLabel={defaultI18n.t('action.back')}
        focusRadius={dimensions.width >= 700 ? 22 : 19} onPress={() => {if (callbacks.current.active) onBack();}}
        style={{height: dimensions.width >= 700 ? 44 : 38, width: dimensions.width >= 700 ? 44 : 38, paddingHorizontal: 0}}/>
    </View>
  </View>);
}
