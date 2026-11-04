/**
 * A TV page drawn as a native `Modal` over the whole shell (the refused-link page). The platform isolates it: an
 * Android dialog window owns key focus, and a tvOS presented view controller's focus environment excludes the
 * presenting hierarchy, so nothing under it can take D-pad focus. What the platform does not stop is a view under
 * it asking for focus in its own window (Android keeps that as the window's focused view, which it gets back when
 * the page closes). So while a page is held, the shell treats its base page, routes, player and listening screen
 * as covered (the `Covered` context only, never the native block: covering natively under a `Modal` would move
 * tvOS focus, D7), and their preferred asks wait.
 *
 * DS-427 (4 Nov amendment): a page that changed identity while covered (a profile switch, a new session) opens on
 * its own entry, not the old page's element. The shell provides its page identity; a hold captures it at open and
 * compares it at close.
 */
type Page = Readonly<{identity: () => string; enter: () => void}>;
let page: Page | undefined, holds = 0, open = false;
const listeners = new Set<() => void>();
const publish = () => {
  const next = holds > 0;
  if (next === open) return;
  open = next;
  for (const listener of [...listeners]) listener();
};

/** The shell's page: its identity now, and how it takes its own entry. */
export function provideTVPage(next: Page): () => void {
  page = next;
  return () => {if (page === next) page = undefined;};
}

export type TVPageModalHold = Readonly<{
  /** Whether the page under it is still the one it covered when it opened. */
  pageCurrent: () => boolean;
  /** Ends the hold. When the page under it changed, that page takes its own entry. Idempotent. */
  release: () => void;
}>;
export function holdTVPageModal(): TVPageModalHold {
  const opened = page?.identity();
  let released = false;
  holds++; publish();
  const pageCurrent = () => page?.identity() === opened;
  return {
    pageCurrent,
    release() {
      if (released) return;
      released = true;
      if (!pageCurrent()) page?.enter();
      holds--; publish();
    },
  };
}

/** Whether a TV page `Modal` is held now (a useSyncExternalStore source). */
export const tvPageModal = Object.freeze({
  subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
  getSnapshot: () => open,
});
