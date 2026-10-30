import type {Library} from '../../../../packages/client-core/src/index';
import type {SavedDefinition} from '../../../../packages/client-core/src/personal-saved';
import type {LibraryTabId} from '../../../../packages/client-core/src/presentation/library-tabs';
import type {IconName} from '../kit/Icon';
import {hiddenReadingLibraryOnTV, isReadingLibraryKind} from '../screens/read/platform';
import {tvCanvasFrame} from '../platform/layout/tv-canvas';

export type ShellScope = Readonly<{serverId: string; accountId: string; profileId: string}>;
/** `privacyFence` keys the shell (H-11): only who the viewer is or what they may see remounts it.
 * `viewerFence` also follows the library list and is updated in place for Home-data checks.
 * Older servers send no privacy fence, so their viewer fence keys the shell as before. */
export type ShellAuthority = ShellScope & Readonly<{epoch: string; viewerFence: string; privacyFence?: string}>;
export type ShellDestination = Readonly<
  | {kind: 'home' | 'libraries' | 'live' | 'saved' | 'settings'}
  /** An optional initial query seeds one ordinary Search route; the screen owns later edits. */
  | {kind: 'search'; query?: string}
  | {kind: 'library'; libraryId: string; /** An explicit authored opening pivot. */ tab?: LibraryTabId; /** A server-authored query, validated against this library's current capabilities. */ definition?: SavedDefinition; /** Scroll to the profile Mixes row once its data mounts. */ anchor?: 'mixes'}
>;
export type ShellTab = Readonly<{id: 'home' | 'search' | 'libraries' | 'live' | 'saved'; icon: IconName; message: 'web.shell.home' | 'web.shell.search' | 'pins.libraries' | 'search.chip.liveTV' | 'saved.title'}>;

export const scopeKey = (scope: ShellScope): string => JSON.stringify([scope.serverId, scope.accountId, scope.profileId]);
export const authorityKey = (scope: ShellAuthority): string => JSON.stringify([scope.serverId, scope.accountId, scope.profileId, scope.epoch, scope.privacyFence ?? scope.viewerFence]);
export const sameAuthority = (a: ShellAuthority, b: ShellAuthority): boolean => authorityKey(a) === authorityKey(b);

/** The host a signed-in session reached its server at: no scheme, credentials or path, and
 * absent rather than guessed when the session's address is not a URL. */
export const nativeServerHost = (baseUrl: string): string | undefined => {
  try {return new URL(baseUrl).host || undefined;} catch {return undefined;}
};

/** The API's user order is authoritative. This never sorts libraries by name or kind. */
export function visibleLibraries(libraries: readonly Library[], isTV: boolean): readonly Library[] {
  return isTV ? libraries.filter(library => !hiddenReadingLibraryOnTV(library.kind)) : libraries;
}

export function canOpenLibrary(libraries: readonly Library[], id: string, isTV: boolean): boolean {
  return visibleLibraries(libraries, isTV).some(library => library.id === id);
}

export function phoneTabs(hasLiveTV: boolean): readonly ShellTab[] {
  return [
    {id: 'home', icon: 'i-home', message: 'web.shell.home'},
    {id: 'search', icon: 'i-search', message: 'web.shell.search'},
    {id: 'libraries', icon: 'i-grid', message: 'pins.libraries'},
    ...(hasLiveTV ? [{id: 'live', icon: 'i-tv', message: 'search.chip.liveTV'} as const] : []),
    {id: 'saved', icon: 'i-bookmark', message: 'saved.title'},
  ];
}

export function libraryIcon(kind: string): IconName {
  if (kind === 'movie') return 'i-film';
  if (kind === 'tv') return 'i-tvlib';
  if (kind === 'music') return 'i-music';
  if (kind === 'audiobook' || kind === 'books' || isReadingLibraryKind(kind)) return 'i-book';
  return 'i-category';
}

/** First use and a removed/unauthorized remembered id open the overview, even with one library. */
/** phone-app 4d: the overview is the fallback for "no last library" only — the first time, or
 * when the remembered one was removed. A remembered library that is still visible opens, and
 * with a single visible library the tab opens straight into it instead of the overview. */
export function librariesDestination(remembered: string | undefined, libraries: readonly Library[], isTV: boolean): ShellDestination {
  const visible = visibleLibraries(libraries, isTV);
  if (remembered && visible.some(library => library.id === remembered)) return {kind: 'library', libraryId: remembered};
  return visible.length === 1 ? {kind: 'library', libraryId: visible[0].id} : {kind: 'libraries'};
}

export function selectedTab(destination: ShellDestination): ShellTab['id'] | undefined {
  return destination.kind === 'library' ? 'libraries' : destination.kind === 'settings' ? undefined : destination.kind;
}

/** The medium rail's drawn measurements (foldable1, tablet-kit .trail). */
export const mediumRailMetrics = Object.freeze({width: 84, itemWidth: 64, itemRadius: 14, iconSize: 22, gap: 6, top: 40, bottom: 16, shortHeight: 800, fadeHeight: 30});
/** A real inset never hides the first item under a notch or the last under the home bar. */
export function mediumRailPadding(topInset: number, bottomInset: number): Readonly<{top: number; bottom: number}> {
  return Object.freeze({top: Math.max(mediumRailMetrics.top, Math.max(0, topInset) + 16), bottom: Math.max(mediumRailMetrics.bottom, Math.max(0, bottomInset) + 8)});
}
/** foldable1 notes: Audiobooks takes the headphones glyph; Comics takes panels; Books keeps the open book. */
export function mediumLibraryIcon(kind: string): IconName {
  if (kind === 'audiobook') return 'i-headphones';
  if (kind === 'comic') return 'i-panels';
  return libraryIcon(kind);
}

export type ShellLayout = Readonly<{kind: 'phone' | 'tablet' | 'desktop' | 'tv'; panel: 'bottom' | 'side'; tabs: boolean; rail: boolean; scale: number; left: number; top: number; width: number; height: number}>;
/** TV is one letterboxed 1920x1080 coordinate system, never separately enlarged text/cards. */
export function shellLayout(width: number, height: number, isTV: boolean): ShellLayout {
  if (isTV) {
    return {kind: 'tv', panel: 'side', tabs: false, rail: false, ...tvCanvasFrame(width, height)};
  }
  // foldable1/tablet: 600-1023 uses the 84 pt rail, never the phone tab bar.
  // A phone in landscape is wider than 600 but short: phone-guide 2 draws the
  // phone chrome (tab bar, no rail) there, so a short window keeps the phone shell.
  const kind: ShellLayout['kind'] = width < 600 || width < 1024 && height < 600 ? 'phone' : width < 1024 ? 'tablet' : 'desktop';
  return {kind, panel: kind === 'phone' ? 'bottom' : 'side', tabs: kind === 'phone', rail: kind === 'tablet', scale: 1, left: 0, top: 0, width, height};
}

/** phone-guide 2 and phone-player 1 draw a landscape phone's `.tabbar.cmp`: 52 pt tall,
 * 6 pt above and 14 pt below, 120 pt at the sides, 20 pt icons and 12 pt labels in a row 7 pt apart. */
export const landscapePhoneBarMetrics = Object.freeze({height: 52, content: 38, paddingX: 120, paddingTop: 6, iconSize: 20, gap: 7, labelSize: 12, labelLineHeight: 16, drawnWidth: 844});
/** phone-guide 2's 120 pt side inset holds at the drawn 844 pt width, where the five tabs
 * keep their 120.8 pt columns. A narrower window gives up the inset before the tab columns,
 * and a real side inset always wins. */
export function landscapePhoneBarInset(width: number, tabCount: number, leftInset = 0, rightInset = 0): number {
  const drawnTab = (landscapePhoneBarMetrics.drawnWidth - 2 * landscapePhoneBarMetrics.paddingX) / 5;
  const available = Math.max(0, width - Math.max(0, leftInset) - Math.max(0, rightInset));
  return Math.max(Math.max(0, leftInset), Math.max(0, rightInset), Math.min(landscapePhoneBarMetrics.paddingX, (available - tabCount * drawnTab) / 2));
}
/** The 84-point drawn bar includes the ordinary bottom safe area. Larger insets extend it.
 * The drawn 52-point landscape bar already holds 14 pt below its row; a larger inset extends it,
 * so the home indicator never covers a tab. */
export const phoneBarHeight = (bottomInset: number, landscape = false): number => landscape
  ? Math.max(landscapePhoneBarMetrics.height, landscapePhoneBarMetrics.content + Math.max(0, bottomInset))
  : Math.max(84, 50 + Math.max(0, bottomInset));

/** phone-app.html draws54 points over the opening hero and46 after scrolling. */
export function phoneHeaderGeometry(topInset: number, scrolled: boolean): Readonly<{top: number; height: number}> {
  const top = Math.max(scrolled ? 46 : 54, Math.max(0, topInset));
  return {top, height: top + 50};
}
