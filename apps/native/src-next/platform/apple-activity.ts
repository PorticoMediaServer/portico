import {NativeModules, Platform} from 'react-native';
import type {ContentEntry} from '../../../../packages/client-core/src/library-content';
import type {HomeDocument, HomeRow} from '../../../../packages/client-core/src/home';

/** Native/PorticoActivity.m (iPhone and iPad): Handoff, Spotlight and Siri's title list. */
type Title = Readonly<{itemId: string; title: string; subtitle?: string}>;
export type NativeAppleActivityPort = Readonly<{
  setCurrent: (title: Title | null) => void;
  indexTitles: (lists: Readonly<{continueWatching: readonly Title[]; saved: readonly Title[]}>) => void;
  clear: () => void;
}>;
export type NativeAppleActivityOwner = Readonly<{
  current: () => boolean;
  /** The Home document this viewer already reads; the index never makes a read of its own. */
  home: () => HomeDocument | undefined;
  subscribeHome: (listener: () => void) => () => void;
  /** My List, through the one named read (client-core `readWatchlist`). */
  readSaved: (signal: AbortSignal) => Promise<readonly ContentEntry[]>;
  /** The title the player holds now, if any. */
  playing: () => ContentEntry | undefined;
  subscribePlayback: (listener: () => void) => () => void;
}>;

const continueRow = (rows: readonly HomeRow[]): HomeRow | undefined => {
  for (const row of rows) {
    if (row.id === 'continue') return row;
    const child = row.children && continueRow(row.children);
    if (child) return child;
  }
};

/** What Siri, Spotlight and Handoff call a title: the show for an episode (people ask for
 * "Slow Horses"), with the episode as its subtitle. */
export function activityTitle(entry: ContentEntry): Title | undefined {
  if (!/^[A-Za-z0-9_-]{1,128}$/.test(entry.id)) return;
  const own = entry.displayTitle || entry.title;
  if (entry.kind === 'episode' && entry.show?.name) return {itemId: entry.id, title: entry.show.name, ...(own ? {subtitle: own} : {})};
  return own ? {itemId: entry.id, title: own} : undefined;
}

/** N2-2: the signed-in iPhone or iPad feeds its own system surfaces. The playing title is the
 * Handoff activity; Home's Continue Watching is Siri's title list ("Play … in Portico", "Resume")
 * and Spotlight's results; everything is cleared when this viewer's runtime retires (sign-out,
 * profile switch). Continuing any of them opens `portico://item|play/{id}` (N5-1's intake), which
 * is resolved against whoever is signed in then. My List is read once at the start and again
 * whenever Home's document moves (which follows the viewer's own changes). */
export function bindAppleActivity(owner: NativeAppleActivityOwner, port: NativeAppleActivityPort, reportError: (error: unknown) => void): () => void {
  let live = true, indexed: string | undefined, advertised: string | undefined, saved: readonly Title[] = [], document: HomeDocument | undefined;
  let reading: AbortController | undefined;
  const guarded = (run: () => void) => {if (!live || !owner.current()) return; try {run();} catch (error) {reportError(error);}};
  const titles = (entries: readonly ContentEntry[]) => entries.flatMap(entry => {const title = activityTitle(entry); return title ? [title] : [];}).slice(0, 50);
  const index = () => guarded(() => {
    const home = owner.home();
    if (!home) return;
    const continueWatching = titles(continueRow(home.rows)?.entries ?? []);
    const key = JSON.stringify([continueWatching, saved]);
    if (key === indexed) return;
    indexed = key;
    port.indexTitles({continueWatching, saved});
  });
  const readSaved = () => {
    reading?.abort();
    const controller = reading = new AbortController();
    void owner.readSaved(controller.signal).then(entries => {
      if (controller.signal.aborted || !live || !owner.current()) return;
      saved = titles(entries); index();
    }, error => {if (!controller.signal.aborted && live && owner.current()) reportError(error);});
  };
  const homeChanged = () => {
    const next = owner.home();
    if (next && next !== document) {const first = !document; document = next; if (!first && live && owner.current()) readSaved();}
    index();
  };
  const advertise = () => guarded(() => {
    const entry = owner.playing(), title = entry && activityTitle(entry);
    const key = title ? JSON.stringify(title) : undefined;
    if (key === advertised) return;
    advertised = key;
    port.setCurrent(title ?? null);
  });
  const offHome = owner.subscribeHome(homeChanged), offPlayback = owner.subscribePlayback(advertise);
  if (owner.current()) readSaved();
  homeChanged(); advertise();
  return () => {
    if (!live) return;
    live = false; offHome(); offPlayback(); reading?.abort();
    try {port.clear();} catch (error) {reportError(error);}
  };
}

/** iPhone and iPad only; the module isn't in the Apple TV or Android builds. */
export function bindNativeAppleActivity(owner: NativeAppleActivityOwner, reportError: (error: unknown) => void): (() => void) | undefined {
  if (Platform.OS !== 'ios' || Platform.isTV || !owner.current()) return;
  const native = NativeModules.PorticoActivity as Partial<NativeAppleActivityPort> | undefined;
  if (typeof native?.setCurrent !== 'function' || typeof native.indexTitles !== 'function' || typeof native.clear !== 'function') return;
  return bindAppleActivity(owner, {setCurrent: native.setCurrent, indexTitles: native.indexTitles, clear: native.clear}, reportError);
}
