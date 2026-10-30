import type {ReactNode, RefObject} from 'react';
import type {View} from 'react-native';
import type {Library} from '../../../../packages/client-core/src/index';
import type {HomeService} from '../../../../packages/client-core/src/home-service';
import type {HomeChannel, HomeCollection, HomeRow, HomeSeeAll} from '../../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../../packages/client-core/src/library-content';
import type {TextEnvironment} from '../../../../packages/client-core/src/presentation/kit-model';
import type {I18n} from '../../../../packages/i18n/src';
import type {ArtworkSource, AmbientArtSource, ProfileOption} from '../kit/artwork';
import type {AccountMenuProps} from '../kit/shell/AccountShell';
import type {LibraryMenuProps} from '../kit/browse';
import type {LayoutSelection} from '../kit/layout/types';
import type {StateViewProps} from '../kit/states';
import type {ErrorContext} from '../../../../packages/client-core/src/presentation/errors';
import type {ShellAuthority, ShellDestination} from './model';
import type {NativeShellRoute} from './routes';
import type {LastLibraryMemory} from './library-memory';
import type {NativeShellPlaybackAdapter} from './playback';
import type {LyricsService, LyricsTarget} from '../../../../packages/client-core/src/lyrics';
import type {NativeWatchOutletPort} from './watch-outlet';
import type {NativePlayOn} from '../platform/play-on/controller';
import type {NativeListeningPlaylist} from './listening-playlist';
import type {NativeListeningQueuePolicy} from './listening-queue-policy';
import type {NativeListeningSongDetail} from './listening-song-detail';

type LibraryMenuWithoutSave<T> = T extends unknown ? Omit<T, 'onSaveView' | 'canSaveView'> : never;

export type NativeShellSnapshot = Readonly<{
  authority: ShellAuthority;
  destination: ShellDestination;
  /** The title's genuine library may differ from its retained origin page. */
  railDestination?: ShellDestination;
  /** A non-shell route retains its origin without making that page current. */
  contentVisible?: boolean;
  selectionActive: boolean;
  homeObscured: boolean;
  homeVisible: boolean;
  mediaExpanded: boolean;
  libraries: readonly Library[];
  /** The library directory did not answer: unknown, never empty (P2-61 HOME-RESILIENCE). */
  librariesUnavailable?: boolean;
  hasLiveTV: boolean;
  profile: ProfileOption;
  serverName?: string;
  unreadNotificationCount?: number;
  unreadNotifications: boolean;
  account: Pick<AccountMenuProps, 'accountLine' | 'actions' | 'profiles' | 'viewerRole' | 'serverAction'>;
}>;

export type NativeHomeActions = Readonly<{
  navigate: (entry: ContentEntry) => void;
  play: (entry: ContentEntry, target: NonNullable<ContentEntry['playback']>) => void;
  more: (entry: ContentEntry, rowId?: string, anchorRef?: RefObject<View | null>) => void;
  myList: (entry: ContentEntry, selected: boolean) => Promise<void>;
  channel: (channel: HomeChannel) => void;
  channelMore: (channel: HomeChannel, anchorRef?: RefObject<View | null>) => void;
  collectionMore: (collection: HomeCollection, rowId: string, anchorRef?: RefObject<View | null>) => void;
  seeAll: (destination: HomeSeeAll) => void;
  /** The real collection page, from a Home collection banner. */
  openCollection: (collection: HomeCollection) => void;
  customize: () => void;
}>;

/** Bootstrapped by the real session/transport owner, never by mock data or global credentials.
 * A new authority must replace the HomeService, selection and authenticated image bindings. */
/** Touch only: the native stack host's view of navigation. Television keeps one route layer. */
export type NativeTouchStacksPort = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => import('./routes').NativeTabStacks;
  /** A pushed page's own content; shell destinations are drawn by the shell. */
  renderRoute: (route: import('./routes').NativeShellRoute) => ReactNode;
  /** The system already popped `occurrence` (edge swipe). False: the stack must resync. */
  commitNativePop: (occurrence: string) => boolean;
  /** Tab bar or rail: another tab returns as it was left (`anyRoot`, or the same first screen). */
  selectTab: (destination: ShellDestination, anyRoot?: boolean) => void;
  /** The tab you're on again: its first screen, then its top. */
  reselectTab: (destination: ShellDestination) => void;
}>;

export interface NativeShellRuntime {
  /** Original bootstrap owner retirement, aborted before route/focus teardown.
   * Passive observers stop on it; this never grants actions or freshens a snapshot. */
  readonly retirementSignal?: AbortSignal;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeShellSnapshot;
  home: HomeService;
  memory: LastLibraryMemory;
  text: Omit<TextEnvironment, 't'> & Pick<I18n, 't'> & Readonly<{region?: I18n['region']}>;
  actions: NativeHomeActions;
  artwork: (path: string | undefined) => ArtworkSource | undefined;
  ambient: (path: string | undefined) => AmbientArtSource | undefined;
  selection: (rowId: string) => LayoutSelection & {isMenuOpen?: (id: string) => boolean};
  setHomeWindow: (row: HomeRow, sectionOrder: number, first: number, last: number, visible: boolean, physicalIds?: readonly string[], retainedIds?: readonly string[], visibleIds?: readonly string[]) => void;
  homeMenuRow?: (windowId: string) => HomeRow | undefined;
  renderSelection: () => ReactNode;
  renderRoutes: () => ReactNode;
  stacks?: NativeTouchStacksPort;
  /** Persistent watch chrome above the one native picture surface. */
  renderWatch?: () => ReactNode;
  /** The screen supplies Save view and its canSave rule from its own live draft.
   * The omit distributes so the owned/member menu union survives. */
  libraryMenu: (library: Library) => LibraryMenuWithoutSave<LibraryMenuProps>;
  /** Full browse, search and player screens stay with their later-phase owners. */
  renderDestination: (destination: ShellDestination, openLibrary: (id: string) => void) => ReactNode;
  renderHomeEmpty: () => ReactNode;
  /** Read the library directory again now (Home's Try again while it is unavailable). */
  retryLibraries?: () => void;
  /** The drawn Switch server action, when the installed source offers that route. */
  openServers?: (initialPanel?: 'consent-kind') => void;
  /** Sanctioned, already-known connection facts for a recovery panel: the server's display
   * name and the address this session is reaching it on. The address is neither proof of a
   * home/local route nor of a secure one - this client has no authoritative distinct producer
   * for either - so it is only ever labelled neutrally, and never inferred from its range. */
  connectionFacts?: Readonly<{serverName?: string; connectionAddress?: string}>;
  /** The shell-owned download sheet, opened by a title page's drawn Download action. */
  renderDownloads?: () => ReactNode;
  /** Existing shell sheet for retained changes in this exact selected scope. */
  renderPendingChanges?: () => ReactNode;
  /** Destinations name their own copy context; Home stays the default. */
  error: (error: unknown, retry: () => void, context?: ErrorContext) => StateViewProps;
  reportError: (error: unknown) => void;
  /** One quiet confirmation, such as "Link copied" (O-162). */
  notice?: (message: string) => void;
  /** Explicit consent only, scoped to the still-current original startup refusal. */
  reportPlaybackProblem?: (consent: Readonly<{signal: AbortSignal; current: () => boolean}>) => Promise<void>;
  onDestination: (destination: ShellDestination) => void;
  scrollToTop: (destination: ShellDestination) => void;
  registerScrollToTop: (destination: ShellDestination, handler: () => void) => () => void;
  /** Registered by the TV shell; assembly dispatches only after topmost holds/player. */
  registerTVRailBack?: (handler: () => boolean) => () => void;
  /** Destination-scoped; legacy Home callers omit destination. Other destinations may continue navigation Back when false. */
  registerTVPageBack?: (handler: () => boolean, destination?: ShellDestination) => () => void;
  /** Confirmed Appearance effects. Absent until the shell binds a preference snapshot;
   * a missing effect keeps the drawn default rather than inventing a value. */
  appearanceCardSizePercent?: 85 | 100 | 125;
  appearanceShowBackdrops?: boolean;
  onArtworkError: (error: unknown, entry: ContentEntry) => void;
  /** Resolve an authorized Music library, open Discover and target its profile Mixes row. */
  musicDiscover?: () => void;
  /** music-plus (MC-9): the profile's own Mixes row, read from /v1/music/mixes. Absent
   * until the shell binds a real API and authority. */
  mixes?: import('../screens/destinations/music/mixes').NativeMusicMixesPort;
  playback?: NativeShellPlaybackAdapter;
  /** The real playlist picker for the current listening song and its playback fence. */
  listeningPlaylist?: NativeListeningPlaylist;
  listeningQueuePolicy?: NativeListeningQueuePolicy;
  /** The current queue song's own detail/favorite, read from that song's library target. */
  listeningSongDetail?: NativeListeningSongDetail;
  /** The real pushed route and its revision, bound to the same navigation the routes use.
   * A guarded control captures this when its surface opens and retires when the page leaves. */
  navigation?: Readonly<{subscribe: (listener: () => void) => () => void;
    getSnapshot: () => Readonly<{route: NativeShellRoute; revision: number; authority: ShellAuthority}>}>;
  /** Present in the real bootstrap. Missing measurement withholds expanded
   * bounds; it does not retire the persistent player owner. */
  watch?: NativeWatchOutletPort;
  /** Play on and the Portico remote, when this device can send to another. */
  playOn?: NativePlayOn;
  /** The effective server preference, expressed in seconds by the assembly. */
  skipBackSeconds: number;
  /** Synced lyrics for one current song, with the viewer's own preference.
   * Absent when this build has no lyric source; the caller owns the service. */
  lyrics?: (target: LyricsTarget) => Readonly<{enabled: boolean; service: LyricsService}>;
  /** The viewer's own "Synced lyrics" choice, read live so a changed setting
   * removes the control without reopening the surface. */
  lyricsEnabled?: () => boolean;
  /** Direction-specific audiobook step; absent callers retain their old step. */
  skipForwardSeconds?: number;
  /** Only installed when a real device destination control exists. */
  castControl?: ReactNode;
  desktopActivity?: ReactNode;
}
