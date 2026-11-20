import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {HomeDocument, HomeRow} from '../../../../../packages/client-core/src/home';
import {homeRowPresentation} from '../../../../../packages/client-core/src/presentation/home-spotlight';
import {cardText, type TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';
import {homeCardText, homePlayTarget, homeSubject} from '../../../../../packages/client-core/src/presentation/home-card';
import type {ShellAuthority} from '../../shell/model';

// The Home card's words and play target live in client-core so the kit's Home rows can use them.
export {homeCardText, homePlayTarget, homeSubject};
/** A Saved card names what the title is: "1982 · Movie", "Show · 1 season" (W2-A13). */
export const savedCardText = (entry: ContentEntry, env: TextEnvironment) => cardText(homeSubject(entry), 'saved', env);
/** A native search card: TV Search (tv.html f5) draws a show's year, with the season count as a
 *  tag on the top result only, so the row's own season count is not its caption here (W2-A4). */
export const nativeSearchCardText = (entry: ContentEntry, env: TextEnvironment) => homeCardText(entry.kind === 'show' ? {...entry, seasonCount: undefined} : entry, false, env);
/**
 * The Home document as this viewer's latest answer, or nothing. `authority.viewerFence` is a live getter that follows
 * the latest Home document, so this asks only "this server, and the latest answer", never "the same fence as before".
 * It stays because a TV start treats an admitted document with no rows as proof that Home is empty
 * (resolveTVHomeStart → canYieldTVHomeMenu moves focus to the rail or menu for good): a document from another viewer
 * must read as still waiting, never as empty (step 7 item 4).
 */
export function authorizedHome(document: HomeDocument | undefined, authority: ShellAuthority): HomeDocument | undefined {
  return document;
}

/** Platform eligibility is server-authored; Read is additionally hidden from the TV shell. */
export function platformRows(document: HomeDocument, platform: 'phone' | 'tv', readLibraryIds: ReadonlySet<string>): readonly HomeRow[] {
  return document.rows.filter(row => (!row.platforms || row.platforms.includes(platform)) && (platform !== 'tv' || !row.libraryId || !readLibraryIds.has(row.libraryId)))
    // TV reads no books or comics (their resume card draws nothing there), so Continue your books keeps only what TV can
    // resume: an empty slot left of the audiobook made the first Left scroll the row instead of reaching the rail (fault 18).
    .map(row => platform === 'tv' && row.id === 'continue_books' ? {...row, entries: row.entries.filter(entry => entry.kind !== 'ebook' && entry.kind !== 'comic')} : row);
}

export function pageAtVisibleEnd(row: HomeRow, last: number, preload = 3): boolean {
  const count = row.live?.channels.length ?? row.collection?.members.length ?? row.entries.length;
  return !!row.nextCursor && last >= Math.max(0, count - preload);
}

/** A drawn row needs something under its heading: cards, channels or a collection banner.
 * A heading over nothing is never drawn, and a cursor alone draws nothing to page from. */
export function homeRowHasContent(row: HomeRow): boolean {
  if (row.collection) return true;
  if (row.live) return row.live.channels.length > 0;
  return homeRowPresentation(row).cards.length > 0;
}

export function initialHomeFocus(rows: readonly HomeRow[]): Readonly<{rowId: string; entryId: string}> | undefined {
  const row = rows.find(candidate => candidate.kind === 'continue' && candidate.entries.length) ?? rows.find(candidate => candidate.entries.length);
  return row && {rowId: row.id, entryId: row.entries[0].id};
}
