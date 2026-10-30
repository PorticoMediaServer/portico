import React, {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {activeLyricLine, lyricSeekPosition, type LyricsService, type LyricsSnapshot, type LyricsTarget} from '../../../../../../packages/client-core/src/lyrics';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Focusable} from '../../../kit/controls';
import {useTheme} from '../../../kit/theme';
import {browseType} from '../../../kit/browse/presentation';
import {committedPaintScope, useCommittedPaintPublication} from '../../../kit/overlays/CommittedPaint';
import type {ListeningLyricsPaintData} from './ListeningPanePaint';

const t = defaultI18n.t;
const emptySnapshot: LyricsSnapshot = Object.freeze({data: null, candidates: Object.freeze([]), warnings: Object.freeze([]), preview: null, busy: false, error: '', generation: 0});
const noService = () => emptySnapshot;
const noSubscription = () => () => {};

/** The song's own synced lyrics, from the server's lyric resource for this
 * session and source. The reading is bound to the accepted playback position,
 * so the highlighted line follows the engine rather than a wall clock, and a
 * line seeks only through the real playback command. The viewer's "Synced
 * lyrics" preference decides whether this is offered at all. */
export type NowPlayingLyricsProps = Readonly<{
  read: (target: LyricsTarget) => Readonly<{enabled: boolean; service: LyricsService}> | undefined;
  target: LyricsTarget;
  positionSeconds: number;
  playing: boolean;
  onSeek: (seconds: number) => void;
  /** Touch publisher only; its pure display leaf lives in the retained page. */
  paintOwner?: object;
  paintScope?: string;
  testID?: string;
}>;

export function NowPlayingLyrics({read, target, positionSeconds, playing, onSeek, paintOwner, paintScope = '', testID = 'now-playing-lyrics'}: NowPlayingLyricsProps) {
  const theme = useTheme(), scroller = useRef<ScrollView>(null), offsets = useRef(new Map<number, number>());
  const [viewport, setViewport] = useState(0);
  const owned = useMemo(() => {try {return read(target) ?? undefined;} catch {return undefined;}}, [read, target]);
  const service = owned?.service;
  const snapshot = useSyncExternalStore(service?.subscribe ?? noSubscription, service?.getSnapshot ?? noService);
  const live = useRef(playing); live.current = playing;
  useEffect(() => {
    if (!service) return;
    // One read when the panel opens, then a slow refresh while the song is playing;
    // a paused song keeps the lyrics it already has.
    const refresh = () => {if (live.current) void service.refresh();};
    void service.refresh();
    const timer = setInterval(refresh, 30_000);
    return () => {clearInterval(timer); service.dispose();};
  }, [service]);
  const selected = snapshot.data?.selection.resource, document = selected?.document, source = snapshot.data?.source;
  const current = document ? activeLyricLine(document, positionSeconds, selected?.offsetMs ?? 0, source?.startSeconds ?? 0) : -1;
  const paintSemantic = useMemo(() => committedPaintScope(paintOwner, paintScope, service, document, selected, source), [paintOwner, paintScope, service, document, selected, source]);
  const resources = useMemo(() => !document ? snapshot.data?.resources.filter(resource => !resource.deleted) ?? [] : [], [document, snapshot.data?.resources]);
  const resourceLabels = useMemo(() => resources.map(resource => ({id: resource.id, label: resource.provenance.label || resource.language})), [resources]);
  const seeks = useMemo(() => document?.lines.map(line => source ? lyricSeekPosition(line, selected?.offsetMs ?? 0, source) : null) ?? [], [document, selected?.offsetMs, source]);
  const lines = useMemo(() => document?.lines.map((line, index) => ({index, text: line.text, active: current >= 0 && index === current, seekable: typeof seeks[index] === 'number'})), [document, seeks, current]);
  // A progress render may supply a fresh closure over the same pinned command.
  // Keep the original command for this document/source lifetime; an old pane
  // must never borrow a successor's callback.
  const actions = useMemo(() => ({
    retry: () => {if (service) void service.refresh();},
    choose: (id: string) => {const resource = resources.find(value => value.id === id);if (resource && service) void service.choose(resource);},
    seek: (index: number) => {const at = seeks[index];if (typeof at === 'number' && Number.isFinite(at)) onSeek(at);},
    // The semantic token includes the original service, source and document.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [service, resources, seeks, paintSemantic]);
  const display: ListeningLyricsPaintData | undefined = useMemo(() => paintOwner && owned?.enabled ? {
    error: snapshot.error || undefined,
    messageOnly: !service,
    message: !service ? t('player.noLyrics') : !document && !snapshot.busy && !snapshot.error
      ? t(snapshot.data?.resources.length ? 'player.chooseLyrics' : 'player.noLyrics') : undefined,
    resources: resourceLabels,
    lines,
    activeIndex: current,
  } : undefined, [paintOwner, owned?.enabled, snapshot.error, snapshot.busy, snapshot.data?.resources.length, service, document, resourceLabels, lines, current]);
  const publication = useMemo(() => display ? {data: display, actions} : undefined, [display, actions]);
  useCommittedPaintPublication(paintOwner, 'listening-lyrics', paintScope, paintSemantic, publication);
  const renderedLines = useMemo(() => lines?.map(line => <Focusable key={line.index} focusKind="row" focusRadius={8} focusId={`${testID}-line-${line.index}`} testID={`${testID}-line-${line.index}`}
    accessibilityRole="button" accessibilityState={{selected: line.active, disabled: !line.seekable}} disabled={!line.seekable}
    onPress={() => actions.seek(line.index)}
    onLayout={event => {offsets.current.set(line.index, event.nativeEvent.layout.y);}}
    style={{paddingVertical: 4, paddingHorizontal: 6, borderRadius: 8}}>
    <Text style={[browseType(theme, line.active ? 19 : 17, line.active ? 800 : 600, -0.01), {textAlign: 'center', color: line.active ? theme.colors.text : theme.colors.textTertiary}]}>{line.text || '♪'}</Text>
  </Focusable>), [lines, actions, testID, theme]);
  useEffect(() => {
    if (paintOwner) return;
    const y = offsets.current.get(current);
    if (y !== undefined) scroller.current?.scrollTo({y: Math.max(0, y - viewport / 3), animated: true});
  }, [current, viewport, paintOwner]);
  if (paintOwner) return null;
  if (!owned?.enabled) return null;
  const message = (text: string) => <View style={{paddingHorizontal: 18, paddingVertical: 22}}><Text style={[browseType(theme, 14, 500), {textAlign: 'center', color: theme.colors.textSecondary}]}>{text}</Text></View>;
  if (!service) return message(t('player.noLyrics'));
  return <View testID={testID} style={{flex: 1, minHeight: 0}}>
    {snapshot.error ? <View style={{alignItems: 'center', gap: 10, paddingHorizontal: 18, paddingVertical: 18}}>
      <Text style={[browseType(theme, 13.5, 500), {textAlign: 'center', color: theme.colors.textSecondary}]}>{snapshot.error}</Text>
      <Button focusId="retry" testID={`${testID}-retry`} variant="glass" size="small" icon="i-restart" onPress={() => {void service.refresh();}}>{t('action.tryAgain')}</Button>
    </View> : null}
    {!document && !snapshot.busy && !snapshot.error ? message(snapshot.data?.resources.length ? t('player.chooseLyrics') : t('player.noLyrics')) : null}
    {resources.map(resource => <Focusable key={resource.id} focusKind="row" focusRadius={10}
      focusId={`${testID}-resource-${resource.id}`} testID={`${testID}-resource-${resource.id}`} accessibilityRole="button"
      onPress={() => {void service.choose(resource);}}
      style={({focused}) => ({minHeight: 48, marginHorizontal: 10, paddingHorizontal: 12, justifyContent: 'center', borderRadius: 10, backgroundColor: focused ? 'rgba(237,245,250,.16)' : 'transparent'})}>
      <Text numberOfLines={1} style={[browseType(theme, 14, 600), {color: theme.colors.text}]}>{resource.provenance.label || resource.language}</Text>
    </Focusable>)}
    {document ? <ScrollView ref={scroller} testID={`${testID}-lines`} onLayout={event => setViewport(event.nativeEvent.layout.height)} showsVerticalScrollIndicator={false}
      contentContainerStyle={{paddingHorizontal: 16, paddingVertical: 24, gap: 6}}>
      {renderedLines}
    </ScrollView> : null}
  </View>;
}
