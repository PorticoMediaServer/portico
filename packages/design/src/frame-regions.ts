/**
 * Each frame's required regions as data: one key per frame, each region's id (the frame's prop of that name in both
 * apps; a dotted id is a field of that prop), the forms that draw it and the mock-up frames it comes from (`design/ui-v1`
 * pages: `desktop-search s-all` is desktop-search.html's frame `s-all`, `phone-app 7` the seventh phone on phone-app.html;
 * or `shots/`). Both apps' frames take exactly these regions as props; a region a screen does not fill is absent, never a
 * blank. `flexible` marks the frame's flexible region. `drawnBy` names a region the frame keeps room for but another part
 * draws (the shell's bar, R1's start focus).
 *
 * One shape for every key: a list of regions. A NAMED USE is a kind of screen drawn on an existing frame (Saved on
 * `BrowseFrame`): its key lists the regions of that frame it fills, and its comment names the frame.
 *
 * Forms are frames.ts's (`phone`, `medium`, `expanded`, `tv`), plus `web` for the expanded form in a browser where it
 * differs from a tablet's, and `medium-split` for a `medium` window wider than tall (an open foldable on its side).
 */
import type {FrameForm} from './frames.ts';

export type RegionForm = FrameForm | 'web' | 'medium-split';
export type FrameRegion = Readonly<{id: string; forms: readonly RegionForm[]; from: readonly string[]; flexible?: true; drawnBy?: 'shell' | 'R1'}>;
const all: readonly FrameForm[] = ['phone', 'medium', 'expanded', 'tv'];
const touch: readonly FrameForm[] = ['phone', 'medium', 'expanded'];
const wide: readonly FrameForm[] = ['medium', 'expanded', 'tv'];
/** The tablet forms alone. */
const tablet: readonly FrameForm[] = ['medium', 'expanded'];
/** With the browser's own expanded form, where it differs from a tablet's. */
const allWeb: readonly RegionForm[] = ['phone', 'medium', 'expanded', 'web', 'tv'];
const touchWeb: readonly RegionForm[] = ['phone', 'medium', 'expanded', 'web'];
const tabletWeb: readonly RegionForm[] = ['medium', 'expanded', 'web'];

/** The title page's own form lists: an open foldable on its side (`medium-split`) is two pages there. */
const titleAll: readonly RegionForm[] = ['phone', 'medium', 'medium-split', 'expanded', 'tv'];
const titleTouch: readonly RegionForm[] = ['phone', 'medium', 'medium-split', 'expanded'];
/** Frame 2, the title page: a film, show, season, episode, person, collection, album, artist or book. */
const title = [
  {id: 'art', forms: titleAll, from: ['desktop-detail f1', 'phone-detail d1', 'tablet f6', 'tablet f8', 'foldable f2', 'detail-tv-1']},
  {id: 'cover', forms: titleAll, from: ['desktop-detail f7', 'desktop-listen album', 'desktop-listen artist', 'desktop-listen book', 'phone-detail d3 (7)', 'detail-tv-6']},
  {id: 'back', forms: titleTouch, from: ['phone-detail d1', 'tablet f6', 'tablet f8', 'foldable f2', 'desktop-detail f7 (web from 1024: a text link on a pushed page; the nav is the way back otherwise)']},
  {id: 'topAction', forms: titleTouch, from: ['phone-detail d1', 'tablet f6', 'tablet f8', 'foldable f2']},
  {id: 'kicker', forms: titleAll, from: ['desktop-detail f1', 'desktop-detail f7', 'desktop-detail f8', 'tablet f6', 'phone-detail d3', 'desktop-listen album', 'desktop-listen book', 'detail-tv-6']},
  {id: 'title', forms: titleAll, from: ['desktop-detail f1', 'phone-detail d1', 'detail-tv-1']},
  {id: 'metadata', forms: titleAll, from: ['desktop-detail f1', 'phone-detail d1', 'detail-tv-1']},
  {id: 'picker', forms: ['phone', 'medium', 'medium-split'], from: ['phone-detail d1 (1)', 'desktop-detail f1 notes: on the web and TV the choices are in the player']},
  {id: 'primary', forms: titleAll, from: ['desktop-detail f1', 'desktop-detail f2', 'phone-detail d1 (3)', 'detail-tv-2', 'desktop-detail f8', 'desktop-listen book']},
  {id: 'actions', forms: titleAll, from: ['desktop-detail f1', 'desktop-detail f2', 'tablet f6', 'phone-detail d1', 'detail-tv-2']},
  {id: 'tagline', forms: ['expanded', 'tv'], from: ['desktop-detail f1', 'tablet f6', 'detail-tv-4']},
  {id: 'synopsis', forms: titleAll, from: ['desktop-detail f1', 'phone-detail d1', 'desktop-detail f7 (Read more)', 'phone-detail d3 (Read more)']},
  {id: 'credits', forms: ['expanded', 'tv'], from: ['desktop-detail f1', 'tablet f6', 'detail-tv-1']},
  {id: 'cast', forms: ['medium-split'], from: ['foldable f2 (left page, under the synopsis)']},
  {id: 'tabs', forms: titleAll, from: ['desktop-detail f1', 'phone-detail d1', 'detail-tv-4', 'detail-tv-5a']},
  {id: 'collapsedBar', forms: titleAll, from: ['desktop-detail f3', 'desktop-detail f4', 'phone-detail d1 (2)', 'phone-detail d2 (4)', 'detail-tv-4', 'detail-tv-5a']},
  {id: 'panel', forms: titleAll, from: ['desktop-detail f1 to f8', 'phone-detail d1 to d3', 'detail-tv-3 to 7'], flexible: true},
  {id: 'aside', forms: titleAll, from: ['desktop-detail f8 (About)', 'desktop-listen album (Details, Files)', 'desktop-listen artist (Albums, Details)', 'desktop-listen book (About this book)']},
  {id: 'state', forms: titleAll, from: ['states-20', 'states-22b']},
] as const satisfies readonly FrameRegion[];

export type TitleRegion = typeof title[number]['id'];
export type TitleKind = 'film' | 'show' | 'season' | 'episode' | 'person' | 'collection' | 'album' | 'artist' | 'book';

/** Which regions each kind of title page fills. `panel` holds the kind's own body: rows (film), the Episodes tab
 * (show, season), the episode's credits and neighbours (episode), On this server (person), Films in order
 * (collection), the songs (album, artist), the chapters (book). */
export const titleKinds = {
  film: ['art', 'back', 'topAction', 'kicker', 'title', 'metadata', 'picker', 'primary', 'actions', 'tagline', 'synopsis', 'credits', 'tabs', 'collapsedBar', 'panel', 'state'],
  show: ['art', 'back', 'topAction', 'title', 'metadata', 'primary', 'actions', 'tagline', 'synopsis', 'credits', 'cast', 'tabs', 'collapsedBar', 'panel', 'state'],
  season: ['art', 'back', 'topAction', 'kicker', 'title', 'metadata', 'primary', 'actions', 'synopsis', 'tabs', 'collapsedBar', 'panel', 'state'],
  episode: ['art', 'back', 'topAction', 'kicker', 'title', 'metadata', 'picker', 'primary', 'actions', 'synopsis', 'credits', 'panel', 'state'],
  person: ['cover', 'back', 'kicker', 'title', 'metadata', 'synopsis', 'panel', 'state'],
  collection: ['art', 'back', 'topAction', 'kicker', 'title', 'metadata', 'primary', 'actions', 'synopsis', 'panel', 'aside', 'state'],
  album: ['cover', 'back', 'kicker', 'title', 'metadata', 'primary', 'actions', 'panel', 'aside', 'state'],
  artist: ['cover', 'back', 'kicker', 'title', 'metadata', 'primary', 'actions', 'panel', 'aside', 'state'],
  book: ['cover', 'back', 'kicker', 'title', 'metadata', 'primary', 'actions', 'panel', 'aside', 'state'],
} as const satisfies Readonly<Record<TitleKind, readonly TitleRegion[]>>;

export const frameRegions = {
  title,
  /** `BrowseFrame` with a `header`: a library, and any page of rows or a grid under a title (See all, a collection's grid). */
  browse: [
    {id: 'header.title', forms: all, from: ['desktop-browse grid', 'phone-app 4, 5', 'tablet 3, 4, 5', 'final3-tv-08']},
    {id: 'header.switcher', forms: all, from: ['desktop-browse grid (the round chevron)', 'phone-app 4, 4b (the centred name)', 'tablet 4', 'final3-tv-08 (title ▾)']},
    {id: 'header.back', forms: touch, from: ['phone-app 4 (‹ Libraries)', 'collections-46, -47 (See all)']},
    {id: 'header.trailing', forms: ['medium', 'expanded'], from: ['tablet 3, 4 (Select)', 'desktop-listen music (Shuffle)']},
    {id: 'header.menu', forms: touch, from: ['phone-app 4c', 'desktop-browse grid (⋯)', 'tablet 3']},
    {id: 'header.intro', forms: touch, from: ['desktop-browse seeall (why these titles are here)', 'collections-46, -47']},
    {id: 'header.actions', forms: touch, from: ['desktop-browse seeall (Play, Shuffle)', 'collections-46, -47']},
    {id: 'header.tabs', forms: all, from: ['desktop-browse grid', 'phone-app 4', 'tablet 4', 'final3-tv-08', 'desktop-listen music (Discover / Artists / Albums / Songs / Genres)']},
    {id: 'toolbar', forms: all, from: ['desktop-browse grid, filters, sort', 'phone-app 5', 'tablet 4', 'final3-tv-08', 'tv-focus 18-21']},
    {id: 'filters', forms: all, from: ['desktop-browse filters (side panel)', 'phone-app 6 (sheet)', 'tablet 4 (docked), 5 (over the grid)', 'final3-tv-08 (side sheet)']},
    {id: 'grid', forms: all, flexible: true, from: ['desktop-browse grid, big, list, collections', 'phone-app 5', 'tablet 4, 5', 'final3-tv-08']},
    {id: 'alphabet', forms: touch, from: ['desktop-browse grid, big (rail and the scroll bubble)', 'phone-app 5', 'tablet 4']},
    {id: 'children', forms: all, flexible: true, from: ['phone-app 4 (Discover: Continue Watching, Recently added ›, Recommended)', 'desktop-browse recommended, seeall', 'final3-tv-08']},
    {id: 'state', forms: all, from: ['system-rules 12 (loading keeps the loaded page\'s boxes)', 'states-22b', 'desktop-browse empty']},
    {id: 'startFocus', forms: ['tv'], drawnBy: 'R1', from: ['tv-focus 18 (the first card; never the rail)']},
  ],
  /** `BrowseFrame` with a `hero`: Home. */
  home: [
    {id: 'bar', forms: ['phone'], drawnBy: 'shell', from: ['phone-app 1, 2 (wordmark, Cast, avatar; solid once scrolled)', 'final-mobileweb-1 frame 1']},
    {id: 'hero', forms: all, from: ['mock.html home', 'final-desktop-1 (pager thumbnails)', 'phone-app 1', 'tablet 1, 2', 'final3-tv-01 (follows focus; owner, 18 Nov)']},
    {id: 'hero.hints', forms: ['tv'], from: ['final3-tv-01 (OK Resume, Hold OK More options)']},
    {id: 'hero.focusedEntry', forms: ['tv'], from: ['final3-tv-01, tv-focus 2 (the hero shows the focused card)']},
    {id: 'children', forms: all, flexible: true, from: ['phone-app 1, 2 (Continue Watching, New on your server ›, On now, Continue Listening, Popular)', 'mock.html home', 'tablet 1, 2', 'final3-tv-01']},
    {id: 'state', forms: all, from: ['system-rules 12', 'states 4b']},
    {id: 'startFocus', forms: ['tv'], drawnBy: 'R1', from: ['owner, 18 Nov: the first Continue Watching card']},
  ],
  /** Frame 3, the Settings index (family E). */
  settingsList: [
    {id: 'bar', forms: ['phone'], from: ['phone-app 16', 'phone-web 9']},
    {id: 'search', forms: all, from: ['desktop-settings f1, f2', 'phone-app 16', 'tablet f12', 'tv-more 22']},
    {id: 'profile', forms: ['phone'], from: ['phone-app 16', 'phone-web 9']},
    {id: 'shortcuts', forms: ['phone'], from: ['phone-app 16', 'phone-web 9']},
    {id: 'list', forms: all, from: ['desktop-settings f1', 'phone-app 16, 17', 'tablet f12, f13'], flexible: true},
    {id: 'page', forms: wide, from: ['desktop-settings f1', 'tablet f12, f13']},
  ],
  /** Frame 4, one settings or owner page (family E). */
  settingsPage: [
    {id: 'back', forms: touch, from: ['phone-app 18-21', 'tablet SC-8 (a page shown alone)']},
    {id: 'title', forms: all, from: ['desktop-settings f1, f4-f10', 'phone-app 18']},
    {id: 'status', forms: all, from: ['desktop-settings f1 "✓ Saved"', 'tablet f12']},
    {id: 'actions', forms: touch, from: ['desktop-settings f5 "+ Add library"', 'owner-people "Invite someone", "Add account"', 'phone-app 21 "Mark all read"']},
    {id: 'server', forms: all, from: ['desktop-settings f4-f10 server strip', 'phone-app 19 server card']},
    {id: 'intro', forms: all, from: ['desktop-settings f1', 'phone-app 18']},
    {id: 'tabs', forms: all, from: ['owner-people Accounts / Invitations / Devices', 'owner-libraries General / Metadata', 'phone-app 21 Unread / All / Archived']},
    {id: 'sections', forms: all, from: ['desktop-settings f1-f10', 'phone-app 18-21'], flexible: true},
  ],
  /** Frame 5, a sheet, dialog, side panel or popover: its form comes from `overlayForms` (R9). */
  sheet: [
    {id: 'bar', forms: touch, from: ['phone-detail 9-11', 'live-owner 13a-c', 'tablet f4', 'tablet-play f2']},
    {id: 'subtitle', forms: touch, from: ['phone-detail 11 "File info  Dune: Part Two  Done"', 'tablet f9 "1979 · Movie"']},
    {id: 'rows', forms: all, from: ['phone-detail 9-12', 'live-owner 13a-c', 'select 22'], flexible: true},
    {id: 'foot', forms: all, from: ['live-owner 13a-c', 'select 22', 'tablet f4', 'desktop-detail f9b']},
  ],
  /** Frame 6, the pages before you're in (family E; TV sign-in is the TV builder's form, family E its content). */
  signIn: [
    {id: 'back', forms: touch, from: ['auth 5, 6', 'auth-4']},
    {id: 'wordmark', forms: touch, from: ['auth 4', 'system-frames 6']},
    {id: 'kicker', forms: all, from: ['auth-4 "Sign in to"', 'auth-6', 'auth 6']},
    {id: 'title', forms: all, from: ['auth 4-6', 'auth-4, 5, 6']},
    {id: 'lede', forms: all, from: ['auth 4-6']},
    {id: 'error', forms: all, from: ['auth 6', 'auth-6 locked out']},
    {id: 'form', forms: all, from: ['auth-4 chips, Show, links', 'auth-6 code boxes', 'auth-4b, 5, 5c hand-off', 'auth 5 found servers'], flexible: true},
    {id: 'terms', forms: touch, from: ['auth 4', 'system-frames 6']},
  ],
  /** Who's watching and the profile PIN (auth-13, 14, 15; desktop-search who). */
  whosWatching: [
    {id: 'server', forms: all, from: ['auth-13 "On Portico demo  Switch server"', 'tablet f14']},
    {id: 'title', forms: all, from: ['auth-13', 'auth-15']},
    {id: 'profiles', forms: all, from: ['auth-13', 'auth-15', 'desktop-search who'], flexible: true},
    {id: 'pin', forms: all, from: ['auth-14', 'auth-15', 'auth-17']},
  ],
  /** The account menu under the avatar (desktop-search menu, phone-app r7, account.html). */
  accountMenu: [
    {id: 'header', forms: all, from: ['desktop-search menu', 'tv-focus 9']},
    {id: 'profiles', forms: all, from: ['desktop-search menu "Switch to"']},
    {id: 'entries', forms: all, from: ['desktop-search menu', 'account.html'], flexible: true},
  ],
  /** The Notifications panel and page (desktop-search notif, phone-app 21). */
  notifications: [
    {id: 'bar', forms: all, from: ['desktop-search notif "Notifications  ✓ Mark all read"', 'phone-app 21']},
    {id: 'audience', forms: all, from: ['desktop-search notif For you / Server']},
    {id: 'views', forms: all, from: ['desktop-search notif Unread / All / Archived', 'phone-app 21']},
    {id: 'items', forms: all, from: ['desktop-search notif', 'phone-app 21'], flexible: true},
  ],
  /** Frame 9, the whole page can't load (family E). */
  pageState: [
    {id: 'icon', forms: all, from: ['states 3, 4a-4c, 5']},
    {id: 'title', forms: all, from: ['states 3, 4a-4c, 5']},
    {id: 'body', forms: all, from: ['states 3, 4a'], flexible: true},
    {id: 'retry', forms: all, from: ['states 3 "Trying again in 26 seconds"']},
    {id: 'downloads', forms: touch, from: ['states 4a "Your downloads still play"']},
    {id: 'actions', forms: all, from: ['states 3 "Try again now", Switch server']},
    {id: 'details', forms: all, from: ['states 3, 4c, 5 (owner Details)']},
    {id: 'help', forms: all, from: ['states 4a, 5']},
  ],
  /** `SearchFrame` (family F: the kinds of screen that had no frame). */
  search: [
    {id: 'title', forms: ['phone'], from: ['phone-app 7']},
    {id: 'field', forms: all, from: ['desktop-search s-empty', 'phone-app 7', 'phone-app 8', 'tablet 10', 'final3-tv-09-search']},
    {id: 'cancel', forms: ['phone'], from: ['phone-app 8']},
    {id: 'keyboard', forms: ['tv'], from: ['final3-tv-09-search', 'tv-focus 22']},
    {id: 'scopes', forms: all, from: ['desktop-search s-all', 'desktop-search s-top', 'phone-app 8', 'tablet 10', 'final3-tv-09-search']},
    {id: 'notice', forms: touch, from: ['desktop-search s-top']},
    {id: 'none', forms: all, from: ['desktop-search s-none']},
    {id: 'results', forms: all, flexible: true, from: ['desktop-search s-empty', 'desktop-search s-all', 'desktop-search s-songs', 'desktop-search s-top', 'phone-app 7', 'phone-app 8', 'final3-tv-09-search']},
  ],
  /** `LiveFrame`: the guide, the channel list, Recordings and rules. */
  live: [
    {id: 'title', forms: all, from: ['desktop-live live-guide', 'phone-guide g1', 'tablet-play 6', 'final3-tv-10-live-guide']},
    {id: 'switcher', forms: all, from: ['desktop-live live-guide', 'phone-guide g1', 'final3-tv-10-live-guide']},
    {id: 'facts', forms: ['expanded'], from: ['desktop-live live-guide']},
    {id: 'actions', forms: all, from: ['phone-guide g1', 'tablet-play 6', 'final3-tv-10-live-guide']},
    {id: 'tabs', forms: tablet, from: ['desktop-live live-guide', 'desktop-live live-upcoming', 'tablet-play 6']},
    {id: 'chips', forms: all, from: ['desktop-live live-guide', 'phone-guide g1', 'tablet-play 6', 'final3-tv-10-live-guide']},
    {id: 'lead', forms: ['expanded'], from: ['desktop-live live-guide']},
    {id: 'toolbar', forms: touch, from: ['desktop-live live-guide', 'desktop-live live-upcoming', 'phone-guide g1', 'tablet-play 6']},
    {id: 'content', forms: all, flexible: true, from: ['desktop-live live-guide', 'desktop-live live-list', 'desktop-live live-upcoming', 'desktop-live live-recorded', 'desktop-live live-rules', 'phone-guide g1', 'phone-guide g2']},
    {id: 'details', forms: all, from: ['desktop-live live-sheet', 'desktop-live live-rules', 'phone-guide g3', 'tablet-play 6', 'tablet-play 7', 'final3-tv-10-live-guide']},
    {id: 'hints', forms: ['expanded', 'tv'], from: ['desktop-live live-guide']},
  ],
  /** `SplitFrame`: a page with a second pane. */
  split: [
    {id: 'page', forms: all, flexible: true, from: ['tablet-play 6', 'tablet 11b']},
    {id: 'pane', forms: all, from: ['desktop-live live-sheet', 'desktop-live live-rules', 'phone-guide g3', 'tablet-play 6', 'tablet-play 7', 'tablet 11b', 'final3-tv-10-live-guide']},
  ],
  /** `ReaderFrame` (web; native draws it inside the reading guest web view). */
  reader: [
    {id: 'bar', forms: touch, from: ['desktop-read reader-chrome', 'phone-read r5', 'tablet-read 4']},
    {id: 'tools', forms: touch, from: ['desktop-read reader-chrome', 'phone-read r5', 'tablet-read 4']},
    {id: 'page', forms: touch, flexible: true, from: ['desktop-read reader', 'desktop-read comic', 'desktop-read pdf', 'phone-read r5', 'tablet-read 5']},
    {id: 'turns', forms: ['expanded'], from: ['desktop-read reader-chrome']},
    {id: 'progress', forms: touch, from: ['desktop-read reader-chrome', 'phone-read r5']},
    {id: 'panel', forms: touch, from: ['desktop-read panel', 'desktop-read search', 'tablet-read 7', 'tablet-read 7b', 'phone-read r6']},
    {id: 'display', forms: touch, from: ['desktop-read aa', 'tablet-read 6', 'tablet-read 6b', 'phone-read r6']},
  ],
  /** Saved: a named use of `BrowseFrame`. */
  saved: [
    {id: 'header.title', forms: all, from: ['desktop-search saved', 'phone-app 9', 'tablet 11']},
    {id: 'header.tabs', forms: all, from: ['desktop-search saved', 'desktop-search history', 'tablet 11', 'collections-15-tv-saved-playlists']},
    {id: 'header.toolbar', forms: touch, from: ['desktop-search saved', 'tablet 11']},
    {id: 'grid', forms: all, flexible: true, from: ['desktop-search saved', 'tablet 11']},
  ],
  /** Downloads: a named use of `SplitFrame` with `BrowseFrame`. */
  downloads: [
    {id: 'header.title', forms: touch, from: ['collections-34-desktop-downloads', 'tablet 11b']},
    {id: 'header.tabs', forms: tablet, from: ['collections-34-desktop-downloads', 'tablet 11b']},
    {id: 'rows', forms: touch, flexible: true, from: ['collections-34-desktop-downloads', 'collections-36-mobile-web-downloads', 'tablet 11b']},
    {id: 'pane', forms: ['medium', 'expanded'], from: ['tablet 11b']},
  ],
  /** Music: a named use of `BrowseFrame`. */
  music: [
    {id: 'header.title', forms: all, from: ['final3-desktop-12-music-discover', 'desktop-listen music', 'phone-app 15a']},
    {id: 'header.switcher', forms: all, from: ['final3-desktop-12-music-discover']},
    {id: 'header.trailing', forms: touch, from: ['final3-desktop-12-music-discover']},
    {id: 'header.tabs', forms: all, from: ['final3-desktop-12-music-discover', 'desktop-listen songs']},
    {id: 'rows', forms: all, flexible: true, from: ['final3-desktop-12-music-discover', 'desktop-listen songs', 'phone-app 15a', 'final3-tv-11-music-card']},
  ],
  /** Audiobooks: a named use of `BrowseFrame`. */
  audiobooks: [
    {id: 'header.title', forms: all, from: ['desktop-listen books', 'phone-app 15c', 'listen-7-audiobooks-discover']},
    {id: 'header.switcher', forms: all, from: ['desktop-listen books']},
    {id: 'header.tabs', forms: all, from: ['desktop-listen books']},
    {id: 'rows', forms: all, flexible: true, from: ['desktop-listen books', 'phone-app 15c']},
  ],
  /** Frame 7, the video player (PlayerChromeFrame). Its parts are kit/player's. */
  player: [
    {id: 'picture', forms: allWeb, from: ['desktop-player f1', 'tablet-play f1', 'phone-player r1', 'tv-player p1']},
    {id: 'close', forms: touchWeb, from: ['desktop-player f1', 'phone-player r1']},
    {id: 'title', forms: allWeb, from: ['desktop-player f1 (episode line, "Paused at" f6)', 'desktop-live live-player', 'tv-player p11']},
    {id: 'tools', forms: allWeb, from: ['desktop-player f1 (Play on, More, Close)', 'phone-player r1 (Play on, PiP, options)', 'tablet-play f8 (Record, More, Close)']},
    {id: 'scrubber', forms: allWeb, from: ['desktop-player f1 (recap, intro, credits tinted)', 'tv-player p2 (filmstrip, chapter names)', 'phone-player 1b (preview)']},
    {id: 'times', forms: allWeb, from: ['desktop-player f1 (Ends 4:48 PM)', 'phone-player 1c (behind live)', 'tv-player p1 (clock)']},
    {id: 'transport', forms: allWeb, from: ['desktop-player f1', 'phone-player r1, r4', 'tv-player p1, p11 (channel steps)']},
    {id: 'leading', forms: allWeb, from: ['desktop-player f1 (Episodes)', 'desktop-live live-player (Guide)']},
    {id: 'trailing', forms: allWeb, from: ['desktop-player f1 (volume, subtitles, quality, speed, PiP, full screen)', 'phone-player r1 (Subtitles, Audio, quality pills)']},
    {id: 'prompts', forms: allWeb, from: ['desktop-player f1, f2s (Skip intro, Skip recap, Skipped + Undo)', 'phone-player 2, 2b', 'tablet-play f8 (Go to live)']},
    {id: 'card', forms: allWeb, from: ['desktop-player f4, f4s (Up next, Skip credits, scene note, countdown)', 'phone-player 6, 6b, 6c, 6e (Still watching?)', 'tv-player p5, p6']},
    {id: 'panel', forms: allWeb, from: ['desktop-player f3, f6 (options, Episodes drawer)', 'tablet-play f2 (beside)', 'phone-player 3 (sheet, side sheet)', 'tv-player p4 (in place)', 'desktop-live live-player (short guide)']},
    {id: 'hints', forms: ['tv'], from: ['tv-player p2', 'tv-player p4 (Back Close)']},
    {id: 'body', forms: ['phone', 'medium'], from: ['phone-player r4 (watch page)', 'tablet-play f3', 'phone-player 1c (channel page under an upright live picture)'], flexible: true},
  ],
  /** Frame 8, the audio player (AudioPlayerFrame). Its parts are kit/player's (queue.tsx on native, Queue.tsx on the web). */
  audio: [
    {id: 'close', forms: touchWeb, from: ['desktop-listen now', 'phone-app 14', 'tablet-play f9']},
    {id: 'context', forms: allWeb, from: ['desktop-listen now ("Playing from After Hours")', 'final3-tv-12']},
    {id: 'device', forms: touchWeb, from: ['desktop-listen now (top right)', 'tablet-play f10 (pill row)', 'phone-app 14 (bottom row)']},
    {id: 'more', forms: allWeb, from: ['desktop-listen now', 'final3-tv-12 (button row)']},
    {id: 'notice', forms: tabletWeb, from: ['desktop-listen book-player ("Went back 30 s")', 'tablet-play f12']},
    {id: 'artwork', forms: allWeb, from: ['desktop-listen now', 'phone-app 14, 15', 'final3-tv-12']},
    {id: 'title', forms: allWeb, from: ['desktop-listen now', 'final3-tv-12 (display line)']},
    {id: 'badge', forms: tabletWeb, from: ['desktop-listen book-player (Unabridged)']},
    {id: 'byline', forms: allWeb, from: ['desktop-listen now (artist, album)', 'book-player (author, Read by)', 'final3-tv-12 (album, year)']},
    {id: 'favourite', forms: touchWeb, from: ['desktop-listen now', 'phone-app 14']},
    {id: 'titleMore', forms: tabletWeb, from: ['desktop-listen now (More by the heart)', 'tablet-play f9']},
    {id: 'chapter', forms: allWeb, from: ['desktop-listen book-player', 'phone-app 15']},
    {id: 'scrubber', forms: allWeb, from: ['desktop-listen now', 'book-player (segmented by chapter)']},
    {id: 'times', forms: allWeb, from: ['desktop-listen now (at the ends)', 'phone-app 14, 15 (under)', 'final3-tv-12']},
    {id: 'transport', forms: allWeb, from: ['desktop-listen now (compact)', 'phone-app 14', 'final3-tv-12']},
    {id: 'volume', forms: ['web'], from: ['desktop-listen now', 'book-player']},
    {id: 'next', forms: ['phone'], from: ['phone-app 14']},
    {id: 'lyrics', forms: ['phone', 'medium', 'tv'], from: ['phone-app 14', 'tablet-play f10', 'final3-tv-12']},
    {id: 'queue', forms: ['phone', 'medium'], from: ['phone-app 14', 'tablet-play f10']},
    {id: 'playlist', forms: ['tv'], from: ['final3-tv-12 (Add to playlist)']},
    {id: 'speed', forms: allWeb, from: ['phone-app 15 (tile)', 'desktop-listen book-player (row of choices)']},
    {id: 'sleep', forms: allWeb, from: ['phone-app 15 (tile)', 'desktop-listen book-player (row of choices)']},
    {id: 'chapters', forms: ['phone', 'tv'], from: ['phone-app 15 (tile)']},
    {id: 'panel', forms: allWeb, from: ['desktop-listen now, now-states (Up next / Lyrics)', 'book-player (Chapters / Up next)', 'tablet-play f9, f10, f12', 'final3-tv-12', 'select-18 (phone sheet)'], flexible: true},
  ],
} as const satisfies Readonly<Record<string, readonly FrameRegion[]>>;
