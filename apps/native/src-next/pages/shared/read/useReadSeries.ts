import {useLayoutEffect, useMemo, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ReadingItem} from '../../../../../../packages/client-core/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRoute} from '../../../shell/routes';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {askNativeDownload} from '../../../screens/downloads/request';
import {useReadingActions} from './useReadingActions';
import {NativeReadingWindow} from '../../../screens/read/reading-window';
import type {NativeReadSeries} from '../../../screens/read/read-series-model';
import {nativePublicationReadsAvailable} from '../../../platform/offline-reading';

const {t} = defaultI18n;
export const readingVolumeKey = (item: ReadingItem) => `${item.id}:${item.sourceId}`;

export type ReadSeriesProps = {
  controller: NativeReadSeries; route: Extract<NativeShellRoute, {kind: 'read-series'}>; comic: boolean;
  context: NativeShellBootstrapContext; runtime: NativeShellRuntime; onBack: () => void;
};

export function useReadSeries({controller, route, comic, context, runtime}: ReadSeriesProps, phone: boolean) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const actions = useReadingActions(context, runtime, controller);
  useLayoutEffect(() => {
    if (!controller.arm()) return;
    void controller.load().catch(() => {});
    return () => controller.release();
  }, [controller, route]);
  const collection = useMemo(() => new NativeReadingWindow(() => {
    const value = controller.getSnapshot();
    return {items: value.items, cursor: value.nextCursor, loading: value.phase === 'loading' || !!value.morePending, error: value.moreError};
  }, controller.subscribe, controller.more, readingVolumeKey), [controller]);
  const continued = state.continued?.item, first = continued ?? state.items[0], overview = state.overview;
  const title = overview?.title ?? first?.series?.name ?? t('library.tab.series');
  const authors = [...new Set([overview?.author?.value, ...state.items.map(item => item.author?.value)])]
    .filter((author): author is string => !!author);
  const exactCount = !overview && state.phase === 'ready' && state.nextCursor === undefined ? state.items.length : undefined;
  const total = overview?.total ?? exactCount;
  const facts = [...(authors.length ? [defaultI18n.list(authors)] : []),
    ...(total !== undefined ? [t(comic ? 'next.reader.volumeCount' : 'next.kit.books', {count: total})] : [])];
  const count = overview ? t('next.reader.seriesReadCount', {read: defaultI18n.number(overview.completed), count: defaultI18n.number(overview.total)})
    : exactCount !== undefined ? defaultI18n.number(exactCount) : undefined;
  const progression = state.continued ? Math.max(0, Math.min(1, state.continued.progress.location.progression)) : undefined;
  const readable = continued?.inventoryAvailable === true ? continued : state.items.find(item => item.inventoryAvailable === true);
  const readLabel = readable && readable === continued ? t('next.cards.read')
    : comic && readable?.series?.number !== undefined ? t('next.reader.readVolume', {number: defaultI18n.number(readable.series.number, {maximumFractionDigits: 20})})
      : t(phone ? 'next.reader.readNext' : 'next.reader.read');
  const open = (item: ReadingItem) => {void controller.open(item).catch(runtime.reportError);};
  const offer = (item: ReadingItem) => {
    const lifetime = controller.itemLifetime(item);
    return lifetime ? {item, lifetime, open: () => open(item),
      ...(nativePublicationReadsAvailable() ? {download: () => askNativeDownload({title: item.title, itemId: item.id, reading: true, lifetime})} : {})} : undefined;
  };
  return {state, actions, collection, title, author: authors[0], facts, count, progression, continuedId: continued?.id, readable, readLabel,
    covers: state.items.length ? state.items.slice(0, 3) : first ? [first] : [], backdrop: runtime.ambient(first?.coverUrl), open, offer,
    retry: () => {void controller.retry().catch(runtime.reportError);},
    more: () => {void controller.more().catch(runtime.reportError);}};
}
