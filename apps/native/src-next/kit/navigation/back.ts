import {BackHandler} from 'react-native';

/**
 * Back has one listener (TV-FOCUS-REVIEW §5, step 3). React Native runs `hardwareBackPress` subscriptions
 * last-registered first, so a hidden page that subscribed later swallowed Back for the visible one (NT-1). Here a
 * handler says what it closes, and Back walks the scopes from the top of the screen down:
 *
 *   observer  sees every Back and never consumes it (a held seek stops)
 *   overlay   a menu, sheet, panel or dialog: the newest open first
 *   surface   a full-screen surface over the shell: the player, Now playing, a sign-in or recovery flow
 *   page      a step inside a page (a tab, a selected programme, docked filters); only while `active` says the
 *             page is the one on screen
 *   shell     the shell's own ladder: routes, the rail, the page's Back, Home
 *
 * Inside a scope the newest registration goes first. The first handler that returns true ends it; when none
 * does, the platform's own Back runs (leave the app, tvOS Menu to the system).
 */
export type BackScope = 'observer' | 'overlay' | 'surface' | 'page' | 'shell';
export type BackOptions = Readonly<{scope: BackScope; active?: () => boolean}>;
const scopes: readonly BackScope[] = ['observer', 'overlay', 'surface', 'page', 'shell'];
type Entry = Readonly<{handler: () => boolean | null | undefined; scope: BackScope; active?: () => boolean; sequence: number}>;

const entries: Entry[] = [];
let sequence = 0, subscription: {remove: () => void} | undefined;

/** The one dispatch, in scope order. Exported for tests and for a caller that must run the ladder itself. */
export function dispatchBack(): boolean {
  for (const scope of scopes) {
    const candidates = entries.filter(entry => entry.scope === scope).sort((a, b) => b.sequence - a.sequence);
    for (const entry of candidates) {
      if (!entries.includes(entry) || entry.active && !entry.active()) continue;
      if (entry.handler() === true && scope !== 'observer') return true;
    }
  }
  return false;
}

export function registerBack(handler: Entry['handler'], options: BackOptions): () => void {
  if (options.scope === 'page' && !options.active) throw new Error('A page Back step must say when its page is the one on screen.');
  const entry: Entry = {handler, scope: options.scope, active: options.active, sequence: ++sequence};
  entries.push(entry);
  subscription ??= BackHandler.addEventListener('hardwareBackPress', dispatchBack);
  return () => {
    const index = entries.indexOf(entry);
    if (index < 0) return;
    entries.splice(index, 1);
    if (!entries.length) {subscription?.remove(); subscription = undefined;}
  };
}

/** Tests: the registrations currently held. */
export const backRegistrations = (): number => entries.length;
