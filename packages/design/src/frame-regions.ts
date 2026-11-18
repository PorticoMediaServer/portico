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
export const frameRegions = {
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
} as const satisfies Readonly<Record<string, readonly FrameRegion[]>>;
