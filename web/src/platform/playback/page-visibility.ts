import type {PageVisibility} from '@core/index.ts';

/**
 * The page's visibility for the playback service. Browsers defer loading media while a page is
 * hidden (another tab in front, a minimized window, a sleeping display), so a play started then
 * can't become ready until the page is shown; the service waits instead of failing it.
 */
export const documentVisibility: PageVisibility = {
  hidden: () => typeof document !== 'undefined' && document.visibilityState === 'hidden',
  onVisible(listener) {
    if (typeof document === 'undefined') return () => {};
    const changed = () => { if (document.visibilityState !== 'hidden') listener(); };
    document.addEventListener('visibilitychange', changed);
    return () => document.removeEventListener('visibilitychange', changed);
  },
  /** Every hide and show, so foreground-only deadlines pause with their remaining time. */
  onChange(listener) {
    if (typeof document === 'undefined') return () => {};
    const changed = () => listener();
    document.addEventListener('visibilitychange', changed);
    return () => document.removeEventListener('visibilitychange', changed);
  },
};
