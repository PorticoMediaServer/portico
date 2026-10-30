import {useEffect, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {FactPart} from '@portico/client-core/kit-model';
import {readLibrarySummaries, type LibraryPublicationCount} from '@core/library-summary.ts';
import type {ShellArtwork} from '../../shell/artwork';
import {ReadLibraryWash} from './ReadLibraryWash';

const publicationPivot: Readonly<Record<string, string>> = {book: 'books', magazine: 'magazines', newspaper: 'newspapers'};
/** Each library's own first published count, worded as the drawn library list words it. */
const kindPivot: Readonly<Record<string, string>> = {movie: 'movies', show: 'shows', artist: 'artists', album: 'albums', song: 'songs', ebook: 'books', comic: 'comics', book: 'books'};
const libraryCountText = (counts: readonly Readonly<{kind: string; count: number; atLeast: boolean}>[]) => {
  const first = counts.find(row => row.count > 0);
  return first ? defaultI18n.t(first.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: first.count, pivot: kindPivot[first.kind] ?? first.kind}) : undefined;
};

export type ReadLibrarySummaryRead = Readonly<{
  /** Identity of the captured screen; a read for another capture is never reused. */
  context: unknown; libraryId: string; artwork: ShellArtwork;
  api: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
  signal: (input?: AbortSignal) => AbortSignal; isCurrent: () => boolean; reportError: (error: unknown) => void;
  /** Only a Reading library draws the wash and the classified counts. */
  enabled: boolean;
}>;

/**
 * desktop-read 1b, 2 and 2c draw three of the library's recent covers behind its header and its
 * classified publication counts under its name. The bounded library summary is the only place
 * both are published: a publication type it does not classify is absent, never derived from the
 * ebook total, and a library with no artwork draws no wash.
 */
export function useReadLibrarySummary({context, libraryId, artwork, api, signal, isCurrent, reportError, enabled}: ReadLibrarySummaryRead): Readonly<{wash?: ReactNode; counts: readonly FactPart[]; libraryCounts: readonly Readonly<{id: string; text: string}>[]}> {
  const [snapshot, setSnapshot] = useState<Readonly<{context: unknown; libraryId: string; covers: readonly string[]; publicationCounts: readonly LibraryPublicationCount[]; libraryCounts: readonly Readonly<{id: string; text: string}>[]}>>();
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    let active = true;
    void readLibrarySummaries(api, signal(controller.signal)).then(rows => {
      if (!active || !isCurrent()) return;
      const row = rows.find(candidate => candidate.id === libraryId);
      setSnapshot({context, libraryId, covers: row?.summary.coverUrls ?? [], publicationCounts: row?.summary.publicationCounts ?? [],
        libraryCounts: rows.flatMap(candidate => {const text = libraryCountText(candidate.summary.counts); return text ? [{id: candidate.id, text}] : [];})});
    }).catch(error => {if (active && isCurrent()) reportError(error);});
    return () => {active = false; controller.abort();};
  }, [context, libraryId, api, signal, isCurrent, reportError, enabled]);
  const current = enabled && snapshot && snapshot.context === context && snapshot.libraryId === libraryId ? snapshot : undefined;
  const wash = current && current.covers.length > 0
    ? <ReadLibraryWash covers={current.covers.flatMap((path, index) => {const cover = artwork.foreground(path, 760, 2 / 3, {group: libraryId, order: index});return cover ? [cover] : [];})}/>
    : undefined;
  const counts = (current?.publicationCounts ?? []).filter(row => row.count > 0).map(row => ({id: row.kind, role: 'context' as const,
    text: defaultI18n.t(row.atLeast ? 'lib.countAtLeast' : 'lib.count', {count: row.count, pivot: publicationPivot[row.kind] ?? row.kind})}));
  return {wash, counts, libraryCounts: current?.libraryCounts ?? []};
}
