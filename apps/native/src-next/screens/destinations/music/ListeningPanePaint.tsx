import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {Artwork, type ArtworkSource} from '../../../kit/artwork';
import {Button, Focusable} from '../../../kit/controls';
import {useCommittedPaint} from '../../../kit/overlays/CommittedPaint';
import {browseType} from '../../../kit/browse/presentation';
import {useTheme} from '../../../kit/theme';

const t = defaultI18n.t;
export type ListeningQueuePaintRow = Readonly<{id: string; title: string; detail?: readonly string[]; seconds?: number;
  current: boolean; source?: ArtworkSource; playable: boolean; menu: boolean}>;
export type ListeningQueuePaintGroup = Readonly<{key: string; label: string; rows: readonly ListeningQueuePaintRow[]}>;

/** Audited touch display only: no queue view, revision, edit lease, resolver or
 * command owner enters this leaf. Its native scroller lives with the paint. */
export type ListeningQueuePaintData = Readonly<{current: readonly ListeningQueuePaintRow[]; groups: readonly ListeningQueuePaintGroup[]; summary?: readonly string[]}>;
type QueueActions = Readonly<{play?: (id: string) => void; menu?: (id: string) => void}>;
export function ListeningQueuePaint({owner, scope = '', data: supplied, onPlay, onMenu, testID = 'now-playing-queue'}: {
  owner?: object; scope?: string; data?: ListeningQueuePaintData;
  onPlay?: (id: string) => void; onMenu?: (id: string) => void; testID?: string;
}) {
  const theme = useTheme();
  const paint = useCommittedPaint<ListeningQueuePaintData, QueueActions>({owner, channel: 'listening-queue', scope,
    value: supplied ? {data: supplied, actions: {play: onPlay, menu: onMenu}} : undefined});
  if (!paint.data) return null;
  const {current, groups, summary} = paint.data;
  const row = (value: ListeningQueuePaintRow) => {
    const body = <>
      {value.source ? <Artwork source={value.source} width={44} height={44} style={{borderRadius: 8, overflow: 'hidden', flexShrink: 0}}/> : null}
      <View style={{flex: 1, minWidth: 0, gap: 3}}>
        <Text numberOfLines={1} style={[browseType(theme, value.current ? 16 : 15, 700, -0.01), {color: theme.colors.text}]}>{value.title}</Text>
        {value.detail?.length ? <View style={{flexDirection: 'row', gap: 10}}>{value.detail.map((part, index) => <Text key={index} numberOfLines={1} style={[browseType(theme, 13, 400), {color: theme.colors.textSecondary, flexShrink: 1}]}>{part}</Text>)}</View> : null}
      </View>
      {value.seconds ? <Text style={[browseType(theme, 13, 400), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{durationText(value.seconds, t)}</Text> : null}
    </>;
    const main = !value.playable || value.current ? <View key={`${value.id}:main`} testID={`${testID}-row-${value.id}`} accessibilityRole="text" accessibilityLabel={[value.title, ...(value.detail ?? [])].filter(Boolean).join(', ')}
      style={{flex: 1, minHeight: 52, paddingLeft: 14, paddingVertical: 10, flexDirection: 'row', alignItems: 'center', gap: 12}}>{body}</View>
      : <Focusable key={`${value.id}:main`} testID={`${testID}-row-${value.id}`} focusId={`${testID}-${value.id}`} focusKind="row" focusRadius={10}
        accessibilityRole="button" accessibilityLabel={t('player.queuePlay', {title: value.title})} onPress={() => {if (paint.current()) paint.actions?.play?.(value.id);}}
        style={({focused}) => ({flex: 1, minHeight: 52, paddingLeft: 14, paddingVertical: 10, flexDirection: 'row', alignItems: 'center', gap: 12, borderRadius: 10,
          backgroundColor: focused ? 'rgba(237,245,250,.16)' : 'transparent'})}>{body}</Focusable>;
    return value.menu ? <View key={value.id} style={{flexDirection: 'row', alignItems: 'center'}}>{main}
      <Focusable testID={`${testID}-row-menu-${value.id}`} focusKind="button" accessibilityRole="button" accessibilityLabel={t('server.row.actions', {name: value.title})}
        onPress={() => {if (paint.current()) paint.actions?.menu?.(value.id);}} style={{width: 44, height: 44, alignItems: 'center', justifyContent: 'center'}}>
        <Text style={[browseType(theme, 18, 800), {color: theme.colors.textSecondary}]}>⋯</Text>
      </Focusable>
    </View> : main;
  };
  const heading = (text: string) => <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 10, paddingHorizontal: 14, paddingTop: 16, paddingBottom: 6}}>
    <Text style={[browseType(theme, 13, 700, 0.06), {color: theme.colors.textSecondary, textTransform: 'uppercase'}]}>{text}</Text>
  </View>;
  const populated = current.length > 0 || groups.some(group => group.rows.length > 0);
  return <View testID={testID} pointerEvents={paint.interactive ? 'auto' : 'none'} accessibilityElementsHidden={!paint.interactive}
    importantForAccessibility={paint.interactive ? 'auto' : 'no-hide-descendants'} style={{flex: 1, minHeight: 0}}>
    <ScrollView scrollEnabled={paint.interactive} contentContainerStyle={{paddingBottom: 16}} showsVerticalScrollIndicator={false}>
      {current.length ? heading(t('player.queueNowPlaying')) : null}
      {current.map(row)}
      {groups.map((group, index) => <React.Fragment key={`${group.key}:${index}`}>
        {heading(group.label)}{group.rows.map(row)}
      </React.Fragment>)}
      {!populated ? <Text style={[browseType(theme, 14, 400), {paddingHorizontal: 14, paddingVertical: 18, color: theme.colors.textTertiary}]}>{t('player.queueEmpty')}</Text> : null}
    </ScrollView>
    {populated ? <View style={{borderTopWidth: 1, borderTopColor: theme.colors.line, paddingHorizontal: 14, paddingVertical: 12}}>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 10}}>{summary?.map((part, index) => <Text key={index} style={[browseType(theme, 13, 600), {color: theme.colors.textTertiary}]}>{part}</Text>)}</View>
    </View> : null}
  </View>;
}

export type ListeningLyricPaintLine = Readonly<{index: number; text: string; active: boolean; seekable: boolean}>;
export type ListeningLyricPaintResource = Readonly<{id: string; label: string}>;
/** The service stays in its live publisher. Only observed display values and
 * callbacks for that original publisher reach the live version of this leaf. */
export type ListeningLyricsPaintData = Readonly<{error?: string; message?: string; messageOnly?: boolean; resources: readonly ListeningLyricPaintResource[];
  lines?: readonly ListeningLyricPaintLine[]; activeIndex: number}>;
type LyricActions = Readonly<{retry?: () => void; choose?: (id: string) => void; seek?: (index: number) => void}>;
export function ListeningLyricsPaint({owner, scope = '', data: supplied, onRetry, onChoose, onSeek, testID = 'now-playing-lyrics'}: {
  owner?: object; scope?: string; data?: ListeningLyricsPaintData;
  onRetry?: () => void; onChoose?: (id: string) => void; onSeek?: (index: number) => void; testID?: string;
}) {
  const theme = useTheme(), scroller = useRef<ScrollView>(null), offsets = useRef(new Map<number, number>());
  const [viewport, setViewport] = useState(0);
  const paint = useCommittedPaint<ListeningLyricsPaintData, LyricActions>({owner, channel: 'listening-lyrics', scope,
    value: supplied ? {data: supplied, actions: {retry: onRetry, choose: onChoose, seek: onSeek}} : undefined});
  const {error, message, resources = [], lines, activeIndex = -1} = paint.data ?? {};
  useEffect(() => {
    if (!paint.interactive) return;
    const y = offsets.current.get(activeIndex);
    if (y !== undefined) scroller.current?.scrollTo({y: Math.max(0, y - viewport / 3), animated: true});
  }, [activeIndex, viewport, paint.interactive]);
  if (!paint.data) return null;
  if (paint.data.messageOnly) return <View pointerEvents={paint.interactive ? 'auto' : 'none'} style={{paddingHorizontal: 18, paddingVertical: 22}}>
    <Text style={[browseType(theme, 14, 500), {textAlign: 'center', color: theme.colors.textSecondary}]}>{message}</Text>
  </View>;
  return <View testID={testID} pointerEvents={paint.interactive ? 'auto' : 'none'} accessibilityElementsHidden={!paint.interactive}
    importantForAccessibility={paint.interactive ? 'auto' : 'no-hide-descendants'} style={{flex: 1, minHeight: 0}}>
    {error ? <View style={{alignItems: 'center', gap: 10, paddingHorizontal: 18, paddingVertical: 18}}>
      <Text style={[browseType(theme, 13.5, 500), {textAlign: 'center', color: theme.colors.textSecondary}]}>{error}</Text>
      <Button variant="glass" size="small" icon="i-restart" onPress={() => {if (paint.current()) paint.actions?.retry?.();}}>{t('action.tryAgain')}</Button>
    </View> : null}
    {message ? <View style={{paddingHorizontal: 18, paddingVertical: 22}}><Text style={[browseType(theme, 14, 500), {textAlign: 'center', color: theme.colors.textSecondary}]}>{message}</Text></View> : null}
    {resources.map(resource => <Focusable key={resource.id} focusKind="row" focusRadius={10} focusId={`${testID}-resource-${resource.id}`} testID={`${testID}-resource-${resource.id}`}
      accessibilityRole="button" onPress={() => {if (paint.current()) paint.actions?.choose?.(resource.id);}}
      style={({focused}) => ({minHeight: 48, marginHorizontal: 10, paddingHorizontal: 12, justifyContent: 'center', borderRadius: 10, backgroundColor: focused ? 'rgba(237,245,250,.16)' : 'transparent'})}>
      <Text numberOfLines={1} style={[browseType(theme, 14, 600), {color: theme.colors.text}]}>{resource.label}</Text>
    </Focusable>)}
    {lines ? <ScrollView scrollEnabled={paint.interactive} ref={scroller} testID={`${testID}-lines`} onLayout={event => setViewport(event.nativeEvent.layout.height)} showsVerticalScrollIndicator={false}
      contentContainerStyle={{paddingHorizontal: 16, paddingVertical: 24, gap: 6}}>
      {lines.map(line => <Focusable key={line.index} focusKind="row" focusRadius={8} focusId={`${testID}-line-${line.index}`} testID={`${testID}-line-${line.index}`}
        accessibilityRole="button" accessibilityState={{selected: line.active, disabled: !line.seekable}} disabled={!line.seekable}
        onPress={() => {if (paint.current()) paint.actions?.seek?.(line.index);}} onLayout={event => {offsets.current.set(line.index, event.nativeEvent.layout.y);}}
        style={{paddingVertical: 4, paddingHorizontal: 6, borderRadius: 8}}>
        <Text style={[browseType(theme, line.active ? 19 : 17, line.active ? 800 : 600, -0.01), {textAlign: 'center', color: line.active ? theme.colors.text : theme.colors.textTertiary}]}>{line.text || '♪'}</Text>
      </Focusable>)}
    </ScrollView> : null}
  </View>;
}
