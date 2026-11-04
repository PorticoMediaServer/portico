import type {LiveScreenSearch} from './route-search';

/** phone-app 12: Recordings is a sub-page of Live TV. Entering it pushes one history entry, marked as ours, so the
 * phone's system or swipe Back and the bar's "‹ Live TV" both return to the guide; switching views inside it
 * replaces that entry. Opened directly (a link, a reload), leaving replaces it with the guide. */
export type RecordingsView = 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage';
export type RecordingsNavigation = Readonly<{
  state: () => unknown;
  back: () => void;
  navigate: (options: {search: LiveScreenSearch; replace: boolean; resetScroll: false; state?: (previous: Record<string, unknown>) => Record<string, unknown>}) => Promise<unknown>;
}>;
const entry = 'liveRecordings';
export function recordingsEntered(state: unknown): boolean {
  return !!state && typeof state === 'object' && (state as Record<string, unknown>)[entry] === true;
}
export function enterRecordingsPage(nav: RecordingsNavigation, search: LiveScreenSearch, dvr: RecordingsView): Promise<unknown> {
  const inside = search.tab === 'recordings', marked = inside ? recordingsEntered(nav.state()) : true;
  return nav.navigate({search: {...search, tab: 'recordings', dvr}, replace: inside, resetScroll: false,
    state: previous => ({...previous, ...(marked ? {[entry]: true} : {})})});
}
export function leaveRecordingsPage(nav: RecordingsNavigation, search: LiveScreenSearch): Promise<unknown> {
  if (recordingsEntered(nav.state())) {nav.back(); return Promise.resolve();}
  const {dvr: _view, ...guide} = search;
  return nav.navigate({search: {...guide, tab: 'guide'}, replace: true, resetScroll: false});
}
