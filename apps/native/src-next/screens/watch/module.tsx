import {groupPort, subscribeGroupPort} from '../settings-live/group-port';
import {nativeDeviceSettings} from '../../platform/device-settings';
import React from 'react';
import {audiobookPreferences} from '../../../../../packages/client-core/src/audiobook-preferences';
import {Platform, type View} from 'react-native';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {sameFence} from '../../../../../packages/player-core/src';
import {minimizeTarget} from './minimize';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {qualityOptionSelected} from '../../../../../packages/client-core/src/playback-v1/quality';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativePlaybackRuntimeSnapshot} from '../../platform/playback/types';
import {createNativeWatchScreens, type NativeWatchFactoryPorts, type NativeWatchMenuExtra} from './factory';
import type {MusicPlaylistPickerHandle} from '../destinations/music/MusicPlaylistPicker';
import {NativePlaybackInfoPanel, playbackInfoWords} from './PlaybackInfoPanel';
import {NativeWatchPanels, type NativeWatchPanelsProps, type NativeWatchStylePort} from './WatchPanels';
import {NativeWatchPanelOptions} from './panel-options';
import type {NativeWatchHostBindings} from './WatchHost';
import {sameAuthority} from '../../shell/model';
import type {NativePlaybackFailure} from './PlaybackFailed';
import type {NativeWatchPanelNames} from './panel-model';
import {nativeWatchTracks} from './track-identity';
import {NativeWatchChannelRoute} from './ChannelRoute';
import {NativeTVLiveGuide} from './live-guide';
import {NativeTVLiveActions} from './live-actions';
import {NativeLiveActionPanels} from './LiveActionPanels';
import {NativePlaybackFailed} from './PlaybackFailed';
import {NativeHandheldPlaybackFailed} from './HandheldPlaybackFailed';
import {NativeWatchPostPlay} from './PostPlayScreen';
import {createNativeWatchPanelNames} from './panel-names';
import {nativeSubtitleStyleKeys, nativeSubtitleStyleValue} from '../../platform/playback/subtitle-style-choices';
import {browseValueLabel} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import {bindNativeVideoSystemMedia} from './system-media';
import {nativePostPlayNavigation} from './post-play-navigation';
import {playbackUserMessage} from '../../../../../packages/client-core/src/playback-messages';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {DetailsPanel, type StateFact} from '../../kit/states/DetailsPanel';

export type NativeWatchEntryMenu = Readonly<{
  open: (entry: ContentEntry, entryCurrent: () => boolean, anchorRef?: React.RefObject<View | null>, select?: () => void, extraActions?: readonly NativeWatchMenuExtra[]) => Promise<void>;
  isOpen: (entry: ContentEntry) => boolean;
  subscribe: (listener: () => void) => () => void;
}>;

export type NativeWatchModuleBindings = Pick<NativeWatchFactoryPorts,
  'requestId' | 'title' | 'manageFile' | 'openFileInfo' | 'reopenFileInfo' | 'openMenu' | 'openVersions' | 'beginVersions' | 'startVersion'> & Readonly<{
  /** Canonical localized names/copy and real error UI; no selection enters here. */
  names?: NativeWatchPanelNames;
  /** One playlist picker per authority-local module, rendered by its overlay outlet. */
  playlistPicker?: MusicPlaylistPickerHandle;
  copy?: NativeWatchPanelsProps['copy'];
  playbackInfo?: NativeWatchPanelsProps['playbackInfo'];
  episodes?: NativeWatchPanelsProps['episodes'];
  presentation?: NativeWatchHostBindings['presentation'];
  onPictureInPicture?: ReturnType<NativeWatchHostBindings['chrome']>['onPictureInPicture'];
  renderOverlays?: (runtime: NativeShellRuntime) => React.ReactNode;
  dispose?: () => void;
}>;

/** Current session identities only. An offered default/ceiling is never a
 * current track or decoded picture observation. Missing producer facts stay absent. */
export function nativeWatchChromeFacts(source: NativePlaybackRuntimeSnapshot, names: NativeWatchPanelNames) {
  const tracks = nativeWatchTracks(source), fence = source.fence;
  if (tracks.local) return {quality: '', audio: tracks.audio ? names.audio(tracks.audio, source).primary : undefined,
    subtitle: tracks.subtitle ? names.subtitle(tracks.subtitle, source).primary : tracks.subtitlesOff ? defaultI18n.t('player.opt.off') : undefined};
  if (!tracks.version || !fence || !source.session || !source.options || !sameFence(source.dock.local?.fence, fence)) return {quality: ''};
  const quality = source.options.qualities.find(option => qualityOptionSelected(option, source.session!.presentation.quality));
  return {
    quality: quality ? names.quality(quality, source).primary : '',
    audio: tracks.audio ? names.audio(tracks.audio, source).primary : undefined,
    subtitle: tracks.resource || tracks.subtitle ? names.subtitle((tracks.resource ?? tracks.subtitle)!, source).primary : tracks.subtitlesOff ? defaultI18n.t('player.opt.off') : undefined,
  };
}

/** Authority-local domain composition. The shared route registry owns its
 * lifetime. This binds the SAME bootstrap adapter/outlet and never mounts Surface. */
export function createNativeWatchModule(context: NativeShellBootstrapContext, bindings: NativeWatchModuleBindings) {
  if (!context.current() || !context.session()) throw new Error('native_watch_authority_required');
  const authority = context.authority(), viewer = context.session()!.viewer;
  const detailScope = {serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId])};
  const options = new NativeWatchPanelOptions(context.api, context.playback);
  const copy = bindings.copy ?? {sessionLead: defaultI18n.t('player.opt.temporaryChanges'), sessionDefaults: defaultI18n.t('player.opt.defaultsUnchanged'), audioContinues: defaultI18n.t('player.opt.hearingContinues')};
  let disposed = false;
  const current = () => !disposed && context.current();
  const stopSystemMedia = bindNativeVideoSystemMedia(context, bindings.title.reportError);
  const liveGuide = new NativeTVLiveGuide(context, !Platform.isTV);
  // Media's retry changes the failed phase and source itself, so these controllers
  // belong to the module and its route, never to the error surface. They abort on a
  // route/authority replacement, a collapsed watch or disposal — not on phase,
  // source or session changes — and the core fences unrelated intents itself.
  const retries = new Set<{controller: AbortController; route: Readonly<{authority: ReturnType<typeof context.authority>; revision: number}>}>();
  const expanded = () => context.playback.getSnapshot().dock.local?.expanded === true
    && (!Platform.isTV || groupPort()?.playerVisible?.() !== false);
  const liveActions = liveGuide ? new NativeTVLiveActions(context, liveGuide, () => current() && expanded(), subscribeGroupPort) : undefined;
  const abortAll = () => {for (const operation of retries) operation.controller.abort(); retries.clear();};
  // A pending retry belongs to the route it was started under. Collapsing the watch,
  // replacing the route or authority, or disposing the module aborts it; the retry's
  // own phase, source and session changes never do.
  const stopPlayback = context.playback.subscribe(() => {if (!expanded()) abortAll();});
  // Returning to a Together room hides Watch without stopping its local output.
  // Its presentation lease still retires the retry that the hidden Watch owned.
  const stopGroup = Platform.isTV ? subscribeGroupPort(() => {if (!expanded()) abortAll();}) : () => {};
  const stopNavigation = context.navigation.subscribe(() => {
    const route = {authority: context.authority(), revision: context.navigation.getSnapshot().revision};
    for (const operation of [...retries]) {
      if (sameAuthority(operation.route.authority, route.authority) && operation.route.revision === route.revision) continue;
      operation.controller.abort();
      retries.delete(operation);
    }
  });
  // Only an operation whose captured authority is no longer current is aborted; an
  // unrelated authority notification leaves the rest alone.
  const stopAuthority = context.subscribeAuthority(() => {
    const live = context.authority(), retired = !context.current();
    for (const operation of [...retries]) {
      if (!retired && sameAuthority(operation.route.authority, live)) continue;
      operation.controller.abort();
      retries.delete(operation);
    }
  });
  /** Dispatch one failure the viewer actually saw, under the route it was drawn on.
   * The captured route travels with the operation; nothing reads a later route. */
  const retryFailed = (failure: NativePlaybackFailure, route: Readonly<{authority: ReturnType<typeof context.authority>; revision: number}>,
    report: (error: unknown) => void) => {
    const live = context.playback.getSnapshot();
    if (!current() || !sameAuthority(context.authority(), route.authority)) return;
    if (context.navigation.getSnapshot().revision !== route.revision) return;
    if (live.capabilities.retry !== true || !live.fence || !sameFence(live.fence, failure.fence) || !expanded()) return;
    const retry = context.playback.retry;
    if (!retry) return;
    const controller = new AbortController(), operation = {controller, route};
    retries.add(operation);
    const isCurrent = () => !disposed && !controller.signal.aborted && current() && expanded()
      && sameAuthority(context.authority(), route.authority) && context.navigation.getSnapshot().revision === route.revision;
    void retry(failure.fence, {signal: controller.signal, isCurrent})
      .catch(error => {if (isCurrent()) report(error);})
      .finally(() => retries.delete(operation));
  };
  const skipSeconds = (source: NativePlaybackRuntimeSnapshot, key: 'playback.skipBackMs' | 'playback.skipForwardMs') => {
    if (source.dock?.local?.kind === 'audiobook') {
      const selected = audiobookPreferences(context.preferences());
      return (key === 'playback.skipBackMs' ? selected.skipBackMs : selected.skipForwardMs) / 1000;
    }
    const value = context.preferences().effective[key];
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) throw new Error('invalid_playback_preference');
    return value / 1000;
  };
  // The registry supplies the offered values; player edits stay with this run.
  // Settings remains the only owner of the persisted preference documents.
  const stylePort = (source: NativePlaybackRuntimeSnapshot): NativeWatchStylePort | undefined => {
    const fence = source.fence, appearance = source.subtitleStyle;
    if (!fence || !appearance || !context.playback.setSubtitleStyle) return undefined;
    const snapshot = context.preferences();
    const fields = nativeSubtitleStyleKeys.flatMap(key => {
      const field = snapshot.registry.fields.find(entry => entry.key === key);
      if (!field?.allowedValues?.length) return [];
      return [{key, label: defaultI18n.t(field.labelKey as Parameters<typeof defaultI18n.t>[0]), current: nativeSubtitleStyleValue(key, appearance),
        values: field.allowedValues.map(value => ({value: String(value), label: browseValueLabel(String(value))}))}];
    });
    if (!fields.length) return undefined;
    return {fields, apply: async (key: string, value: string) => {
      if (!current()) return;
      const field = context.preferences().registry.fields.find(entry => entry.key === key);
      if (!field?.allowedValues?.some(allowed => String(allowed) === value)) throw new Error('invalid_playback_preference');
      context.playback.setSubtitleStyle?.(fence, key, value);
    }};
  };
  const sleepPort = (): NonNullable<NativeWatchPanelsProps['sleep']> => {
    const at = context.playback.getSnapshot().playback.listeningDeadlines?.sleepAtMs ?? null;
    return {minutes: at === null ? null : Math.max(0, Math.round((at - Date.now()) / 60_000)),
      set: async (fence, minutes) => {await context.playback.setSleepTimer?.(fence, minutes);}};
  };
  const noticePreferences = {
    subscribe: context.subscribePreferences,
    getSnapshot: () => {
      if (!current()) return false;
      try {return nativeDeviceSettings(context).read('lossNotices') === true;} catch (error) {
        if (error && typeof error === 'object' && 'code' in error && error.code === 'unsupported') return true;
        throw error;
      }
    },
  };
  const failureRecovery = (failure: NativePlaybackFailure) => {
    const source = context.playback.getSnapshot(), route = context.navigation.getSnapshot();
    // A chosen version changes the failed phase and source itself. Its host
    // lifetime remains the original authority/route and expanded player.
    const hostCurrent = () => current() && sameAuthority(context.authority(), authority) && context.navigation.getSnapshot() === route && expanded();
    const active = () => {
      const live = context.playback.getSnapshot();
      return hostCurrent() && !live.retired && live.lease === source.lease && sameFence(live.fence, failure.fence) && live.playback.phase === 'error';
    };
    const entry = source.entry, failedVersion = source.session?.itemId === entry?.id ? source.session?.versionId : undefined;
    const versions = source.options?.itemId === entry?.id ? source.options?.versions : undefined;
    const alternate = active() && !!entry && entry.playback?.itemId === entry.id && failedVersion && bindings.openVersions && bindings.beginVersions && bindings.startVersion
      && versions && new Set(versions.map(version => version.id)).size === versions.length && versions.some(version => version.id !== failedVersion);
    const owner = () => active() && bindings.title.owner === true && context.session()?.viewer.role === 'owner';
    return {source, active, hostCurrent, owner, entry, failedVersion, alternate: !!alternate};
  };
  let postPlayOpening: Readonly<{fence: NativePlaybackFailure['fence']; route: ReturnType<typeof context.navigation.getSnapshot>; navigation: ReturnType<typeof nativePostPlayNavigation>}> | undefined;
  const player: NativeWatchHostBindings = {
    noticePreferences,
    liveGuide,
    chrome: (source, runtime) => ({...nativeWatchChromeFacts(source, bindings.names ?? createNativeWatchPanelNames(runtime.text)), skipBackSeconds: skipSeconds(source, 'playback.skipBackMs'),
      skipForwardSeconds: skipSeconds(source, 'playback.skipForwardMs'), reportError: runtime.reportError, artwork: runtime.artwork,
      onMinimize: fence => {if (current()) {const transition = context.playback.dock.collapse(minimizeTarget(context.playback.getSnapshot().dock, fence)); if (transition) void context.playback.onTransition(transition).catch(runtime.reportError);}},
      ...(context.playback.cast ? {onCast: (fence, anchorRef) => {if (current()) void context.playback.cast?.(fence, anchorRef).catch(runtime.reportError);}} : {}),
      ...(context.playback.goLive ? {goLive: fence => {if (current()) void context.playback.goLive!(fence).catch(runtime.reportError);}} : {}),
      liveGuide,
      ...(liveActions ? {liveActions} : {}),
      onGuide: liveGuide.guideAction(),
      ...(bindings.onPictureInPicture ? {onPictureInPicture: bindings.onPictureInPicture} : {}),
    }),
    panel: host => <NativeWatchPanels context={host} options={options} playback={context.playback} names={bindings.names ?? createNativeWatchPanelNames(host.runtime.text)} copy={copy}
      subtitleControls={fence => context.playback.subtitleControls?.(fence)} sleep={context.playback.setSleepTimer ? sleepPort() : undefined} style={stylePort(host.source)}
      reportError={host.runtime.reportError} renderError={bindings.title.renderError} playbackInfo={bindings.playbackInfo ?? (value => <NativePlaybackInfoPanel context={value} read={screens.readPlaybackInfo} renderError={bindings.title.renderError}/>)} episodes={bindings.episodes ?? screens.watch.episodes}/>,
    notice: (fence, lifetime) => screens.readPlaybackInfo(fence, playbackInfoWords(), lifetime).then(info => info?.notice),
    content: host => screens.watch.body(host),
    details: host => screens.watch.details(host),
    episodes: host => screens.watch.upNext(host),
    failureAction: (_host, failure) => {
      const recovery = failureRecovery(failure);
      return recovery.active() && (recovery.source.capabilities.retry === true && !!context.playback.retry || recovery.owner() || recovery.alternate);
    },
    failed: (host, failure, chromeVisible) => {
      const route = {authority: context.authority(), revision: context.navigation.getSnapshot().revision};
      const live = context.playback.getSnapshot();
      const canRetry = live.capabilities.retry === true && !!context.playback.retry && !!live.fence && sameFence(live.fence, failure.fence);
      const retry = canRetry ? () => {retryFailed(failure, route, host.runtime.reportError);} : undefined;
      if (!Platform.isTV) {
        const failedFence = live.fence, failedVersionId = live.session?.versionId;
        // Queue advancement can supply metadata without a card's playback target.
        // Recovery belongs to this failed session; the chosen version reads resume afresh.
        const entry = live.entry && failedFence?.kind === 'queue' && live.session?.kind === 'vod'
          && live.entry.id === failedFence.itemId && live.session.itemId === failedFence.itemId
          && live.session.id === failedFence.sessionId && failedVersionId === failedFence.sourceId
          ? {...live.entry, playback: {itemId: failedFence.itemId}} : undefined;
        const versionCurrent = () => {
          const source = context.playback.getSnapshot();
          return current() && expanded() && source.playback.phase === 'error' && !!source.fence && sameFence(source.fence, failure.fence)
            && source.session?.kind === 'vod' && source.session.id === failedFence?.sessionId
            && source.session.itemId === entry?.id && source.session.versionId === failedVersionId;
        };
        const canChooseVersion = !Platform.isTV && !!bindings.openVersions && !!bindings.beginVersions && !!bindings.startVersion
          && !!entry && !!failedVersionId && live.options?.itemId === entry.id
          && live.options.versions.some(version => version.id !== failedVersionId) && versionCurrent();
        const capture = canChooseVersion ? screens.capture(entry!, versionCurrent) : undefined;
        const differentVersion = capture ? () => {if (versionCurrent()) void screens.versions(capture, failedVersionId).catch(host.runtime.reportError);} : undefined;
        return <NativeHandheldPlaybackFailed failure={failure} retry={retry} differentVersion={differentVersion}/>;
      }
      const recovery = failureRecovery(failure);
      const differentVersion = recovery.alternate ? () => {
        if (!recovery.active() || !recovery.entry || !recovery.failedVersion) return;
        const captured = screens.capture(recovery.entry, recovery.hostCurrent);
        if (captured) void screens.versions(captured, recovery.failedVersion).catch(error => {if (recovery.active()) host.runtime.reportError(error);});
      } : undefined;
      const details = recovery.owner() ? () => {
        if (!recovery.owner()) return null;
        const facts: StateFact[] = [
          {id: 'reason', label: defaultI18n.t('next.states.whatHappened'), value: playbackUserMessage(recovery.source.playback.error)},
          {id: 'position', label: defaultI18n.t('player.position'), value: clockText(failure.positionSeconds)},
        ];
        const session = recovery.source.session, info = session?.id === failure.fence.sessionId ? session.playbackInfo : undefined;
        if (info) {
          const routes = {original: 'next.info.routes.original', repackaged: 'next.info.routes.repackaged', converted: 'next.info.routes.converted'} as const;
          facts.push({id: 'delivery', label: defaultI18n.t('next.states.sentAs'), value: defaultI18n.t(routes[info.route])});
          const network = playbackInfoWords().network(info.network);
          if (network) facts.push({id: 'network', label: defaultI18n.t('next.states.network'), value: network});
        }
        return <DetailsPanel audience="viewer" compact facts={facts} testID="watch-failed-facts"/>;
      } : undefined;
      return <NativePlaybackFailed context={host} failure={failure} chromeVisible={chromeVisible} retry={retry} details={details} differentVersion={differentVersion}/>;
    },
    postPlay: host => {
      if (!host.source.entry) return null;
      const route = context.navigation.getSnapshot();
      if (!postPlayOpening || !sameFence(postPlayOpening.fence, host.fence) || postPlayOpening.route !== route) postPlayOpening = {
        fence: host.fence, route, navigation: nativePostPlayNavigation(context, host.fence, current, bindings.title.navigate, host.runtime.reportError),
      };
      const navigation = postPlayOpening.navigation;
      return <NativeWatchPostPlay entry={host.source.entry} api={context.api} scope={detailScope} requestId={bindings.requestId} compact={!Platform.isTV}
        current={navigation.current} artwork={host.runtime.artwork} onReplay={() => {if (navigation.current()) void context.playback.replay(host.fence).catch(host.runtime.reportError);}}
        onClose={() => {if (navigation.current()) void context.playback.stop(host.fence).catch(host.runtime.reportError);}} onOpen={navigation.open}
        reportError={host.runtime.reportError}/>;
    },
    presentation: bindings.presentation,
  };
  const screens = createNativeWatchScreens({...bindings, api: context.api,
    scope: detailScope,
    authority, current: context.authority, isCurrent: current, subscribeCurrent: context.subscribeAuthority,
    navigation: context.navigation, playback: context.playback, watch: context.watch, isTV: Platform.isTV, player});
  const entryMenu: NativeWatchEntryMenu = Object.freeze({
    open: (entry, entryCurrent, anchorRef, select, extraActions) => current() ? screens.menu(entry, entryCurrent, anchorRef, select, extraActions) : Promise.resolve(),
    isOpen: entry => {
      if (!current()) return false;
      const actual = bindings.title.menu?.getSnapshot();
      return !!actual && actual.id === entry.id && actual.kind === entry.kind && actual.libraryId === entry.libraryId;
    },
    subscribe: listener => current() && bindings.title.menu ? bindings.title.menu.subscribe(listener) : () => {},
  });
  return Object.freeze({
    entryMenu,
    render: (runtime: NativeShellRuntime) => current() ? screens.watch.render(runtime) : null,
    title: screens.title,
    channel: {render: (route: import('../../shell/routes').NativeShellRoute) => current() && route.kind === 'channel'
      ? <NativeWatchChannelRoute key={JSON.stringify([route.sourceId, route.channelId])} context={context} route={route} title={bindings.title}/> : null},
    renderOverlays: (runtime: NativeShellRuntime) => current() ? <>{bindings.renderOverlays?.(runtime)}{liveActions && <NativeLiveActionPanels actions={liveActions} context={context} runtime={runtime}/>}</> : null,
    back: () => {
      if (!current()) return false;
      if (liveActions?.close()) return true;
      if (liveGuide?.close()) return true;
      const group = Platform.isTV ? groupPort() : undefined;
      if (group?.playerVisible?.() === false) return context.navigation.back(context.authority());
      return screens.watch.back() || group?.closePlayer?.() === true;
    },
    screens,
    dispose: () => {if (disposed) return; disposed = true; stopSystemMedia(); liveActions?.dispose(); liveGuide?.dispose(); abortAll(); stopPlayback(); stopGroup(); stopNavigation(); stopAuthority(); options.dispose(); screens.dispose(); bindings.presentation?.(undefined); bindings.dispose?.();},
  });
}
