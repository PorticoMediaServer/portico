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
 * is resolved against whoever is signed in then. My List isn't indexed yet: it has no read here,
 * and the index must not add one. */
export function bindAppleActivity(owner: NativeAppleActivityOwner, port: NativeAppleActivityPort, reportError: (error: unknown) => void): () => void {
  let live = true, indexed: string | undefined, advertised: string | undefined;
  const guarded = (run: () => void) => {if (!live || !owner.current()) return; try {run();} catch (error) {reportError(error);}};
  const index = () => guarded(() => {
    const document = owner.home();
    if (!document) return;
    const row = continueRow(document.rows);
    const continueWatching = (row?.entries ?? []).flatMap(entry => {const title = activityTitle(entry); return title ? [title] : [];}).slice(0, 50);
    const key = JSON.stringify(continueWatching);
    if (key === indexed) return;
    indexed = key;
    port.indexTitles({continueWatching, saved: []});
  });
  const advertise = () => guarded(() => {
    const entry = owner.playing(), title = entry && activityTitle(entry);
    const key = title ? JSON.stringify(title) : undefined;
    if (key === advertised) return;
    advertised = key;
    port.setCurrent(title ?? null);
  });
  const offHome = owner.subscribeHome(index), offPlayback = owner.subscribePlayback(advertise);
  index(); advertise();
  return () => {
    if (!live) return;
    live = false; offHome(); offPlayback();
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
