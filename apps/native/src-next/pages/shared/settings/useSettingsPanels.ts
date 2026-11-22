import {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {NativeModules} from 'react-native';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import {idempotencyKey} from '../../../../../../packages/client-core/src/playback-v1/http';
import {localV1Http} from '../../../../../../packages/client-core/src/playback-v1/local-http';
import {SessionsClient} from '../../../../../../packages/client-core/src/playback-v1/sessions';
import {playbackControlsService, type RemotePlaybackControls} from '../../../../../../packages/client-core/src/playback-controls';
import {seekTarget, transportOffers, type TransportCommand} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {clockText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import type {MenuEntry} from '../../../kit/overlays';
import type {NativeShellRoute} from '../../../shell/routes';
import type {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsDownloads} from '../../../screens/settings-live/ports';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panel-deps';

/** The one viewer-scoped downloads store, read for its real used and free bytes;
 * the row opens the real Downloads route. No writable controls live here. */
type DownloadsStorageLease = Readonly<{
  route: NativeShellRoute; revision: number; downloads: NativeSettingsDownloads;
  lifetime: NativeScreenLifetime; live: {value: boolean};
}>;

/** Settings › Downloads storage: shown only on the Settings route while the summary is ready. Moved from settings-panels as it was. */
export function useDownloadsStorage({downloads, openRoute, context, lifetime}: NativeSettingsPanelDeps & {downloads: NativeSettingsDownloads}) {
  const navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot, context.navigation.getSnapshot);
  const snapshot = useSyncExternalStore(downloads.subscribe, downloads.getSnapshot, downloads.getSnapshot);
  const [lease, setLease] = useState<DownloadsStorageLease>();
  const settingsRoute = navigation.route.kind === 'shell' && navigation.route.destination.kind === 'settings';
  // The committed layout publishes an immutable lease for the exact Settings
  // route/revision, this summary and this lifetime. A route change re-publishes
  // and retires the old lease, so a retained row can never press from a stale one.
  useLayoutEffect(() => {
    if (!settingsRoute) {setLease(undefined); return;}
    const live = {value: true};
    const next = Object.freeze({route: navigation.route, revision: navigation.revision, downloads, lifetime, live});
    setLease(next);
    return () => {
      live.value = false;
      setLease(current => current === next ? undefined : current);
    };
  }, [downloads, lifetime, navigation.revision, navigation.route, settingsRoute]);
  if (!settingsRoute || !lease || lease.downloads !== downloads || !snapshot?.supported || !snapshot.ready) return undefined;
  const held = lease;
  const open = () => {
    if (!held.live.value || held.downloads !== downloads || !held.lifetime.current()) return;
    const now = context.navigation.getSnapshot();
    if (now.revision !== held.revision || now.route !== held.route) return;
    if (now.route.kind !== 'shell' || now.route.destination.kind !== 'settings') return;
    openRoute({kind: 'downloads'});
  };
  return {used: defaultI18n.bytes(snapshot.usedBytes), free: defaultI18n.bytes(snapshot.freeBytes), open};
}

const transportIcon: Record<TransportCommand, 'i-pause' | 'i-play' | 'i-rew' | 'i-ffw' | 'i-restart'> = {pause: 'i-pause', resume: 'i-play', back: 'i-rew', forward: 'i-ffw', startOver: 'i-restart'};

function nativeRequestId(): string {
  const runtime = NativeModules.PorticoRuntime as undefined | {playbackRequestId?: () => string};
  if (typeof runtime?.playbackRequestId !== 'function') throw new Error('native_settings_request_id_unavailable');
  return runtime.playbackRequestId();
}

/** What this profile is playing elsewhere, read from the server, and the commands each offers. Moved from settings-panels as it was. */
export function useRemoteControls({context, lifetime, t}: NativeSettingsPanelDeps) {
  const [items, setItems] = useState<readonly RemotePlaybackControls[]>([]);
  const [menu, setMenu] = useState<RemotePlaybackControls>();
  const controls = useMemo(() => playbackControlsService(async <T,>(path: string, method?: string, body?: unknown): Promise<T> => {
    lifetime.assert();
    const value = await context.api.request<T>(path, method, body, lifetime.signal);
    lifetime.assert();
    return value;
  }), [context, lifetime]);
  const sessions = useMemo(() => new SessionsClient(localV1Http(context.api)), [context]);
  useEffect(() => {
    // A failed read leaves what is drawn as it is; repeat backs the next one off.
    const poll = repeat(async signal => {const value = await controls.remote(); if (!signal.aborted) setItems(value.items);}, {everyMs: 10_000, backoff: {baseMs: 20_000, maxMs: 120_000}});
    return () => poll.stop();
  }, [controls]);
  const words: Record<TransportCommand, string> = {pause: t('action.pause'), resume: t('entry.resume'), back: t('player.skipBackSeconds', {seconds: 30}), forward: t('player.skipForwardSeconds', {seconds: 30}), startOver: t('action.startOver')};
  const send = (target: RemotePlaybackControls, what: TransportCommand | 'stop') => {
    lifetime.assert();
    const work = what === 'stop' ? sessions.stop(target.sessionId) : (async () => {
      await sessions.patch(target.sessionId, what === 'pause' || what === 'resume' ? {state: what === 'pause' ? 'paused' : 'playing'} : {seek: {positionMs: seekTarget(what, target.positionMs) ?? 0, id: idempotencyKey(() => nativeRequestId())}});
    })();
    void work.then(() => controls.remote().then(value => setItems(value.items), () => {}), () => {});
  };
  const entries = (target: RemotePlaybackControls): readonly MenuEntry[] => [
    ...transportOffers(target, target.actions).map((command): MenuEntry => ({id: command, label: words[command], icon: transportIcon[command], onSelect: () => send(target, command)})),
    ...(target.actions.includes('stop') ? [{id: 'stop', label: t('action.stop'), icon: 'i-stop' as const, destructive: true, onSelect: () => send(target, 'stop')}] : []),
  ];
  const stateWord = (target: RemotePlaybackControls) => target.state === 'paused' ? t('remote.state.paused') : target.state === 'playing' ? t('remote.state.playing') : t('remote.state.starting');
  /** A TV's row offers Pause or Resume in place, with how far along it is. */
  const tvRow = (target: RemotePlaybackControls) => {
    const offered = transportOffers(target, target.actions);
    const command = offered.includes('pause') ? 'pause' as const : offered.includes('resume') ? 'resume' as const : undefined;
    const progress = target.durationMs ? t('player.clockOf', {position: clockText(Math.min(target.positionMs, target.durationMs) / 1000), duration: clockText(target.durationMs / 1000)}) : undefined;
    return {command, progress};
  };
  return {items, menu, setMenu, entries, send, words, stateWord, tvRow};
}
