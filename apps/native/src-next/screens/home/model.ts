import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {HomeDocument, HomeRow} from '../../../../../packages/client-core/src/home';
import {homeRowPresentation} from '../../../../../packages/client-core/src/presentation/home-spotlight';
import {cardText, type CardSubject, type TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';
import {contentEntrySubject} from '../../../../../packages/client-core/src/presentation/content-entry-subject';
import type {ShellAuthority} from '../../shell/model';

/** SIM-01: the canonical subject (date-based, absolute, air-date-only, special, seasonal). */
export const homeSubject: (entry: ContentEntry) => CardSubject = contentEntrySubject;

export const homeCardText = (entry: ContentEntry, continuation: boolean, env: TextEnvironment) => cardText(homeSubject(entry), continuation ? 'continuation' : 'ordinary', env);
export function authorizedHome(document: HomeDocument | undefined, authority: ShellAuthority): HomeDocument | undefined {
  return document?.serverId === authority.serverId && document.viewerFence === authority.viewerFence ? document : undefined;
}

/** Platform eligibility is server-authored; Read is additionally hidden from the TV shell. */
export function platformRows(document: HomeDocument, platform: 'phone' | 'tv', readLibraryIds: ReadonlySet<string>): readonly HomeRow[] {
  return document.rows.filter(row => (!row.platforms || row.platforms.includes(platform)) && (platform !== 'tv' || !row.libraryId || !readLibraryIds.has(row.libraryId)));
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

/** A continuation may be a show with an explicit server-selected episode target. */
export function homePlayTarget(row: HomeRow, entry: ContentEntry): ContentEntry | undefined {
  const target = row.resumes?.find(resume => resume.entryId === entry.id)?.target ?? entry;
  return target.playback && target.available !== false && !target.missing ? target : undefined;
}

export function initialHomeFocus(rows: readonly HomeRow[]): Readonly<{rowId: string; entryId: string}> | undefined {
  const row = rows.find(candidate => candidate.kind === 'continue' && candidate.entries.length) ?? rows.find(candidate => candidate.entries.length);
  return row && {rowId: row.id, entryId: row.entries[0].id};
}
