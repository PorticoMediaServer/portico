import {useState} from 'react';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';

/** W-12: Back to a music page starts on the card it opened (an album from an artist's page, or from an album's More like
 * this), not on Play. The music route draws a new page on Back, so what was opened is remembered here, keyed by the page's
 * own entry (library and id), and read once by the next page drawn for it. */
const opened = new Map<string, string>();

export type OpenedReturn = Readonly<{
  /** The card's key to start on, once, when this page is drawn again after Back. */
  returning?: string;
  /** Opens an entry, remembering it when it is one this page returns to. */
  openEntry: (value: ContentEntry) => void;
  /** This page's own key: stable across the page drawn again, for its rows' focus-region names (`focusKey`). */
  pageKey: string;
}>;

export function useOpenedReturn(entry: ContentEntry, openEntry: (value: ContentEntry) => void,
  remembers: (value: ContentEntry) => boolean = value => value.kind === 'album'): OpenedReturn {
  const pageKey = JSON.stringify([entry.libraryId ?? '', entry.id]);
  const [returning] = useState(() => {const id = opened.get(pageKey); opened.delete(pageKey); return id;});
  return {returning, pageKey, openEntry: value => {if (remembers(value)) opened.set(pageKey, value.id); openEntry(value);}};
}
