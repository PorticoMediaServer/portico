import React, {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {generatedCoverTint} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ReadingItem} from '../../../../../packages/client-core/src';
import {Artwork, AmbientArt, GeneratedCover, StandardBackdrop} from '../../kit/artwork';
import {Button, Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {WindowedGrid} from '../../kit/layout';
import {NativeReadingWindow} from './reading-window';
import {ReadingItemMenu, ReadingSelectionToolbar, useReadingActions} from './ReadingActions';
import {SelectionCircle} from '../../kit/selection';
import type {CardSelection} from '../../kit/cards/model';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {askNativeDownload} from '../downloads/request';
import {nativePublicationReadsAvailable} from '../../../platform/offline-reading';
import {StateView, SkeletonBlock, LoadingPaint} from '../../kit/states';
import {alpha, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeReadSeries} from './read-series-model';

const {t} = defaultI18n;
const initial = (title: string): string => title.trim().slice(0, 1).toUpperCase() || '·';
type SeriesRoute = Extract<NativeShellRoute, {kind: 'read-series'}>;

/** phone-read 4/4c: the series' signed, ordered volumes. The hero shows the
 * signed summary when the server publishes one. The one real continue row
 * marks only its own progress; menus use the Reading producer's personal commands. */
export function ReadSeriesScreen({controller, route, comic, context, runtime, onBack}: {
  controller: NativeReadSeries; route: SeriesRoute; comic: boolean; context: NativeShellBootstrapContext; runtime: NativeShellRuntime; onBack: () => void;
}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme(), insets = useSafeAreaInsets(), dimensions = useWindowDimensions();
  const phone = theme.platform === 'phone';
  const actions = useReadingActions(context, runtime, controller);
  const offer = (item: ReadingItem) => {const lifetime = controller.itemLifetime(item);
    return lifetime ? {item, lifetime, open: () => {void controller.open(item).catch(runtime.reportError);},
      ...(nativePublicationReadsAvailable() ? {download: () => askNativeDownload({title: item.title, itemId: item.id, reading: true, lifetime})} : {})} : undefined;
  };
  const [paneWidth, setPaneWidth] = useState<number>(), [heroHeight, setHeroHeight] = useState(0), [collapsed, setCollapsed] = useState(false);
  useLayoutEffect(() => {
    if (!controller.arm()) return;
    void controller.load().catch(() => {});
    return () => controller.release();
  }, [controller, route]);
  const continuedRow = state.continued, continued = continuedRow?.item, first = continued ?? state.items[0];
  const title = state.overview?.title ?? first?.series?.name ?? t('library.tab.series');
  const overview = state.overview;
  const authors = [...new Set([...(overview?.author ? [overview.author.value] : []), ...state.items.map(item => item.author?.value)])]
    .filter((value): value is string => !!value);
  const exactCount = !overview && state.phase === 'ready' && state.nextCursor === undefined ? state.items.length : undefined;
  const total = overview?.total ?? exactCount;
  const cover = runtime.artwork(first?.coverUrl), tint = generatedCoverTint(title);
  // The volumes grid fills its own pane (the shell rail is outside it) and the drawn
  // phone page puts three portrait covers across with 10 pt between columns.
  const columns = theme.platform === 'phone' ? 3 : 5, columnGap = 10, rowGap = 16;
  const width = Math.max(96, Math.min(220, (Math.min(900, paneWidth ?? dimensions.width) - 40 - columnGap * (columns - 1)) / columns));
  const height = width * 3 / 2;
  const progression = continuedRow ? Math.max(0, Math.min(1, continuedRow.progress.location.progression)) : undefined;
  const readLabel = continued ? t('next.cards.read')
    : comic && first?.series?.number !== undefined ? t('next.reader.readVolume', {number: defaultI18n.number(first.series.number, {maximumFractionDigits: 20})})
      : t(phone ? 'next.reader.readNext' : 'next.reader.read');
  const stack = state.items.slice(0, 3);
  const window = useMemo(() => new NativeReadingWindow(() => {const value = controller.getSnapshot();
    return {items: value.items, cursor: value.nextCursor, loading: value.phase === 'loading' || !!value.morePending, error: value.moreError};
  }, controller.subscribe, controller.more, item => `${item.id}:${item.sourceId}`), [controller]);
  return <View testID={`native-read-series-${route.seriesId}`} style={{flex: 1}} onLayout={event => {
    const next = Math.round(event.nativeEvent.layout.width);
    if (next > 0 && next !== paneWidth) setPaneWidth(next);
  }}><StandardBackdrop/>
    <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 0, right: 0, height: 560, overflow: 'hidden'}}>
      {runtime.ambient(first?.coverUrl) ? <AmbientArt source={runtime.ambient(first?.coverUrl)} treatment="title" style={{position: 'absolute', top: 0, left: 0, right: 0, bottom: 0}}/> : null}
      <View style={{position: 'absolute', top: 0, left: 0, right: 0, bottom: 0, backgroundColor: theme.colors.ink, opacity: .5}}/>
    </View>
    {/* phone-read 4/4c: past the hero the drawn page pins a bar with the way back
        and the series name; before that the same back button floats over the art. */}
    <View pointerEvents={collapsed ? 'auto' : 'box-none'} style={{position: 'absolute', top: 0, left: 0, right: 0, zIndex: 10,
      paddingTop: insets.top + 6, height: insets.top + 50, backgroundColor: collapsed ? theme.colors.ink : 'transparent',
      borderBottomWidth: collapsed ? 1 : 0, borderBottomColor: theme.colors.line}}>
      <View pointerEvents="box-none" style={{height: 44, paddingHorizontal: 10, justifyContent: 'center'}}>
        <Button variant="icon" icon="i-left" iconOnly accessibilityLabel={t('action.back')} onPress={onBack} style={{height: 44, width: 44}}/>
        {collapsed ? <View pointerEvents="none" style={{position: 'absolute', left: 64, right: 64, height: 44, justifyContent: 'center'}}>
          <Text numberOfLines={1} style={[browseType(theme, 17, 700), {textAlign: 'center', color: theme.colors.text}]}>{title}</Text>
        </View> : null}
      </View>
    </View>
    {state.phase === 'error' ? <StateView {...runtime.error(state.error, () => {void controller.retry().catch(runtime.reportError);}, 'library')}/>
      : <LoadingPaint ready={state.phase === 'ready' || state.phase === 'loading' && state.items.length > 0} immediate={state.phase === 'ready'}
        skeleton={<SkeletonBlock width="100%" height={360}/>} style={{flex: 1}} testID="native-read-series-paint">
        <WindowedGrid collection={window} getKey={item => `${item.id}:${item.sourceId}`} itemWidth={width} itemHeight={height + 66}
          gap={columnGap} rowGap={rowGap} gutter={20} rightGutter={20} paddingBottom={insets.bottom + 48}
          style={{flex: 1, width: '100%', maxWidth: 900, alignSelf: 'center'}}
          onScrollOffset={offset => setCollapsed(current => {const next = heroHeight > 0 && offset > heroHeight - 50; return next === current ? current : next;})}
          header={<View style={{paddingTop: Math.max(96, insets.top + 56)}}>
        <View style={{alignItems: 'center', paddingHorizontal: 20}} onLayout={event => {
          const next = Math.round(event.nativeEvent.layout.height);
          if (next > 0 && next !== heroHeight) setHeroHeight(next);
        }}>
          <View style={{width: 280, height: 214}}>
            {stack.length ? stack.map((item, index) => {
              const left = stack.length === 1 ? 76 : stack.length === 2 ? 36 + index * 80 : [4, 72, 146][index]!;
              const top = stack.length === 3 ? [18, 6, 14][index]! : 12;
              const rotate = stack.length === 1 ? '0deg' : stack.length === 2 ? (index ? '5deg' : '-5deg') : ['-9deg', '-2deg', '7deg'][index]!;
              return <View key={item.id} style={{position: 'absolute', left, top, width: 128, height: 192, borderRadius: 7, overflow: 'hidden',
                transform: [{rotate}], backgroundColor: theme.colors.raised, shadowColor: theme.colors.shadow, shadowOpacity: .6, shadowRadius: 24, shadowOffset: {width: 0, height: 14}, elevation: index + 4}}>
                {runtime.artwork(item.coverUrl) ? <Artwork source={runtime.artwork(item.coverUrl)} width={128} height={192}/>
                  : <GeneratedCover title={item.title} author={item.author?.value} tint={generatedCoverTint(item.title)} glyph={initial(item.title)} width={128} height={192}/>}
              </View>;
            }) : <View style={{position: 'absolute', left: 76, top: 12, width: 128, height: 192, borderRadius: 7, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
              {cover ? <Artwork source={cover} width={128} height={192} priority/> : <GeneratedCover title={title} author={authors[0]} tint={tint} glyph={initial(title)} width={128} height={192}/>}
            </View>}
          </View>
          <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, 34, 800, -.02, phone ? 34 : 'normal'), {color: theme.colors.text, textAlign: 'center', marginTop: 14}]}>{title}</Text>
          {authors.length || total !== undefined ? <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'center', columnGap: 12, rowGap: phone ? 6 : 4, marginTop: 10}}>
            {authors.length ? <Text numberOfLines={2} style={[browseType(theme, 13, 700), {color: theme.colors.text, textAlign: 'center'}]}>{defaultI18n.list(authors)}</Text> : null}
            {total !== undefined ? <Text style={[browseType(theme, 13, 600), {color: theme.colors.textSecondary}]}>{t(comic ? 'next.reader.volumeCount' : 'next.kit.books', {count: total})}</Text> : null}
          </View> : null}
          {comic && (state.direction === 'rtl' || state.direction === 'ltr') ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 5, marginTop: 10, paddingHorizontal: 8, paddingVertical: 3,
            borderRadius: 12, borderWidth: 1, borderColor: alpha(theme.colors.text, .24)}}>
            <Icon name={state.direction === 'rtl' ? 'i-rtl' : 'i-ltr'} size={13} color={theme.colors.textSecondary}/>
            <Text style={[browseType(theme, 11.5, 600), {color: theme.colors.textSecondary}]}>{t(state.direction === 'rtl' ? 'next.reader.directionRtl' : 'next.reader.directionLtr')}</Text>
          </View> : null}
        </View>
        <View style={{paddingHorizontal: 20, marginTop: 18}}>
          <Button icon="i-book" disabled={!first} onPress={() => {if (first) void controller.open(first).catch(runtime.reportError);}} style={{alignSelf: 'stretch'}}>
            {readLabel}
          </Button>
        </View>
        {progression !== undefined ? <View style={{height: 4, borderRadius: 2, marginHorizontal: 20, marginTop: 10, backgroundColor: alpha(theme.colors.text, .18)}}>
          <View style={{width: `${progression * 100}%`, height: 4, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/>
        </View> : null}
        <ReadingSelectionToolbar port={actions} runtime={runtime}/>
        <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 10, marginTop: 24, marginBottom: 10, paddingHorizontal: 20}}>
          <Text style={[browseType(theme, 19, 700, -.015), {color: theme.colors.text}]}>{t(comic ? 'next.reader.volumes' : 'next.kit.booksInSeries')}</Text>
          {overview ? <Text style={[browseType(theme, 13, 600), {color: theme.colors.textTertiary}]}>{t('next.reader.seriesReadCount', {read: defaultI18n.number(overview.completed), count: defaultI18n.number(overview.total)})}</Text>
            : exactCount !== undefined ? <Text style={[browseType(theme, 13, 600), {color: theme.colors.textTertiary}]}>{defaultI18n.number(exactCount)}</Text> : null}
        </View>
          {state.items.length === 0 && state.phase === 'ready' ? <StateView testID="native-read-series-empty" variant="inline" title={t('library.emptySeries')} body={t('library.emptySeriesBody')}/> : null}
          {state.moreError ? <StateView {...runtime.error(state.moreError, () => {void controller.more().catch(runtime.reportError);}, 'library')}/> : null}
          </View>}
          renderPlaceholder={() => <SkeletonBlock width={width} height={height} radius={10}/>}
          renderItem={item => {const offered = offer(item); return <VolumeCard item={item} width={width} height={height} comic={comic} theme={theme} runtime={runtime}
            progression={continuedRow?.item.id === item.id ? progression : undefined}
            onOpen={() => {void controller.open(item).catch(runtime.reportError);}}
            selection={offered ? actions.selection(offered) : undefined} onMenu={offered ? anchorRef => actions.actions.show({...offered, anchorRef}) : undefined}/>;}}/>
      </LoadingPaint>}
    <ReadingItemMenu port={actions} runtime={runtime}/>
  </View>;
}

/** The drawn volume card, shared with the book page's series shelf. The shelf
 * variant only changes the measured title/number metrics and their order to the
 * frame's card (title above its "Book N" line); the series page keeps its own. */
export function VolumeCard({item, width, height, comic, theme, runtime, progression, onOpen, onMenu, selection, variant = 'default'}: {
  item: ReadingItem; width: number; height: number; comic: boolean; theme: ReturnType<typeof useTheme>;
  runtime: NativeShellRuntime; progression?: number; onOpen: () => void; onMenu?: (anchor: React.RefObject<View | null>) => void; selection?: CardSelection; variant?: 'default' | 'shelf';
}) {
  const anchor = useRef<View>(null);
  const number = item.series?.number, shelf = variant === 'shelf', phoneDefault = !shelf && theme.platform === 'phone';
  const numberLabel = number === undefined ? undefined : t(comic ? 'next.reader.volumeNumber' : 'next.reader.bookNumber', {number: defaultI18n.number(number, {maximumFractionDigits: 20})});
  const numberText = numberLabel === undefined ? null : <Text numberOfLines={1} style={[browseType(theme, shelf ? 13 : 11.5, shelf ? 400 : phoneDefault ? 500 : 600), {color: theme.colors.textTertiary, marginTop: shelf ? 2 : phoneDefault ? 2 : 7}]}>
    {numberLabel}
  </Text>;
  const titleText = <Text numberOfLines={shelf ? 1 : 2} style={[browseType(theme, shelf ? 13.5 : phoneDefault ? 12.5 : 13, 600), {color: theme.colors.text, marginTop: shelf ? 10 : phoneDefault ? 7 : number !== undefined ? 2 : 7}]}>{item.title}</Text>;
  return <Focusable accessibilityRole="button" accessibilityLabel={item.title} focusKind="row" focusRadius={10} ref={anchor} onPress={selection?.active ? selection.onToggle : onOpen}
    onLongPressAction={onMenu ? () => onMenu(anchor) : undefined} accessibilityState={selection?.active ? {selected: selection.selected} : undefined} style={{width}}>
    <View style={{width, height, borderRadius: 10, overflow: 'hidden', backgroundColor: theme.colors.raised, borderWidth: selection?.selected ? 2 : 0, borderColor: theme.colors.accentStrong}}>
      {runtime.artwork(item.coverUrl) ? <Artwork source={runtime.artwork(item.coverUrl)} width={width} height={height}/>
        : <GeneratedCover title={item.title} author={item.author?.value} tint={generatedCoverTint(item.title)} glyph={initial(item.title)} width={width} height={height}/>}
      {progression !== undefined ? <View style={{position: 'absolute', left: 0, right: 0, bottom: 0, height: 3, backgroundColor: alpha(theme.colors.text, .2)}}>
        <View style={{width: `${progression * 100}%`, height: 3, backgroundColor: theme.colors.accentStrong}}/>
      </View> : null}
      {selection?.active ? <SelectionCircle selected={selection.selected} selecting interactive={false} accessibilityLabel={item.title}
        style={{position: 'absolute', right: 7, bottom: 7}}/> : null}
    </View>
    {shelf || phoneDefault ? <>{titleText}{numberText}</> : <>{numberText}{titleText}</>}
    {item.author && !shelf && !phoneDefault ? <Text numberOfLines={1} style={[browseType(theme, 11.5, 500), {color: theme.colors.textTertiary, marginTop: 2}]}>{item.author.value}</Text> : null}
  </Focusable>;
}
