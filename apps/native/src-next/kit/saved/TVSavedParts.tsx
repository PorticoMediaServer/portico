import React, {type ComponentProps, type ReactNode, type RefObject} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {durationText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {Artwork} from '../artwork';
import {Button, Focusable} from '../controls';
import {StateView} from '../states';
import {FocusRegion} from '../focus';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {TVFocusGrid} from '../../tv/layout/TVFocusGrid';
import {type Actions, type Tab, emptyFor, clock, historyLine} from '../../pages/shared/saved/useTVSavedPage';

const t = defaultI18n.t;

export function TVSavedEmpty({tab, browseHome}: {tab: Tab; browseHome: () => void}) {
  const copy = emptyFor[tab];
  const body = [t(copy.body), tab === 'watchlist' ? t('saved.holdHint') : ''].filter(Boolean).join(' ');
  return <StateView testID="saved-empty" variant="centered" title={t(copy.title)} body={body} primaryAction={{id: 'browse-home', label: t('saved.browseHome'), onPress: browseHome}}/>;
}

export function TVSavedHistoryRow({entry, updatedAt, positionSeconds, completed, runtime, actions, openEntry, onFocus, disabled}: {
  entry: ContentEntry; updatedAt?: string; positionSeconds?: number; completed?: boolean; runtime: NativeShellRuntime; actions: Actions; openEntry: (entry: ContentEntry) => void; onFocus?: () => void; disabled?: boolean;
}) {
  const theme = useTheme(), progress = entry.duration && positionSeconds !== undefined ? Math.max(0, Math.min(1, positionSeconds / entry.duration)) : undefined;
  const remaining = entry.duration && positionSeconds !== undefined ? durationText(Math.max(0, entry.duration - positionSeconds), runtime.text.t) : undefined;
  const video = ['movie', 'episode', 'show', 'season', 'extra'].includes(entry.kind), art = runtime.artwork(video ? entry.backdropUrl ?? entry.stillUrl : entry.posterUrl);
  return <Focusable disabled={disabled} onFocus={onFocus} testID={`saved-history-${encodeURIComponent(entry.id)}`} accessibilityRole="button" accessibilityLabel={entry.show?.name ?? entry.displayTitle ?? entry.title}
    onPress={() => entry.playback ? actions.play(entry) : openEntry(entry)} onLongPressAction={() => actions.menu(entry)} focusKind="row" focusRadius={16} style={{minHeight: 104}}>
    {({focused}) => <View style={{minHeight: 104, marginBottom: 8, flexDirection: 'row', alignItems: 'center', gap: 26, paddingLeft: 14, paddingRight: 28, borderRadius: 16, backgroundColor: focused ? theme.colors.raisedHigh : undefined}}>
      <View style={{width: 156, height: 88, borderRadius: 10, overflow: 'hidden', backgroundColor: theme.colors.raised, flexShrink: 0}}>
        {art ? <Artwork source={art} width={156} height={88}/> : null}
        {!video && art ? <View style={{position: 'absolute', left: 42, top: 8, width: 72, height: 72, borderRadius: 6, overflow: 'hidden', borderWidth: 1, borderColor: theme.colors.line}}><Artwork source={art} width={72} height={72}/></View> : null}
      </View>
      <View style={{flex: 1, minWidth: 0}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{entry.show?.name ?? entry.displayTitle ?? entry.title}</Text>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 3}]}>{historyLine(entry)}</Text>
      </View>
      <View style={{width: 280, flexShrink: 0}}>
        {completed ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('saved.watched')}</Text> : <>
          {progress !== undefined ? <View style={{height: 6, borderRadius: 6, backgroundColor: 'rgba(237,245,250,.18)', marginBottom: 8}}><View style={{height: 6, width: `${progress * 100}%`, borderRadius: 6, backgroundColor: theme.colors.accentStrong}}/></View> : null}
          {remaining ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('next.kit.remaining', {time: remaining})}</Text> : null}
        </>}
      </View>
      <Text style={[theme.type.rowHelp, {width: 140, textAlign: 'right', color: theme.colors.textSecondary, fontVariant: ['tabular-nums'], flexShrink: 0}]}>{clock(updatedAt, runtime.text.locale) ?? ''}</Text>
    </View>}
  </Focusable>;
}


export function TVSavedScroll({node, root, onExtent, onHeight, onOffset, children}: {
  node: RefObject<ScrollView | null>; root: RefObject<View | null>;
  onExtent: (height: number) => void; onHeight: (height: number) => void; onOffset: (offset: number) => void; children: ReactNode;
}) {
  const theme = useTheme();
  return <ScrollView ref={node} testID="native-tv-saved" onContentSizeChange={(_width, height) => onExtent(height)}
    onLayout={event => onHeight(event.nativeEvent.layout.height)} onScroll={event => onOffset(event.nativeEvent.contentOffset.y)}
    scrollEventThrottle={16} contentContainerStyle={{paddingBottom: theme.space.pageBottom}} showsVerticalScrollIndicator={false}>
    <View ref={root} collapsable={false}>{children}</View>
  </ScrollView>;
}

export function TVSavedSection({onTop, children}: {onTop?: (top: number) => void; children: ReactNode}) {
  return <View onLayout={onTop && (event => onTop(event.nativeEvent.layout.y))}>{children}</View>;
}

export function TVSavedDirectoryPane({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{height: 860, paddingTop: theme.space.controlGap}}>{children}</View>;
}

export function TVSavedToolbar({region, children, action, count}: {region: Omit<ComponentProps<typeof FocusRegion>, 'style'>; children: ReactNode; action?: ReactNode; count: string}) {
  const theme = useTheme();
  return <FocusRegion {...region} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap,
    paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight, paddingTop: theme.space.md}}>
    {children}
    {action && <View style={{marginLeft: 'auto'}}>{action}</View>}
    <Text style={[theme.type.rowHelp, {marginLeft: action ? 0 : 'auto', color: theme.colors.textTertiary}]}>{count}</Text>
  </FocusRegion>;
}

export function TVSavedToolbarDivider() {
  const theme = useTheme();
  return <View style={{width: theme.space.hairline, height: theme.targets.touch, backgroundColor: theme.colors.line, marginHorizontal: theme.space.tight}}/>;
}

export function TVSavedResults({region, onTop, children}: {region: Omit<ComponentProps<typeof FocusRegion>, 'style'>; onTop: (top: number) => void; children: ReactNode}) {
  const theme = useTheme();
  return <FocusRegion {...region} onLayout={event => onTop(event.nativeEvent.layout.y)}
    style={{paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight, paddingTop: theme.space.lg, minHeight: 520}}>{children}</FocusRegion>;
}

export function TVSavedHeading({title, count}: {title: string; count: number}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'baseline', gap: theme.space.metaGap, marginTop: theme.space.sectionGap, marginBottom: theme.space.md}}>
    <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{defaultI18n.number(count)}</Text>
  </View>;
}

export function TVSavedHistoryDay({day, first}: {day: string; first: boolean}) {
  const theme = useTheme();
  return <View style={{marginTop: first ? theme.space.tight : theme.space.lg, marginBottom: theme.space.controlGap}}>
    <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{day}</Text>
  </View>;
}

export function TVSavedCardCell({onTop, children}: {onTop: (top: number) => void; children: ReactNode}) {
  return <View style={{width: savedCardWidth}} onLayout={event => onTop(event.nativeEvent.layout.y)}>{children}</View>;
}

export const savedCardWidth = 200;

export function TVSavedGrid<T>({shape, ...props}: Omit<ComponentProps<typeof TVFocusGrid<T>>, 'itemWidth' | 'itemHeight' | 'gap' | 'rowGap' | 'initialWidth'> & {
  shape: 'history' | 'posters' | 'squares';
}) {
  const theme = useTheme(), history = shape === 'history';
  return <TVFocusGrid {...props} itemWidth={history ? 1 : savedCardWidth} itemHeight={history ? 104 : shape === 'posters' ? 380 : 280}
    gap={history ? 0 : theme.space.lg} rowGap={history ? 0 : 40} initialWidth={1920 - theme.gutter - theme.layout.gutter.tvRight}/>;
}
