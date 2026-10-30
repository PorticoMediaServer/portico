import type {ShellDestination} from './model';

/** The held opener belongs to its captured page, including non-Home pages. */
export function canReturnToPage(captured: string, now: Readonly<{
  destination: ShellDestination; contentVisible?: boolean; homeVisible: boolean;
  homeObscured?: boolean; mediaExpanded?: boolean; selectionActive?: boolean;
}>, listening: boolean, overlay: boolean): boolean {
  return captured === JSON.stringify(now.destination) && now.contentVisible !== false
    && (now.destination.kind !== 'home' || now.homeVisible)
    && !now.homeObscured && !now.mediaExpanded && !now.selectionActive && !listening && !overlay;
}

/** Whether the page's own focus must be retained for its return: TV listening, the expanded
 * player, a hidden Home, or a route (title, book, collection) pushed over the page. */
export function holdsPageFocus(state: Readonly<{
  destination: ShellDestination; contentVisible?: boolean; homeVisible: boolean; mediaExpanded?: boolean;
}>, listening: boolean): boolean {
  return listening || !!state.mediaExpanded || state.contentVisible === false || state.destination.kind === 'home' && !state.homeVisible;
}
