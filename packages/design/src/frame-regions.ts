/**
 * Each frame's required regions as data: one key per frame, each region's id, the forms that draw it and the mock-up
 * frames it comes from. Both apps' frames take exactly these regions as props; a region a screen does not fill is absent,
 * never a blank. A region marked `flexible` is the frame's one flexible region.
 */
import type {FrameForm} from './frames.ts';

export type FrameRegion = Readonly<{id: string; forms: readonly FrameForm[]; from: readonly string[]; flexible?: true}>;
const all: readonly FrameForm[] = ['phone', 'medium', 'expanded', 'tv'];
const touch: readonly FrameForm[] = ['phone', 'medium', 'expanded'];
const wide: readonly FrameForm[] = ['medium', 'expanded', 'tv'];

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
} as const satisfies Readonly<Record<string, readonly FrameRegion[]>>;
