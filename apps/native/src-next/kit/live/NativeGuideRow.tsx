import React, {useCallback, useId, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import Animated, {useAnimatedStyle, type SharedValue} from 'react-native-reanimated';
import Svg, {Defs, Pattern, Rect} from 'react-native-svg';
import {SLOT_MS, layoutRow, legacyChannel, nowNext, type GuideCell, type GuideChannelRow, type GuideProgram} from '../../../../../packages/client-core/src/guide';
import {channelUnavailableMessage} from '../../../../../packages/client-core/src/presentation/live-availability';
import {programmeFactParts} from '../../../../../packages/client-core/src/presentation/kit-model';
import {Artwork, ChannelMark} from '../artwork';
import {Focusable} from '../controls';
import {FocusItem, useCovered} from '../focus';
import {Icon} from '../Icon';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {GuideLoadingMarker} from './GuideLoadingMarker';
import {GuideProgrammeTitle} from './GuideProgrammeTitle';
import {ProgrammeFlags} from './ProgrammeDetails';
import type {NativeLiveController} from '../../screens/settings-live/live-controller';
import {useScreenEntry} from '../../screens/settings-live/Screen';
import {nativeGuideCellWindow, nativeGuideElapsedWidth, nativeGuideTitleLines, type nativeGuidePanRange} from '../../screens/settings-live/guide-pan-model';
import {cellFocusId, channelFocusId, nativeGuideNext, nativeProgrammeCaption, type GuideCellIdentity} from '../../screens/settings-live/guide-presentation';
import {tvGuideNavigationCells} from '../../tv/layout/guide-focus';
import {livePageTakesFocus} from '../../screens/settings-live/tv-settings-focus';

const cellRefKey = (cell: GuideCell) => JSON.stringify([cell.program ? 'programme' : 'gap', cell.program?.id ?? cell.start]);

export function LiveRow({selected, onChannelMenu, tvRange, onTVFocus, panPosition, panActive, laneWidth, clock, entryEnabled, entryOpen, index, controller, runtime, start, end, now, channelWidth, rowHeight, px, list, onOpen, grid, watch}: {tvRange: ReturnType<typeof nativeGuidePanRange>; onTVFocus: (index: number, cell?: GuideCell) => void; panPosition: SharedValue<number>; panActive: boolean; laneWidth: number; clock: (at: number) => string | undefined; entryEnabled: boolean; entryOpen: () => boolean; index: number; controller: NativeLiveController; runtime: NativeShellRuntime; start: number; end: number; now: number; channelWidth: number; rowHeight: number; px: number; list: boolean; onOpen: (channel: GuideChannelRow, program?: GuideProgram, node?: View | null) => void; grid: string; watch: (channel: GuideChannelRow) => void; onChannelMenu: (channel: GuideChannelRow, node?: View | null) => void; selected?: Readonly<{channel: GuideChannelRow; program?: GuideProgram}>}) {
  const theme = useTheme(), t = runtime.text.t, guide = controller.guide;
  useSyncExternalStore(listener => guide.subscribePage(guide.pageOf(index), listener), () => guide.getSnapshot().version);
  const overscan = theme.isTV ? tvRange ? SLOT_MS : 0 : panActive ? (end - start) / 2 : 0;
  const channel = guide.channelAt(index), programs = channel?.guide === 'none' ? {programs: [], complete: true, failed: false} : guide.programsFor(index, start - overscan, end + overscan);
  const shift = useAnimatedStyle(() => ({transform: [{translateX: panActive ? start * px - panPosition.value : 0}]}), [panActive, start, px]);
  const first = programs.programs.find(value => value.start <= now && value.end > now) ?? programs.programs[0];
  const compactChannel = !theme.isTV && channelWidth <= 80;
  const phoneGrid = compactChannel, rowGap = theme.isTV ? 8 : phoneGrid ? 0 : 6;
  const cells = layoutRow(programs.programs, {viewStart: start - overscan, viewEnd: end + overscan, pxPerMs: px, now, gapPx: phoneGrid ? 2 : 6, tiers: theme.isTV ? {full: 300, title2: 150, title: 32} : {full: 50, title2: 50, title: 24}});
  // The list row always renders the now cell, so it owns the entry whenever the list
  // is what this row draws; only the guide can enter on a real programme or gap.
  const entryCell: GuideCellIdentity | undefined = list ? {kind: 'now'}
    : first ? {kind: 'programme', id: first.id}
      : cells[0] ? {kind: 'gap', start: cells[0].start} : undefined;
  // The ask is made only while the page can take it: not covered, still the Live destination, visible and not
  // under expanded media (what revealTVCell checks, plus covered), and not cancelled since it was armed.
  const covered = useCovered(), coveredRef = useRef(covered), entryOpenRef = useRef(entryOpen); coveredRef.current = covered; entryOpenRef.current = entryOpen;
  const entryCurrent = useCallback(() => {
    if (!controller.lifetime.current()) return false;
    const shell = runtime.getSnapshot();
    return livePageTakesFocus({covered: coveredRef.current, destination: shell.destination.kind, contentVisible: shell.contentVisible, mediaExpanded: shell.mediaExpanded, homeObscured: shell.homeObscured}) && entryOpenRef.current();
  }, [controller, runtime]);
  const entry = useScreenEntry(theme.isTV && entryEnabled && index === 0 && channel && entryCell ? {regionId: grid, childId: cellFocusId(channel, entryCell)} : undefined, entryCurrent);
  const channelRef = useRef<View>(null), programRefs = useRef(new Map<string, View>());
  if (!channel) return theme.isTV ? <View style={{height: rowHeight, marginBottom: rowGap, flexDirection: 'row', backgroundColor: theme.colors.raised, opacity: .4, borderRadius: 12}}>
    <FocusItem focusId={`pending-channel:${index}`} cell={{row: index, kind: 'channel'}}><Focusable focusKind="row" accessibilityLabel={t('status.loading')} onFocus={() => onTVFocus(index)} style={{width: channelWidth, height: rowHeight}}/></FocusItem>
    <FocusItem focusId={`pending-programme:${index}`} cell={{row: index, kind: 'programme', start, end}}><Focusable focusKind="row" accessibilityLabel={t('status.loading')} onFocus={() => onTVFocus(index)} style={{flex: 1, height: rowHeight}}/></FocusItem>
  </View> : <View style={{height: rowHeight, marginBottom: rowGap, backgroundColor: theme.colors.raised, opacity: .4, borderRadius: 12}}/>;
  const logo = runtime.artwork(channel.logoUrl);
  const current = nowNext(programs.programs, now), nextText = nativeGuideNext(current.next, clock, runtime.text);
  if (list && !theme.isTV) return <NativeLiveListRow channel={channel} programme={current.now} next={current.next} now={now} runtime={runtime} clock={clock} height={rowHeight} onOpen={node => onOpen(channel, current.now, node)} onMenu={node => onChannelMenu(channel, node)}/>;
  // Spec §10: an unavailable channel is dimmed and says why (LT-4); a server-wide reason is not repeated per row.
  const unavailable = !channel.tuneAvailable ? channelUnavailableMessage(channel.tuneUnavailableReason ?? '') : undefined;
  return <View style={{height: rowHeight, opacity: legacyChannel(channel)?.serverHidden || !channel.tuneAvailable ? .5 : 1, marginBottom: rowGap, flexDirection: 'row', gap: theme.isTV ? 18 : phoneGrid ? 2 : 8}}>
    <View style={{width: channelWidth - (theme.isTV ? 18 : phoneGrid ? 2 : 8)}}>
      <FocusItem focusId={channelFocusId(channel)} cell={theme.isTV ? {row: index, kind: 'channel'} : undefined}><Focusable ref={channelRef} focusId={channelFocusId(channel)} testID={`live-channel-${channel.sourceId}-${channel.id}`} accessibilityRole="button" accessibilityLabel={[`${channel.number} ${channel.name}`, unavailable ? runtime.text.t(unavailable) : undefined].filter(Boolean).join(', ')} onFocus={() => onTVFocus(index)} onLongPress={() => onChannelMenu(channel, channelRef.current)} onPress={() => list ? onOpen(channel, current.now, channelRef.current) : theme.isTV ? watch(channel) : onOpen(channel, undefined, channelRef.current)} focusKind="row" style={{height: rowHeight, borderRadius: compactChannel ? 8 : 12, paddingHorizontal: phoneGrid ? 0 : 16, flexDirection: compactChannel ? 'column' : 'row', justifyContent: compactChannel ? 'center' : undefined, alignItems: 'center', gap: compactChannel ? 5 : 16, backgroundColor: phoneGrid ? 'transparent' : theme.colors.raised}}>
        <View style={{width: theme.isTV ? 56 : 36, height: theme.isTV ? 56 : 36}}><GuideChannelArt channel={channel} source={logo} size={theme.isTV ? 56 : 36}/>{compactChannel && channel.favorite && <View style={{position: 'absolute', right: -7, top: -6, width: 18, height: 18, borderRadius: 9, backgroundColor: theme.colors.ink, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-star-f" size={10} color={theme.colors.accentStrong}/></View>}</View>
        <View style={{flex: compactChannel ? undefined : 1, minWidth: 0, alignItems: compactChannel ? 'center' : undefined}}>
          {!compactChannel && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight}}>
            {theme.isTV ? <GuideProgrammeTitle title={channel.name} lines={2} maxHeight={rowHeight - (theme.type.micro.lineHeight ?? theme.space.md)}
              style={[theme.type.cardTitle, {color: theme.colors.text}]}/> : <Text style={[theme.type.captionStrong, {color: theme.colors.text, flexShrink: 1}]}>{channel.name}</Text>}
          </View>}
          <Text style={[theme.isTV ? theme.type.micro : theme.type.caption, {color: theme.colors.textSecondary}]}>{channel.number}</Text>
        </View>
      </Focusable></FocusItem>
    </View>
    <View style={{flex: 1, overflow: 'hidden'}} onLayout={entry}>
      {list ? <FocusItem focusId={cellFocusId(channel, {kind: 'now'})} cell={theme.isTV ? {row: index, kind: 'programme', start, end} : undefined}><Focusable onFocus={() => onTVFocus(index)} onLongPress={theme.isTV ? () => onOpen(channel, current.now, programRefs.current.get('now')) : undefined} ref={node => {if (node) programRefs.current.set('now', node);}} focusId={cellFocusId(channel, {kind: 'now'})} testID={`live-now-${channel.id}`} onPress={() => onOpen(channel, current.now, programRefs.current.get('now'))} style={{height: rowHeight, padding: 14, backgroundColor: theme.colors.raised, borderRadius: 12, gap: 8}}><Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{current.now?.title ?? t('guide.noInformation')}</Text>{nextText && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{nextText}</Text>}</Focusable></FocusItem>
        : <Animated.View style={[{flex: 1}, shift]}>{(theme.isTV ? tvGuideNavigationCells(cells.filter(cell => !tvRange || cell.end > tvRange.first && cell.start < tvRange.last + (end - start)),
          channel.guide !== 'none' && tvRange ? {first: tvRange.first, last: tvRange.last + (end - start)} : undefined, start - overscan, px, SLOT_MS) : cells).map(cell => {
          const refKey = cellRefKey(cell), loading = 'pending' in cell && cell.pending === true || cell.kind === 'gap' && !programs.complete && !programs.failed;
          const paint = <NativeLiveGuideCell selected={!!selected?.program && selected.program.id === cell.program?.id && selected.channel.id === channel.id && selected.channel.sourceId === channel.sourceId && selected.channel.kind === channel.kind} focusId={cellFocusId(channel, cell.program ? {kind: 'programme', id: cell.program.id} : {kind: 'gap', start: cell.start})} cell={cell} gapLabel={runtime.text.t(channel.guide === 'none' ? 'guide.noGuide' : 'guide.noInformation')} gapBody={channel.guide === 'none' ? runtime.text.t('guide.noGuideBody') : undefined} loading={loading} failed={cell.kind === 'gap' && programs.failed} retry={() => guide.invalidate()} left={theme.isTV && loading ? 0 : cell.x - overscan * px + (phoneGrid ? 2 : 0)} laneWidth={laneWidth} panPosition={panPosition} panActive={panActive} start={start} now={now} px={px} rowHeight={rowHeight} compact={phoneGrid} clock={clock} runtime={runtime} testID={`live-programme-${channel.id}-${cell.program?.id ?? cell.start}`} refNode={node => {if (node) programRefs.current.set(refKey, node); else programRefs.current.delete(refKey);}} onTVFocus={() => onTVFocus(index, cell)} onPress={() => onOpen(channel, cell.program, programRefs.current.get(refKey))}/>;
          const focusId = cellFocusId(channel, cell.program ? {kind: 'programme', id: cell.program.id} : {kind: 'gap', start: cell.start});
          return <FocusItem key={refKey} focusId={focusId} cell={theme.isTV ? {row: index, kind: 'programme', boundary: 'pending' in cell && cell.pending === true, start: cell.start, end: cell.end} : undefined}>
            {theme.isTV && loading
              ? <Focusable focusId={focusId} focusKind="row" accessibilityLabel={t('status.loading')} onFocus={() => onTVFocus(index, cell)}
                style={{position: 'absolute', left: cell.x - overscan * px, top: 0, width: cell.width, height: rowHeight}}>{paint}</Focusable>
              : paint}
          </FocusItem>;
        })}</Animated.View>}
    </View>
  </View>;
}

function NativeLiveListRow({channel, programme, next, now, runtime, clock, height, onOpen, onMenu}: {channel: GuideChannelRow; programme?: GuideProgram; next?: GuideProgram; now: number; runtime: NativeShellRuntime; clock: (at: number) => string | undefined; height: number; onOpen: (node: View | null) => void; onMenu: (node: View | null) => void}) {
  const theme = useTheme(), node = useRef<View>(null), logo = runtime.artwork(channel.logoUrl), nextText = nativeGuideNext(next, clock, runtime.text);
  const remaining = programme && programmeFactParts({startMs: programme.start, endMs: programme.end}, runtime.text, now).find(fact => fact.id === 'remaining');
  const progress = programme && programme.end > programme.start ? Math.max(0, Math.min(1, (now - programme.start) / (programme.end - programme.start))) : undefined;
  return <Focusable ref={node} testID={`live-list-${channel.sourceId}-${channel.id}`} accessibilityRole="button" accessibilityLabel={[channel.name, programme?.title].filter(Boolean).join(' ')} onPress={() => onOpen(node.current)} onLongPress={() => onMenu(node.current)} focusKind="row" style={{minHeight: height, opacity: legacyChannel(channel)?.serverHidden ? .5 : 1, paddingVertical: 12, paddingHorizontal: 20, flexDirection: 'row', gap: 14, borderBottomWidth: 1, borderBottomColor: theme.colors.line}}>
    <View style={{width: 46, alignItems: 'center', gap: 5}}>{(logo || channel.mark && channel.sourceId) && <View style={{width: 46, height: 46, borderRadius: 11, overflow: 'hidden'}}><GuideChannelArt channel={channel} source={logo} size={46}/></View>}<Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{channel.number}</Text></View>
    <View style={{flex: 1, minWidth: 0}}>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 8}}><Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary, flexShrink: 1}]}>{channel.name}</Text>{channel.favorite && <Icon name="i-star-f" size={10} color={theme.colors.accentStrong}/>}<ProgrammeFlags programme={programme} text={runtime.text}/>{remaining && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, marginLeft: 'auto'}]}>{remaining.text}</Text>}</View>
      <Text style={[theme.type.listLabel, {color: theme.colors.text, marginTop: 3}]}>{programme?.title ?? runtime.text.t('guide.noInformation')}</Text>
      {progress !== undefined && <View style={{height: 3, borderRadius: 2, backgroundColor: theme.colors.lineStrong, marginTop: 8, overflow: 'hidden'}}><View style={{height: '100%', width: `${progress * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>}
      {nextText && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 7}]}>{nextText}</Text>}
    </View>
  </Focusable>;
}

function NativeLiveGuideCell({selected, cell, focusId, gapLabel, gapBody, loading, failed, retry, left, laneWidth, panPosition, panActive, start, now, px, rowHeight, compact, clock, runtime, testID, refNode, onPress, onTVFocus}: {selected: boolean; onTVFocus: () => void; cell: GuideCell; focusId: string; gapLabel: string; gapBody?: string; loading: boolean; failed: boolean; retry: () => void; left: number; laneWidth: number; panPosition: SharedValue<number>; panActive: boolean; start: number; now: number; px: number; rowHeight: number; compact: boolean; clock: (at: number) => string | undefined; runtime: NativeShellRuntime; testID: string; refNode: (node: View | null) => void; onPress: () => void}) {
  const theme = useTheme(), fadeId = useId().replace(/:/g, ''), padding = theme.isTV ? 16 : compact ? 7 : 8;
  const gap = cell.kind === 'gap', narrow = nativeGuideTitleLines(cell.tier, left, Math.max(0, cell.width - (compact ? 2 : 0)), laneWidth, compact, theme.isTV) === 1;
  const background = selected ? theme.colors.raisedBright : !theme.isTV && gap ? 'transparent' : cell.state === 'now' ? theme.colors.raisedHigh : !theme.isTV && cell.state === 'past' ? alpha(theme.colors.raised, .5) : theme.colors.raised;
  const cellWidth = Math.max(0, cell.width - (compact ? 2 : 0));
  const elapsed = !gap && cell.state === 'now', progressRoom = elapsed ? theme.space.tight : 0;
  const bodyHeight = rowHeight - (compact ? theme.space.tight : 0) - progressRoom - (selected ? 4 : gap && !theme.isTV ? 2 : 0);
  const [titleHeight, setTitleHeight] = useState(0);
  const titleStyle = theme.isTV ? theme.type.cardTitle : gap ? theme.type.rowHelp : theme.type.captionStrong;
  const titleLines = compact && cellWidth >= theme.space.xl ? 2 : narrow ? 1 : 2;
  const captionGap = theme.space.tight;
  const captionHeight = titleHeight > 0 ? bodyHeight - titleHeight - captionGap : 0;
  const captions = (cell.tier === 'full' || cell.tier === 'title2') && captionHeight >= (theme.type.caption.lineHeight ?? theme.type.caption.fontSize ?? 0);
  const content = useAnimatedStyle(() => {
    const window = nativeGuideCellWindow(left, cellWidth, laneWidth, panActive ? start * px - panPosition.value : 0);
    const inset = Math.min(padding, Math.max(0, (window.width - 16) / 2));
    return {transform: [{translateX: window.cutLeft + inset}], width: Math.max(0, window.width - inset * 2)};
  }, [left, cellWidth, laneWidth, panActive, start, px, padding]);
  const captionLines = gapBody ? [gapBody] : nativeProgrammeCaption(cell.program, cell.state === 'now', theme.isTV, clock, runtime.text);
  if (loading) return <GuideLoadingMarker cellWidth={cellWidth} rowHeight={rowHeight} compact={compact} left={left} testID={`${testID}-loading`}/>;
  if (failed) return <Focusable ref={refNode} focusId={focusId} testID={`${testID}-error`} accessibilityRole="button" accessibilityLabel={runtime.text.t('guide.tileFailed')} onFocus={onTVFocus} onLongPress={theme.isTV ? retry : undefined} onPress={retry} focusKind="row" style={{position: 'absolute', left, top: compact ? 3 : 0, width: cellWidth, height: rowHeight - (compact ? 6 : 0), borderRadius: compact ? 8 : 12, backgroundColor: theme.colors.raised}}><Animated.View style={[{position: 'absolute', left: 0, top: 0, bottom: 0, justifyContent: 'center'}, content]}><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{runtime.text.t('guide.tileFailed')}</Text></Animated.View></Focusable>;
  return <Focusable ref={refNode} focusId={focusId} testID={testID} accessibilityRole="button" accessibilityLabel={cell.program?.title ?? gapLabel} onFocus={onTVFocus} onLongPress={theme.isTV ? onPress : undefined} onPress={onPress} accessibilityState={{selected}} focusKind="row" style={{position: 'absolute', left, top: compact ? 3 : 0, width: cellWidth, height: rowHeight - (compact ? 6 : 0), borderRadius: compact ? 8 : 12, backgroundColor: background, borderWidth: selected ? 2 : !theme.isTV && gap ? 1 : 0, borderColor: selected ? theme.colors.accentStrong : theme.colors.line, overflow: 'hidden'}}>
    {!theme.isTV && gap && <View pointerEvents="none" style={{position: 'absolute', inset: 0}}><Svg width="100%" height="100%" accessible={false}><Defs><Pattern id={`${fadeId}-gap`} patternUnits="userSpaceOnUse" width={14} height={14} patternTransform="rotate(45)"><Rect width={7} height={14} fill={theme.colors.text} fillOpacity={.035}/></Pattern></Defs><Rect width="100%" height="100%" fill={`url(#${fadeId}-gap)`}/></Svg></View>}
    <Animated.View style={[{position: 'absolute', left: 0, top: 0, bottom: progressRoom, justifyContent: 'center'}, content]}>
      <View style={{flexDirection: 'row', alignItems: 'flex-start'}}>
        <GuideProgrammeTitle title={cell.program?.title ?? gapLabel} lines={titleLines} maxHeight={bodyHeight} onHeight={setTitleHeight}
          style={[titleStyle, {color: !theme.isTV && (gap || cell.state === 'past') ? theme.colors.textTertiary : theme.colors.text}]}/>
      </View>
      {captions && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, marginTop: captionGap}}>
        <ProgrammeFlags programme={cell.program} grid text={runtime.text}/>
        {cell.program?.recording && <View style={{width: theme.space.xs, height: theme.space.xs, borderRadius: theme.space.xs / 2, backgroundColor: theme.colors.record}}/>}
        {captionLines.map((caption, index) => <GuideProgrammeTitle key={index} title={caption} lines={1} whole
          maxHeight={captionHeight} style={[theme.type.caption, {color: !theme.isTV && cell.state === 'now' ? theme.colors.textSecondary : theme.colors.textTertiary,
            opacity: !theme.isTV && cell.state === 'past' ? .65 : 1}]}/>)}
      </View>}
    </Animated.View>
    {elapsed && <View style={{position: 'absolute', left: 0, right: 0, bottom: 0, height: 3, backgroundColor: alpha(theme.colors.text, .07)}}><View style={{height: '100%', width: nativeGuideElapsedWidth(left, cellWidth, (now - start) * px), backgroundColor: theme.colors.accentStrong}}/></View>}
  </Focusable>;
}

export function GuideChannelArt({channel, source, size}: {channel: GuideChannelRow; source: ReturnType<NativeShellRuntime['artwork']>; size: number}) {
  const mark = channel.mark && channel.sourceId ? {...channel.mark, sourceId: channel.sourceId, id: channel.id} : undefined;
  // A logo without a mark takes the mark's corners, so the channel column reads as one set of tiles.
  return mark ? <ChannelMark mark={mark} source={source} size={size}/> : source ? <View style={{width: size, height: size, borderRadius: size * 8 / 34, overflow: 'hidden'}}><Artwork source={source} fit="contain"/></View> : null;
}
