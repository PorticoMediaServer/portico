import React, {useSyncExternalStore} from 'react';
import {shareLinkActions} from '../share/share-actions';
import type {View} from 'react-native';
import type {HttpLocalApi} from '../../../../../packages/client-core/src';
import {DetailService, type DetailProjection, type DetailScope, type PersonalIntent} from '../../../../../packages/client-core/src/detail';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {ManageAction} from '../../../../../packages/client-core/src/presentation/kit-model';
import {PlaybackOptionsClient} from '../../../../../packages/client-core/src/playback-v1/options';
import {setContinueDismissed} from '../../../../../packages/client-core/src/continue-watching';
import {localV1Http} from '../../../../../packages/client-core/src/playback-v1/local-http';
import type {PlaybackOptions} from '../../../../../packages/client-core/src/playback-v1/types';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import type {SegmentInput} from '../../../../../packages/client-core/src/playback-v1/queue';
import type {MusicPlaylistPickerHandle} from '../destinations/music/MusicPlaylistPicker';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativePlaybackAdapter, NativePlaybackReadLifetime, NativePlaybackStart, NativePlaybackVersionSelection} from '../../platform/playback/types';
import {nativeFullWindowOrientation} from '../../platform/layout/full-window';
import type {PlaybackInfoWords} from '../../../../../packages/client-core/src/playback-info-projection';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import type {NativeRouteRenderer} from '../../shell/route-registry';
import type {NativeShellNavigation} from '../../shell/routes';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeWatchOutletPort} from '../../shell/watch-outlet';
import {NativeTitleController, type NativeTitlePageHost} from './title-controller';
import {NativeTitleStarts} from './title-starts';
import {NativeTitleScreen, type NativeTitleScreenProps} from './TitleScreen';
import {FileInfoSession, fileInfoUsesGroups} from '../../../../../packages/client-core/src/file-info-session';
import {NativeFileInfoPanel} from './FileInfoPanel';
import type {NativeFileInfoOwner} from './file-info';
import {readNativePlaybackInfo} from './playback-info';
import {nativeDiscTitleChoices, nativeVersionChoices, nativeVersionNames} from './version-options';
import {NativeVersionChooserController} from './version-destination';
import {NativeDiscTitlesController} from './disc-titles-controller';
import {prepareDiscTitle, readDiscTitles, type DiscTitles} from './disc-titles';
import {NativeWatchControls, type NativeWatchControlContext} from './controls';
import {NativeWatchChrome, type NativeWatchChromeProps} from './WatchChrome';
import {NativeWatchPicture, type NativeWatchPictureProps} from './picture-slot';
import {NativeWatchHost, type NativeWatchHostBindings, type NativeWatchHostContext} from './WatchHost';
import {NativeWatchEpisodes} from './EpisodePanel';
import type {MenuAction} from '../../kit/overlays/types';

export type NativeWatchCapture = Readonly<{
  entry: ContentEntry;
  authority: ShellAuthority;
  routeRevision: number;
  expectedFence: PlaybackFence | undefined;
  /** The actual entry owner's lifetime, including removal or a replaced row. */
  current: () => boolean;
}>;
export type NativeVersionStartSelection = NativePlaybackVersionSelection;
/** The real menu producers for the captured entry: one lazy detail read, its
 * personal actions, the server's own playback target and the title route. */
export type NativeWatchMenuEntry = Readonly<{
  current: () => DetailProjection | undefined;
  subscribe: (listener: () => void) => () => void;
  play?: () => Promise<void>;
  enqueue?: (placement: 'next' | 'end') => Promise<void>;
  addToPlaylist?: () => void;
  /** music-plus (MC-9): the server builds a radio queue from this song, album or artist. */
  startRadio?: () => Promise<void>;
  goToArtist?: () => void;
  mutate: (intent: PersonalIntent) => Promise<void>;
  details?: () => void;
  dispose: () => void;
}>;
export type NativeWatchMenuExtra = Pick<MenuAction, 'id' | 'label' | 'icon' | 'description' | 'disabled' | 'destructive' | 'tone' | 'onSelect'>;
export type NativeWatchMenuActions = Readonly<{fileInfo: (detail?: DetailProjection) => Promise<void>; differentVersion?: () => Promise<void>; select?: () => void; entry?: NativeWatchMenuEntry; extraActions?: readonly NativeWatchMenuExtra[];
  /** Owner only, and only for a physical disc version the server measured. */
  discTitles?: () => void}>;
export type NativeVersionDestination = NativeVersionChooserController;
/** One Versions opening, from the menu action or from a failed output's own entry.
 * Its identity outlives any overlay object, so awaited inventory cannot adopt itself
 * into a sheet that was closed or replaced while it was reading. */
export type NativeVersionsOpening = Readonly<{capture: NativeWatchCapture; live: () => boolean}>;
export type NativeWatchFactoryPorts = Readonly<{
  api: HttpLocalApi;
  scope: DetailScope;
  authority: ShellAuthority;
  current: () => ShellAuthority;
  /** Genuine bootstrap lifetime, separate from the current playback output. */
  isCurrent?: () => boolean;
  subscribeCurrent: (listener: () => void) => () => void;
  navigation: Pick<NativeShellNavigation, 'getSnapshot' | 'subscribe' | 'back'>;
  playback: NativePlaybackAdapter;
  watch: NativeWatchOutletPort;
  /** Bound by the genuine assembly. Optional only for older descriptor fixtures. */
  player?: NativeWatchHostBindings;
  /** One authority-local playlist picker, hosted by the Watch module's overlay outlet. */
  playlistPicker?: MusicPlaylistPickerHandle;
  requestId: () => Promise<string>;
  isTV: boolean;
  /** Bound to Android's real fenced runtime command after it lands on MAIN. */
  startVersion?: (input: NativePlaybackStart, selection: NativeVersionStartSelection) => Promise<void>;
  manageFile: (capture: NativeWatchCapture, action: ManageAction['id'], fileId?: string) => void;
  openFileInfo: (capture: NativeWatchCapture, content: React.ReactNode, owner: NativeFileInfoOwner) => void;
  /** The opening went stale (the title changed): refresh the title and open File info again. */
  reopenFileInfo?: (capture: NativeWatchCapture) => void;
  /** The bound presenter follows the phone sheet / TV panel flow in ruling 21. */
  openVersions?: (capture: NativeWatchCapture, destination: NativeVersionDestination, opening: NativeVersionsOpening) => void;
  /** Created where Versions is dispatched, before any inventory is read. */
  beginVersions?: (capture: NativeWatchCapture) => NativeVersionsOpening | undefined;
  openDiscTitles?: (capture: NativeWatchCapture, dispatch: () => void, create: (opening: () => boolean) => NativeDiscTitlesController) => void;
  openMenu: (capture: NativeWatchCapture, actions: NativeWatchMenuActions, anchorRef?: React.RefObject<View | null>) => void;
  /** One quiet confirmation (O-162: "Link copied"). */
  notice?: (message: string) => void;
  title: Pick<NativeTitleScreenProps, 'aboutLabel' | 'renderError' | 'reportError' | 'navigate' | 'person' | 'owner' | 'openCollection' | 'serverName' | 'search'> & Readonly<{
    myList?: (entry: ContentEntry) => NativeTitleScreenProps['myList'];
    download?: NativeTitleScreenProps['download'];
    menu?: NativeTitleScreenProps['menu'];
    selection?: NativeTitleScreenProps['selection'];
  }>;
}>;
const titleKey = (entry: ContentEntry) => JSON.stringify([entry.libraryId, entry.kind, entry.id]);
/** The session names this item, so its version is the one on screen. */
const discsMatch = (session: {itemId?: string; versionId?: string} | undefined, itemId: string): session is {itemId: string; versionId: string} =>
  !!session?.itemId && session.itemId === itemId && !!session.versionId;
const sameOptionalFence = (a: PlaybackFence | undefined, b: PlaybackFence | undefined) => b ? sameFence(a, b) : !a;


/** Explicit descriptors and disposal for the genuine assembly; importing never registers a route. */
export function createNativeWatchScreens(ports: NativeWatchFactoryPorts) {
  const controllers = new Map<string, NativeTitleController>(), controllerSubscriptions = new Map<string, () => void>(), requests = new Map<AbortController, () => boolean>();
  const options = new PlaybackOptionsClient(localV1Http(ports.api)), controls = new NativeWatchControls(ports.isTV, undefined, nativeFullWindowOrientation, ports.title.reportError);
  // A refusal after the request was issued (iOS geometry) clears that exact owner and is
  // reported like any other playback-surface failure; a superseded owner is ignored.
  const offFullWindow = nativeFullWindowOrientation?.subscribeFailure((owner, message) => {
    if (controls.failFullWindow(owner)) ports.title.reportError(new Error(message));
  });
  const destinations = new Set<NativeVersionChooserController>();
  let disposed = false;
  let context: Omit<NativeWatchControlContext, 'expanded' | 'paused'> = {autoHide: true, hiddenByPanel: false, scrubbing: false, focusHeld: false};
  const alive = () => !disposed && ports.isCurrent?.() !== false && sameAuthority(ports.authority, ports.current());
  const starts = new NativeTitleStarts({current: () => alive() && sameAuthority(ports.navigation.getSnapshot().authority, ports.authority),
    revision: () => ports.navigation.getSnapshot().revision, start: ports.playback.start});
  const update = () => {
    starts.invalidate();
    for (const destination of destinations) if (!destination.isCurrent()) {destination.dispose(); destinations.delete(destination);}
    for (const [request, valid] of requests) if (!valid()) request.abort();
    const source = ports.playback.getSnapshot();
    controls.route(ports.navigation.getSnapshot().revision);
    controls.source(alive() && sameAuthority(source.authority, ports.authority) && !source.retired ? source.fence : undefined,
      {...context, expanded: source.dock.local?.expanded === true, paused: source.playback.intent === 'paused'});
  };
  const hostCurrent = (capture: NativeWatchCapture) => {
    const route = ports.navigation.getSnapshot();
    return alive() && sameAuthority(capture.authority, ports.current()) && sameAuthority(route.authority, capture.authority)
      && route.revision === capture.routeRevision && capture.current();
  };
  const current = (capture: NativeWatchCapture) => hostCurrent(capture) && sameOptionalFence(ports.playback.getSnapshot().fence, capture.expectedFence);
  const capture = (entry: ContentEntry, entryCurrent: () => boolean): NativeWatchCapture | undefined => {
    const value = Object.freeze({entry, current: entryCurrent, authority: ports.authority, routeRevision: ports.navigation.getSnapshot().revision, expectedFence: ports.playback.getSnapshot().fence});
    return current(value) ? value : undefined;
  };
  const request = async <T,>(captured: NativeWatchCapture | (() => boolean), run: (signal: AbortSignal) => Promise<T>, signal?: AbortSignal): Promise<T | undefined> => {
    const valid = typeof captured === 'function' ? captured : () => current(captured);
    if (signal?.aborted || !valid()) return;
    const controller = new AbortController(), abort = () => controller.abort(); requests.set(controller, valid);
    signal?.addEventListener('abort', abort, {once: true});
    try {const result = await run(controller.signal); return !controller.signal.aborted && valid() ? result : undefined;}
    finally {requests.delete(controller); signal?.removeEventListener('abort', abort);}
  };
  const readPlaybackInfo = (fence: PlaybackFence, words: PlaybackInfoWords, lifetime: NativePlaybackReadLifetime) => {
    const revision = ports.navigation.getSnapshot().revision;
    const valid = () => alive() && !lifetime.signal.aborted && lifetime.isCurrent()
      && sameAuthority(ports.navigation.getSnapshot().authority, ports.authority) && ports.navigation.getSnapshot().revision === revision;
    return request(valid, signal => readNativePlaybackInfo(ports.playback, fence, words, {signal, isCurrent: valid}), lifetime.signal);
  };
  const createTitleController = (key: string, entry: ContentEntry, body = false): NativeTitleController => {
    let owner: NativeTitleController;
    const memberCurrent = (member: ContentEntry) => {
      const state = owner.getSnapshot();
      return controllers.get(key) === owner && !state.retired && (titleKey(state.entry) === titleKey(member) || state.episodes.some(value => titleKey(value) === titleKey(member))
        || state.detail?.data?.related?.rows.some(row => row.entries.some(value => titleKey(value) === titleKey(member))) === true);
    };
    // The exact host this page lives under: the layout's own route revision for a
    // title page, or the playing session's fence for the watch body. A retained
    // page keeps its cache, but a host that has moved retires its live controls.
    const host = (): NativeTitlePageHost | undefined => {
      const nav = ports.navigation.getSnapshot(), source = ports.playback.getSnapshot();
      // The watch body's host is its own session: still expanded, still live, and
      // still playing the entry this page was created for.
      return body
        ? source.dock.local?.expanded === true && !source.retired && !!source.fence && !!source.entry && titleKey(source.entry) === titleKey(entry)
          ? {fence: source.fence} : undefined
        : nav.route.kind === 'title' && titleKey(nav.route.entry) === titleKey(entry) ? {revision: nav.revision} : undefined;
    };
    const subscribeHost = (listener: () => void) => {
      const offRoute = ports.navigation.subscribe(listener), offSource = ports.playback.subscribe(listener);
      return () => {offRoute(); offSource();};
    };
    owner = new NativeTitleController({...ports, entry, host, subscribeHost, playback: {start: async input => {
      if (host()) await starts.start(input, () => memberCurrent(input.entry));
    }}});
    controllers.set(key, owner); controllerSubscriptions.set(key, owner.subscribe(starts.invalidate));
    return owner;
  };
  /** File info opens from the accepted Detail the menu showed: its opening, never a newer one.
   * The first window of versions is read before the panel shows, as the whole read was before. */
  const fileInfo = async (captured: NativeWatchCapture, detail?: DetailProjection): Promise<void> => {
    if (!captured.entry.libraryId || !current(captured)) return;
    // The title page's live Detail is the newest accepted one; a later accepted Detail is a new opening.
    const live = [...controllers.values()].find(controller => titleKey(controller.entry) === titleKey(captured.entry))?.getSnapshot().detail?.data ?? undefined;
    if (live?.item.id === captured.entry.id && live.fileInfoOpening) detail = live;
    const token = detail?.item.id === captured.entry.id ? detail.fileInfoOpening : undefined;
    // A Detail that is not ready yet carries no opening; reading it again will.
    if (!detail || !token) throw Object.assign(new Error('file_info_unavailable'), {code: 'metadata_unavailable', retryable: true});
    const session = new FileInfoSession({api: ports.api, opening: {token, scope: ports.scope, target: {libraryId: detail.scope.libraryId, itemId: detail.scope.itemId}, revision: detail.revision},
      isCurrent: () => current(captured)});
    try {
      // Playable items read versions; any other kind reads its flat files.
      const read = await request(captured, async () => {await (fileInfoUsesGroups(captured.entry.kind) ? session.groups() : session.files()); return true;});
      const groups = fileInfoUsesGroups(captured.entry.kind) ? session.getSnapshot().groups : session.getSnapshot().files!;
      if (!read || !current(captured)) {session.retire(); return;}
      if (groups.status === 'failed') throw groups.error;
    } catch (error) {session.retire(); throw error;}
    const changed = async () => {if (current(captured)) await Promise.all([...controllers.values()].filter(controller => titleKey(controller.entry) === titleKey(captured.entry)).map(async controller => {
      const state = controller.getSnapshot();
      if (state.detail?.data) await controller.detail.refresh();
      if (state.workspace?.data) await controller.workspace.retry();
    }));};
    ports.openFileInfo(captured, <NativeFileInfoPanel session={session} kind={captured.entry.kind} text={{t: defaultI18n.t}} isTV={ports.isTV} identity={{series: captured.entry.show?.name,
      code: captured.entry.kind === 'episode' && captured.entry.seasonNumber !== undefined && captured.entry.episodeNumber !== undefined
        ? defaultI18n.t('next.kit.episodeCode', {season: captured.entry.seasonNumber, episode: captured.entry.episodeNumber}) : undefined}}
      onManage={(action, occurrence) => {if (current(captured)) ports.manageFile(captured, action, occurrence);}}
      onStale={() => {if (current(captured)) ports.reopenFileInfo?.(captured);}}/>, {session, kind: captured.entry.kind, changed});
  };
  // TV Versions lists a disc's own other titles from the viewer's inventory of that
  // physical version. Never a synthetic version, never the main title twice, and each
  // read is one at a time under the opening that asked for it. A refusal is reported
  // through the captured title presenter, never turned into a quietly empty chooser.
  const discDocuments = async (captured: NativeWatchCapture, offered: PlaybackOptions, opening: () => boolean): Promise<readonly DiscTitles[]> => {
    const drawn: DiscTitles[] = [];
    for (const version of offered.versions.filter(value => value.disc)) {
      if (!opening()) return drawn;
      try {
        const document = await request(captured, signal => readDiscTitles(ports.api, captured.entry.id, version.id, {signal, isCurrent: () => current(captured) && opening()}));
        if (document) drawn.push(document);
      } catch (error) {if (opening()) ports.title.reportError(error);}
    }
    return drawn;
  };
  const publishVersions = (captured: NativeWatchCapture, offered: PlaybackOptions, drawn: readonly DiscTitles[], opening: NativeVersionsOpening, failedVersionId?: string) => {
    const startVersion = ports.startVersion;
    if (!ports.openVersions || !startVersion || !opening.live() || offered.itemId !== captured.entry.id || new Set(offered.versions.map(version => version.id)).size !== offered.versions.length) return;
    const documents = new Map(ports.isTV ? drawn.map(document => [document.versionId, document] as const) : []);
    const rows = nativeVersionChoices(offered, captured.entry.id, nativeVersionNames(defaultI18n.t, defaultI18n.locale), failedVersionId).flatMap(row => {
      const document = documents.get(row.id);
      if (!document) return [row];
      // The disc's own row stays one row, but it names the main title it was drawn
      // with: choosing it starts that exact title, so an owner's change of main title
      // while this sheet is open refuses instead of quietly starting another one.
      const main = document.titles.find(title => title.main);
      const physical = main ? {...row, disc: Object.freeze({versionId: row.id, revision: document.revision, titleId: main.id,
        ...(main.revision === undefined ? {} : {titleRevision: main.revision})})} : row;
      return [physical, ...nativeDiscTitleChoices(document, row.id, defaultI18n.t)];
    });
    if (failedVersionId === undefined ? rows.length < 2 : !rows.length) return;
    const source = ports.playback.getSnapshot(), session = source.session?.itemId === captured.entry.id ? source.session : undefined;
    // Focus names only what the session actually is: an exact version id plus title
    // id and revision, or the version row when the session names no title. A title
    // this chooser has no row for claims nothing; the chooser then focuses a real row
    // without claiming it is the one playing.
    const versionId = session?.versionId, discTitle = session?.discTitle;
    const selected = !versionId ? undefined
      : discTitle ? rows.find(row => row.disc?.versionId === versionId && row.disc.titleId === discTitle.id
        && row.disc.titleRevision === discTitle.revision)?.id
        : rows.find(row => row.id === versionId)?.id;
    const destination = new NativeVersionChooserController({capture: captured, options: offered, rows, selectedRowId: selected,
      hostCurrent: () => hostCurrent(captured), sourceCurrent: () => current(captured), start: startVersion, opening: opening.live,
      prepare: (write, lifetime) => prepareDiscTitle(ports.api, write, lifetime),
      reread: (id, lifetime) => readDiscTitles(ports.api, captured.entry.id, id, lifetime),
      preview: (preview, signal) => options.options(captured.entry.id, {versionId: preview.versionId, discTitle: preview.discTitle}, signal),
      onDispose: () => {destinations.delete(destination);}});
    destinations.add(destination); ports.openVersions(captured, destination, opening);
  };
  const versions = async (captured: NativeWatchCapture, failedVersionId?: string): Promise<void> => {
    if (!ports.openVersions || !ports.startVersion || !ports.beginVersions || captured.entry.playback?.itemId !== captured.entry.id) return;
    if (failedVersionId !== undefined) {
      const source = ports.playback.getSnapshot();
      if (!current(captured) || source.playback.phase !== 'error' || source.session?.itemId !== captured.entry.id || source.session.versionId !== failedVersionId) return;
    }
    // The opening is captured before anything is read: closing the sheet or
    // presenting anything else while the inventory loads retires this one.
    const opening = ports.beginVersions(captured);
    if (!opening) return;
    const fresh = await request(captured, signal => options.options(captured.entry.id, {}, signal));
    if (!fresh || !opening.live()) return;
    const drawn = ports.isTV ? await discDocuments(captured, fresh, opening.live) : [];
    if (opening.live()) publishVersions(captured, fresh, drawn, opening, failedVersionId);
  };

  const menu = async (entry: ContentEntry, entryCurrent: () => boolean, anchorRef?: React.RefObject<View | null>, select?: () => void, extraActions?: readonly NativeWatchMenuExtra[]): Promise<void> => {
    const captured = capture(entry, entryCurrent);
    if (!captured) return;
    // A failed options read must not replace the independent real File info action.
    const offered = entry.playback?.itemId === entry.id ? await request(captured, signal => options.options(entry.id, {}, signal)).catch(error => {if (current(captured)) ports.title.reportError(error); return undefined;}) : undefined;
    if (!current(captured)) return;
    // TV only: a single physical disc version still opens Versions when its own titles
    // are real choices, because frames 16 and 18 put them there. The touch chooser's
    // eligibility and rows stay exactly as they were.
    const versionEligible = !!ports.openVersions && !!ports.startVersion && !!ports.beginVersions && !!offered && offered.itemId === entry.id
      && new Set(offered.versions.map(version => version.id)).size === offered.versions.length;
    const discChoices = versionEligible && ports.isTV && offered!.versions.some(version => version.disc);
    const drawn = discChoices ? await discDocuments(captured, offered!, () => current(captured)) : [];
    if (!current(captured)) return;
    const hasVersions = versionEligible && (offered!.versions.length >= 2 || discChoices
      && drawn.some(document => nativeDiscTitleChoices(document, document.versionId, defaultI18n.t).length > 0));
    // The entry's own live detail owns the personal rows, Play/Resume target and
    // Details route. An unreadable detail keeps the independent File info menu.
    let detail: DetailService | undefined;
    if (entry.libraryId) {
      const candidate = new DetailService({api: ports.api, scope: ports.scope, requestId: ports.requestId});
      try {await candidate.select({libraryId: entry.libraryId, itemId: entry.id});}
      catch {candidate.dispose();}
      if (!current(captured)) {candidate.dispose(); return;}
      if (!candidate.getSnapshot().data) candidate.dispose(); else detail = candidate;
    }
    const projection = detail?.getSnapshot().data;
    const primary = projection?.actions.find(action => action.enabled && action.playback && action.id !== 'start_over')?.playback;
    const route = ports.navigation.getSnapshot().route, onTitle = route.kind === 'title' && titleKey(route.entry) === titleKey(entry);
    // The queue append is offered only for a personal session; a group/follower
    // session never gains a personal append from this screen.
    const catalogue = ports.playback.cataloguePlayback?.();
    const groupSession = ports.playback.getSnapshot().session?.role === 'group_member';
    const segment = (value: ContentEntry): SegmentInput | undefined => ['show', 'season', 'album', 'artist', 'book', 'playlist'].includes(value.kind)
      ? {source: {container: {kind: value.kind, id: value.id}}}
      : ['movie', 'episode', 'song', 'audiobook_file', 'chapter'].includes(value.kind) ? {source: {items: {ids: [value.id]}}} : undefined;
    const queued = segment(entry);
    const menuEntry: NativeWatchMenuEntry | undefined = detail ? Object.freeze({
      current: () => detail!.getSnapshot().data ?? undefined,
      subscribe: listener => detail!.subscribe(listener),
      ...(primary ? {play: async () => {
        const operation = new AbortController();
        await ports.playback.start({entry, target: primary, ...(entry.kind === 'chapter' && primary.startSeconds !== undefined ? {startIntent: 'explicit' as const} : {}), lifetime: {signal: operation.signal, isCurrent: () => current(captured)}});
      }} : {}),
      ...(catalogue && ['song', 'album', 'artist'].includes(entry.kind) ? {startRadio: async () => {
        const operation = new AbortController();
        await catalogue.playSegments([{source: {container: {kind: 'radio', id: entry.id}}}], {signal: operation.signal, isCurrent: () => current(captured)});
      }} : {}),
      ...(catalogue && queued && !groupSession ? {enqueue: async (placement: 'next' | 'end') => {
        const operation = new AbortController();
        await catalogue.enqueueSegments([queued], placement, {signal: operation.signal, isCurrent: () => current(captured) && ports.playback.getSnapshot().session?.role !== 'group_member'});
      }} : {}),
      ...(entry.kind === 'song' && entry.artist ? {goToArtist: () => {
        if (current(captured)) ports.title.navigate({id: entry.artist!.id, kind: 'artist', title: entry.artist!.name, ...(entry.libraryId ? {libraryId: entry.libraryId} : {})});
      }} : {}),
      ...(ports.playlistPicker && ['movie', 'episode', 'song', 'audiobook_file'].includes(entry.kind) && entry.available !== false ? {addToPlaylist: () => {
        if (!current(captured)) return;
        const operation = new AbortController();
        ports.playlistPicker!.open(entry.id, {signal: operation.signal, isCurrent: () => current(captured)});
      }} : {}),
      mutate: async (intent: PersonalIntent) => {await detail!.mutate(intent);},
      ...(onTitle ? {} : {details: () => {if (current(captured)) ports.title.navigate(entry);}}),
      dispose: () => {detail?.dispose(); detail = undefined;},
    }) : undefined;
    if (detail && !current(captured)) {detail.dispose(); return;}
    // The owner's Titles row on TV only, for the physical version this playback is
    // actually on, or the one sole measured version when that is unambiguous. A first
    // measured disc is never assumed.
    const discSession = ports.playback.getSnapshot().session;
    const measured = (offered?.versions ?? []).filter(version => version.disc !== undefined);
    const discVersion = !ports.isTV || !ports.title.owner || !ports.openDiscTitles ? undefined
      : discsMatch(discSession, entry.id) ? measured.find(version => version.id === discSession!.versionId)
        : measured.length === 1 ? measured[0] : undefined;
    const discTitles: (() => void) | undefined = discVersion ? () => {
      if (!current(captured)) return;
      ports.openDiscTitles!(captured, discTitles!, opening => new NativeDiscTitlesController(ports.api,
        {itemId: entry.id, versionId: discVersion.id},
        {opening, owner: () => ports.title.owner === true, version: () => opening() ? discVersion.id : undefined}));
    } : undefined;
    ports.openMenu(captured, {fileInfo: accepted => fileInfo(captured, accepted), ...(discTitles ? {discTitles} : {}), ...(select ? {select: () => {if (current(captured)) select();}} : {}), ...(hasVersions ? {differentVersion: () => versions(captured)} : {}), ...(menuEntry ? {entry: menuEntry} : {}),
      ...(() => {
        // O-162: Share and Copy link for this item on this server, from client-core's one builder (touch only).
        const shared = ports.isTV ? [] : shareLinkActions(ports.current().serverId, 'item', {itemId: entry.id}, {notice: ports.notice, report: ports.title.reportError});
        const all = [...(extraActions ?? []), ...shared];
        return all.length ? {extraActions: all.map(action => ({...action, onSelect: () => {if (current(captured) && !('disabled' in action && action.disabled)) action.onSelect();}}))} : {};
      })()}, anchorRef);
  };
  const title: NativeRouteRenderer = {
    render: (route, runtime) => {
      if (!alive() || route.kind !== 'title') return null;
      const key = titleKey(route.entry), navigation = ports.navigation.getSnapshot();
      let controller = controllers.get(key);
      if (!controller) {
        controller = createTitleController(key, route.entry);
      }
      // The search and Remove Continue actions keep the title route and revision that
      // committed them; a retained callback can never act on a later route.
      const continueWatching = route.continueWatching;
      const removeContinue: NativeTitleScreenProps['removeContinue'] = continueWatching ? async callerSignal => {
        const captured = {route, revision: navigation.revision, authority: ports.authority}, lease = new AbortController();
        const stillCurrent = () => {
          const now = ports.navigation.getSnapshot();
          return alive() && sameAuthority(now.authority, captured.authority) && sameAuthority(captured.authority, ports.current())
            && now.route === captured.route && now.revision === captured.revision && !lease.signal.aborted && !callerSignal.aborted;
        };
        const assertCurrent = () => {if (!stillCurrent()) throw Object.assign(new Error('native_title_route_changed'), {name: 'AbortError'});};
        const stops = [ports.navigation.subscribe(() => {if (!stillCurrent()) lease.abort();}),
          ports.subscribeCurrent(() => {if (!stillCurrent()) lease.abort();})];
        const abortFromCaller = () => lease.abort();
        if (callerSignal.aborted) lease.abort(); else callerSignal.addEventListener('abort', abortFromCaller, {once: true});
        const signal = lease.signal;
        // Every transport — the revision read, a retry and the PUT — rechecks the exact route.
        const scoped = {request: async <T,>(path: string, method?: string, body?: unknown, requestSignal?: AbortSignal): Promise<T> => {
          assertCurrent();
          const result = await ports.api.request<T>(path, method, body, requestSignal);
          assertCurrent();
          return result;
        }};
        try {
          assertCurrent();
          await setContinueDismissed(scoped, {itemId: continueWatching.itemId, dismissed: true, revision: continueWatching.revision,
            operationId: ports.requestId, signal});
          assertCurrent();
          return true;
        } catch (error) {
          if (!stillCurrent()) return false;
          throw error;
        } finally {
          callerSignal.removeEventListener('abort', abortFromCaller);
          for (const stop of stops) stop();
        }
      } : undefined;
      const search: NativeTitleScreenProps['search'] = ports.title.search && navigation.route === route
        ? query => {
            const now = ports.navigation.getSnapshot();
            if (!alive() || !sameAuthority(now.authority, ports.authority) || now.route !== route || now.revision !== navigation.revision) return;
            ports.title.search!(query);
          }
        : undefined;
      // Play on is the shell's real play-on controller: it exists only while this
      // title route is current and this device holds a playback the shell can hand
      // off. A retained page never carries the live control, and no target list or
      // availability is invented here; the controller reads the server's receivers.
      // Rendering the control is only the first fence. The press itself rechecks the
      // captured route, revision, authority and lifetime, plus the live controller
      // and the playback's own cast capability, exactly as the search action does:
      // a retained handler can never open a chooser under a later route.
      const onPlayOn = navigation.route === route && runtime.playOn && ports.playback.getSnapshot().capabilities.cast
        ? (anchorRef?: React.RefObject<View | null>) => {
            const now = ports.navigation.getSnapshot();
            if (!alive() || !sameAuthority(now.authority, ports.authority) || now.route !== route || now.revision !== navigation.revision) return;
            if (!runtime.playOn || !ports.playback.getSnapshot().capabilities.cast) return;
            runtime.playOn.open(anchorRef);
          } : undefined;
      return <NativeTitleScreen authority={ports.authority} {...ports.title} search={search} removeContinue={removeContinue} onPlayOn={onPlayOn} key={key} controller={controller} text={runtime.text} artwork={runtime.artwork} ambient={runtime.ambient}
        back={() => {ports.navigation.back(ports.authority);}} more={(entry, anchorRef) => {
          const owner = controller!;
          void menu(entry, () => {
            const state = owner.getSnapshot();
            return controllers.get(key) === owner && !state.retired && (titleKey(state.entry) === titleKey(entry)
              || state.episodes.some(item => titleKey(item) === titleKey(entry))
              || state.detail?.data?.related?.rows.some(row => row.entries.some(item => titleKey(item) === titleKey(entry))) === true);
          }, anchorRef).catch(ports.title.reportError);
        }}
        myList={ports.title.myList?.(route.entry)} download={ports.title.download}/>;
    },
  };
  const offPlayback = ports.playback.subscribe(update), offAuthority = ports.subscribeCurrent(update), offRoute = ports.navigation.subscribe(update);
  update();
  const setPresentation = (value: typeof context) => {context = value; update();};
  /** Phone-player4's retained watch body, and foldable3g's two book pages: one
   * controller, one captured session, one section each side of the crease. */
  const watchPage = (host: NativeWatchHostContext, sections: 'all' | 'title' | 'episodes') => {
    const source = host.source, entry = source.entry, pane = sections === 'title' ? host.layout.details : host.layout.content;
    if (!alive() || ports.isTV || (host.layout.mode !== 'watch' && host.layout.mode !== 'book') || !pane || !entry?.libraryId || source.retired || host.fence.kind !== 'queue'
      || !sameFence(ports.playback.getSnapshot().fence, host.fence) || entry.id !== host.fence.itemId) return null;
    const key = `watch:${titleKey(entry)}`;
    let controller = controllers.get(key);
    if (!controller) controller = createTitleController(key, entry, true);
    const owner = controller;
    return <NativeTitleScreen authority={ports.authority} {...ports.title} key={`${key}:${sections}`} controller={owner} text={host.runtime.text} artwork={host.runtime.artwork} ambient={host.runtime.ambient}
      watch={{source, playback: ports.playback, width: pane.width, sections}} back={() => {ports.navigation.back(ports.authority);}}
      myList={ports.title.myList?.(entry)} download={ports.title.download} more={(member, anchorRef) => {
        void menu(member, () => {
          const state = owner.getSnapshot(), active = ports.playback.getSnapshot();
          return !state.retired && !active.retired && active.dock.local?.expanded === true && !!active.entry && titleKey(active.entry) === titleKey(entry)
            && (titleKey(state.entry) === titleKey(member) || state.episodes.some(value => titleKey(value) === titleKey(member))
            || state.detail?.data?.related?.rows.some(row => row.entries.some(value => titleKey(value) === titleKey(member))) === true);
        }, anchorRef).catch(ports.title.reportError);
      }}/>;
  };
  const episodeList = (host: NativeWatchHostContext, pane: boolean) => {
    const source = host.source, entry = source.entry;
    if (!alive() || source.retired || host.fence.kind !== 'queue' || entry?.kind !== 'episode' || !entry.show?.id || !entry.libraryId) return null;
    // The panel and the watch page share one retained show controller, so the
    // drawn Hide watched choice is the same fact on both surfaces.
    const key = `watch:${titleKey(entry)}`;
    let controller = controllers.get(key);
    if (!controller) controller = createTitleController(key, entry, true);
    return <NativeWatchEpisodes controller={controller} currentId={entry.id} playing={source.playback.intent === 'playing'} text={host.runtime.text} pane={pane}
      artwork={host.runtime.artwork} onClose={host.closePanel}
      onPlay={next => {
        if (!next.playback) return;
        const operation = new AbortController();
        void ports.playback.start({entry: next, target: next.playback,
          lifetime: {signal: operation.signal, isCurrent: () => alive() && ports.current() === ports.authority && host.fence.kind === 'queue'}}).catch(ports.title.reportError);
      }}/>;
  };
  return Object.freeze({
    title,
    watch: Object.freeze({render: (runtime: NativeShellRuntime) => alive() && ports.player
      ? <NativeWatchHost authority={ports.authority} current={ports.current} playback={ports.playback} outlet={ports.watch} runtime={runtime}
          controls={controls} bindings={ports.player} setPresentation={setPresentation} isTV={ports.isTV}/> : null,
      /** Phone-player4 reuses a real retained title body; foldable3g draws that
       * same real show page, episodes and all, on the right page beside the
       * playing picture. Its controller is separate from the underlying route's
       * memory and survives geometry. */
      body: (host: NativeWatchHostContext) => watchPage(host, host.layout.mode === 'book' ? 'episodes' : 'all'),
      /** foldable3g: the book posture's left page keeps the title block and its
       * actions; the right page carries the tabs and episodes. */
      details: (host: NativeWatchHostContext) => watchPage(host, 'title'),
      /** collections 37/38: the season's episodes inside the player, beside the
       * picture. The picture is the persistent surface; choosing an episode starts
       * that episode through the one playback adapter. */
      episodes: (host: NativeWatchHostContext) => episodeList(host, false),
      /** foldable4/5: the episodes that follow, beside the tabletop's controls. */
      upNext: (host: NativeWatchHostContext) => episodeList(host, true),
      /** Assembly calls this before its existing player/destination Back handling. */
      back: () => {const fence = ports.playback.getSnapshot().fence; if (!alive() || !fence) return false; const result = controls.back(fence); return result === 'panel' || result === 'controls' || result === 'full-window';}}),
    controls,
    menu,
    fileInfo,
    readPlaybackInfo,
    versions,
    capture,
    isCurrent: current,
    setPresentation,
    renderWatch: (props: Omit<NativeWatchChromeProps, 'controls' | 'playback' | 'source'>) => alive() ? <Watch {...props}/> : null,
    renderPictureSlot: (props: Omit<NativeWatchPictureProps, 'outlet' | 'authority'>) => alive() ? <NativeWatchPicture {...props} authority={ports.authority} outlet={ports.watch}/> : null,
    releaseTitle: (entry: ContentEntry) => {const key = titleKey(entry); controllerSubscriptions.get(key)?.(); controllerSubscriptions.delete(key); controllers.get(key)?.dispose(); controllers.delete(key); starts.invalidate();},
    /** Actual playback lifetime end only; never a dock/fold/full-window change. */
    releaseWatchBody: (entry: ContentEntry) => {const key = `watch:${titleKey(entry)}`; controllerSubscriptions.get(key)?.(); controllerSubscriptions.delete(key); controllers.get(key)?.dispose(); controllers.delete(key); starts.invalidate();},
    dispose: () => {if (disposed) return; disposed = true; for (const destination of destinations) destination.dispose(); destinations.clear(); starts.dispose(); for (const off of controllerSubscriptions.values()) off(); controllerSubscriptions.clear(); offPlayback(); offAuthority(); offRoute(); for (const request of requests.keys()) request.abort(); requests.clear(); for (const controller of controllers.values()) controller.dispose(); controllers.clear(); offFullWindow?.(); controls.dispose();},
  });
  function Watch(props: Omit<NativeWatchChromeProps, 'controls' | 'playback' | 'source'>) {
    const source = useSyncExternalStore(ports.playback.subscribe, ports.playback.getSnapshot);
    return alive() ? <NativeWatchChrome {...props} source={source} playback={ports.playback} controls={controls}/> : null;
  }
}
