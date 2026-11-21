import {useCallback, useEffect, useRef, useState} from 'react';
import type {ReadingPeriodicalGroup} from '../../../../../../packages/client-core/src';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {openNativeReadPeriodical} from '../../../screens/read/eligibility';
import type {NativeReadingPageOpening} from '../../../screens/read/reading-page-opening';
import {calendarDate} from '../../../../../../packages/i18n/src/calendar-date';

type Shelf = Readonly<{key: string; phase: 'loading' | 'ready' | 'error'; groups: readonly ReadingPeriodicalGroup[]; next?: string; more?: boolean}>;

/** A reading library's Magazines or Newspapers tab: its publications of that kind (the periodicals source), each with its
 * latest issue's cover and date and how many issues there are, a page at a time; a publication opens its archive. */
export function usePeriodicalShelf({context, runtime, libraryId, kind, opening}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime;
  libraryId: string; kind: 'magazine' | 'newspaper'; opening: NativeReadingPageOpening}) {
  const key = `${libraryId}:${kind}`, [shelf, setShelf] = useState<Shelf>(), flight = useRef<AbortController | undefined>(undefined);
  const read = useCallback((cursor?: string) => {
    const viewer = context.session()?.viewer;
    if (!viewer || !opening.isCurrent()) return;
    flight.current?.abort();
    const abort = flight.current = new AbortController();
    const current = () => !abort.signal.aborted && opening.isCurrent();
    setShelf(before => before?.key === key && cursor ? {...before, more: true} : {key, phase: 'loading', groups: []});
    void context.api.reading({viewer, isCurrent: current, signal: abort.signal}).periodicalGroups(libraryId, kind, {cursor, limit: 60}, abort.signal)
      .then(page => {if (current()) setShelf(before => ({key, phase: 'ready', groups: cursor && before?.key === key ? [...before.groups, ...page.groups] : page.groups, next: page.nextCursor}));},
        () => {if (current()) setShelf(before => before?.key === key && cursor ? {...before, more: false} : {key, phase: 'error', groups: []});});
  }, [context, libraryId, kind, opening, key]);
  useEffect(() => {read(); return () => flight.current?.abort();}, [read]);
  const state = shelf?.key === key ? shelf : {key, phase: 'loading' as const, groups: []};
  return {state, retry: () => read(), more: state.next && !state.more ? () => read(state.next) : undefined,
    cards: state.groups.map(group => ({key: group.id, title: group.name, cover: runtime.artwork(group.latest.item.coverUrl),
      caption: [{id: 'date', text: calendarDate(defaultI18n, group.latest.issueDate), role: 'identity' as const},
        {id: 'issues', text: defaultI18n.t('next.reader.issueCount', {count: group.issueCount}), role: 'identity' as const}],
      open: () => {void openNativeReadPeriodical(context, {libraryId, groupId: group.id, publicationType: kind}, opening.origin).catch(runtime.reportError);}}))};
}
