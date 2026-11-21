import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ReadingItem} from '../../../../../../packages/client-core/src';
import {AmbientArt, Artwork, missingArt} from '../../../kit/artwork';
import {PanelBlock, PanelHeading, posterGridScroller, StateRow, TitleFrame, titleRowsScroller} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {ReadingItemMenu, ReadingSelectionToolbar} from '../../../kit/reading/ReadingActions';
import {useTheme} from '../../../kit/theme';
import {readingVolumeKey, useReadSeries, type ReadSeriesProps} from '../../shared/read/useReadSeries';

const {t} = defaultI18n;

/** A book or comic series (collections 40 and 45, phone-read): `TitleFrame` with the first volume's cover, the author and
 * count, Read (or Read next, Read volume N) with the series' progress, then its books as rows, or a comic's volumes as a
 * grid, a window at a time as the page scrolls. */
export function ReadSeriesPage(props: ReadSeriesProps) {
  const {route, comic, runtime, onBack} = props, theme = useTheme();
  const view = useReadSeries(props, theme.platform === 'phone'), {state, actions} = view;
  const first = view.covers[0];
  // A comic says which way it reads (right to left for manga).
  const direction = comic && (state.direction === 'rtl' || state.direction === 'ltr')
    ? t(state.direction === 'rtl' ? 'next.reader.directionRtl' : 'next.reader.directionLtr') : undefined;
  const facts = [...view.facts, ...(direction ? [direction] : [])]
    .map((text, index) => ({id: `fact-${index}`, text, role: index ? 'context' as const : 'identity' as const}));
  const numberLabel = (item: ReadingItem) => item.series?.number === undefined ? undefined
    : t(comic ? 'next.reader.volumeNumber' : 'next.reader.bookNumber', {number: defaultI18n.number(item.series.number, {maximumFractionDigits: 20})});
  const progress = (item: ReadingItem) => item.id === view.continuedId ? view.progression : undefined;
  const menu = (item: ReadingItem) => {
    const offered = item.inventoryAvailable === true ? view.offer(item) : undefined;
    return offered ? (anchorRef: React.RefObject<import('react-native').View | null>) => actions.actions.show({...offered, anchorRef}) : undefined;
  };
  const selection = (item: ReadingItem) => {
    const offered = item.inventoryAvailable === true ? view.offer(item) : undefined;
    return offered ? actions.selection(offered) : undefined;
  };
  const open = (item: ReadingItem) => {if (item.inventoryAvailable === true) view.open(item);};
  const scroller = comic
    ? posterGridScroller({collection: view.collection, getKey: readingVolumeKey, testID: 'read-series-volumes', item: item => ({
      title: item.title, caption: numberLabel(item), artwork: runtime.artwork(item.coverUrl), progress: progress(item),
      onNavigate: () => open(item), onMore: menu(item), selection: selection(item)})})
    : titleRowsScroller({collection: view.collection, getKey: readingVolumeKey, testID: 'read-series-books', item: item => ({
      title: item.title, kicker: numberLabel(item), facts: item.author ? [item.author.value] : [],
      artwork: runtime.artwork(item.coverUrl), progress: progress(item), onNavigate: () => open(item), onMore: menu(item),
      selection: selection(item), testID: `read-series-book-${encodeURIComponent(item.id)}`})});
  const failure = state.phase === 'error' ? {kind: 'error' as const, icon: 'i-warn' as const, title: runtime.error(state.error, view.retry, 'library').title as string,
    action: {label: t('action.tryAgain'), onPress: view.retry}} : undefined;
  return <>
    <TitleFrame testID={`native-read-series-${route.seriesId}`}
      back={{label: t('action.back'), onPress: onBack, testID: 'read-series-back'}}
      art={<AmbientArt treatment="title" source={view.backdrop}/>}
      cover={{shape: 'poster', node: size => <Artwork source={runtime.artwork(first?.coverUrl)} width={size} height={size * 1.5}
        fallback={missingArt({title: view.title, author: view.author, shape: 'poster', width: size, height: size * 1.5})}/>}}
      kicker={{label: t('library.tab.series')}} title={view.title}
      metadata={<HeroMetadata facts={facts}/>}
      primary={{icon: 'i-book', label: view.readLabel, disabled: !view.readable, progress: view.progression,
        onPress: () => {if (view.readable) view.open(view.readable);}, testID: 'read-series-read'}}
      state={state.phase === 'loading' && !state.items.length ? {kind: 'loading'} : undefined}
      scroller={scroller}>
      <ReadingSelectionToolbar port={actions} runtime={runtime}/>
      <PanelBlock>
        <PanelHeading title={t(comic ? 'next.reader.volumes' : 'next.kit.booksInSeries')} trailing={view.count}/>
        {failure && <StateRow {...failure}/>}
        {!state.items.length && state.phase === 'ready' && <StateRow kind="empty" icon="i-book"
          title={t('library.emptySeries')} reason={t('library.emptySeriesBody')}/>}
        {!!state.moreError && <StateRow kind="error" icon="i-warn" title={runtime.error(state.moreError, view.more, 'library').title as string}
          action={{label: t('action.tryAgain'), onPress: view.more}}/>}
      </PanelBlock>
    </TitleFrame>
    <ReadingItemMenu port={actions} runtime={runtime}/>
  </>;
}
