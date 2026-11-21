import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ArtworkSource} from '../artwork';
import {Button, Focusable} from '../controls';
import {QueueGroupLabel, QueueRow} from './QueueRow';
import {useCommittedPaint} from '../overlays/CommittedPaint';
import {useTheme} from '../theme';

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
  const row = (value: ListeningQueuePaintRow) => <QueueRow key={value.id} testID={`${testID}-row-${value.id}`} menuTestID={`${testID}-row-menu-${value.id}`} focusId={`${testID}-${value.id}`}
    title={value.title} detail={value.detail} seconds={value.seconds} source={value.source} current={value.current}
    onPress={value.playable && !value.current ? () => {if (paint.current()) paint.actions?.play?.(value.id);} : undefined}
    onMenu={value.menu ? () => {if (paint.current()) paint.actions?.menu?.(value.id);} : undefined}/>;
  const populated = current.length > 0 || groups.some(group => group.rows.length > 0);
  return <View testID={testID} pointerEvents={paint.interactive ? 'auto' : 'none'} accessibilityElementsHidden={!paint.interactive}
    importantForAccessibility={paint.interactive ? 'auto' : 'no-hide-descendants'} style={{flex: 1, minHeight: 0}}>
    <ScrollView scrollEnabled={paint.interactive} contentContainerStyle={{paddingBottom: theme.space.sm}} showsVerticalScrollIndicator={false}>
      {current.length ? <QueueGroupLabel>{t('player.queueNowPlaying')}</QueueGroupLabel> : null}
      {current.map(row)}
      {groups.map((group, index) => <React.Fragment key={`${group.key}:${index}`}>
        <QueueGroupLabel>{group.label}</QueueGroupLabel>
        {group.rows.map(row)}
      </React.Fragment>)}
      {!populated ? <Text style={[theme.type.rowHelp, {padding: theme.space.sm, color: theme.colors.textTertiary}]}>
        {t('player.queueEmpty')}
      </Text> : null}
    </ScrollView>
    {populated && summary?.length ? <View style={{borderTopWidth: theme.space.hairline, borderTopColor: theme.colors.line,
      paddingHorizontal: theme.space.sm, paddingVertical: theme.space.xs}}>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{summary.join(' · ')}</Text>
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
  const noticeInset = theme.isTV ? theme.space.sm : theme.gutter;
  if (paint.data.messageOnly) return <View pointerEvents={paint.interactive ? 'auto' : 'none'} style={{paddingHorizontal: noticeInset, paddingVertical: theme.space.lg}}>
    <Text accessibilityLiveRegion="polite" style={[theme.type.rowLabel, {textAlign: 'center', color: theme.colors.textSecondary}]}>{message}</Text>
  </View>;
  return <View testID={testID} pointerEvents={paint.interactive ? 'auto' : 'none'} accessibilityElementsHidden={!paint.interactive}
    importantForAccessibility={paint.interactive ? 'auto' : 'no-hide-descendants'} style={{flex: 1, minHeight: 0}}>
    {error ? <View style={{alignItems: 'center', gap: theme.space.sm, paddingHorizontal: noticeInset, paddingVertical: theme.space.lg}}>
      <Text style={[theme.type.rowHelp, {textAlign: 'center', color: theme.colors.textSecondary}]}>{error}</Text>
      <Button align="center" focusId="retry" testID={`${testID}-retry`} variant="glass" size="small" icon="i-restart" onPress={() => {if (paint.current()) paint.actions?.retry?.();}}>{t('action.tryAgain')}</Button>
    </View> : null}
    {message ? <View style={{paddingHorizontal: noticeInset, paddingVertical: theme.space.lg}}><Text style={[theme.type.rowLabel, {textAlign: 'center', color: theme.colors.textSecondary}]}>{message}</Text></View> : null}
    {resources.map(resource => <Focusable key={resource.id} focusKind="row" focusRadius={10} focusId={`${testID}-resource-${resource.id}`} testID={`${testID}-resource-${resource.id}`}
      accessibilityRole="button" onPress={() => {if (paint.current()) paint.actions?.choose?.(resource.id);}}
      style={({focused}) => ({minHeight: theme.targets.touch, marginHorizontal: theme.space.sm, paddingHorizontal: theme.space.sm, paddingVertical: theme.space.xs, justifyContent: 'center', borderRadius: 10, backgroundColor: focused ? 'rgba(237,245,250,.16)' : 'transparent'})}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{resource.label}</Text>
    </Focusable>)}
    {lines ? <ScrollView scrollEnabled={paint.interactive} ref={scroller} testID={`${testID}-lines`} onLayout={event => setViewport(event.nativeEvent.layout.height)} showsVerticalScrollIndicator={false}
      contentContainerStyle={{paddingHorizontal: theme.space.sm, paddingVertical: theme.space.md, gap: theme.space.xs}}>
      {lines.map(line => <Focusable key={line.index} focusKind="row" focusRadius={8} focusId={`${testID}-line-${line.index}`} testID={`${testID}-line-${line.index}`}
        accessibilityRole="button" accessibilityState={{selected: line.active, disabled: !line.seekable}} disabled={!line.seekable}
        onPress={() => {if (paint.current()) paint.actions?.seek?.(line.index);}} onLayout={event => {offsets.current.set(line.index, event.nativeEvent.layout.y);}}
        style={{paddingVertical: 4, paddingHorizontal: 6, borderRadius: 8}}>
        <Text style={[line.active ? theme.type.bodyStrong : theme.type.body, {textAlign: 'center', color: line.active ? theme.colors.text : theme.colors.textTertiary}]}>{line.text || '♪'}</Text>
      </Focusable>)}
    </ScrollView> : null}
  </View>;
}
