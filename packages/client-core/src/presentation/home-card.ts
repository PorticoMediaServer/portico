import type {ContentEntry} from '../library-content.ts';
import type {HomeRow} from '../home.ts';
import {cardText, type CardSubject, type TextEnvironment} from './kit-model/index.ts';
import {contentEntrySubject} from './content-entry-subject.ts';

/** SIM-01: the canonical subject (date-based, absolute, air-date-only, special, seasonal). */
export const homeSubject: (entry: ContentEntry) => CardSubject = contentEntrySubject;

/** A Home card's words: a continuation ("S1 E2  41 min left") or an ordinary card ("2024"). */
export const homeCardText = (entry: ContentEntry, continuation: boolean, env: TextEnvironment) => cardText(homeSubject(entry), continuation ? 'continuation' : 'ordinary', env);

/** A continuation may be a show with an explicit server-selected episode target. */
export function homePlayTarget(row: HomeRow, entry: ContentEntry): ContentEntry | undefined {
  const target = row.resumes?.find(resume => resume.entryId === entry.id)?.target ?? entry;
  return target.playback && target.available !== false && !target.missing ? target : undefined;
}
