import React, {createContext, useContext, useLayoutEffect, useMemo, useRef} from 'react';
import {ScrollView, StyleSheet, Text, View} from 'react-native';
import {GestureDetector} from 'react-native-gesture-handler';
import Animated, {useSharedValue} from 'react-native-reanimated';
import type {QueuePlaybackView} from '../../../../../packages/client-core/src/queue-controller';
import type {PlaybackFence} from '../../../../../packages/player-core/src';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork} from '../artwork';
import {Layer} from '../shell/Layer';
import {useTheme} from '../theme';
import {ListeningGroup, useListeningCoverSlot, useListeningPageOwner, useListeningPageActive, useListeningPageHeight,
  useListeningPagePresent, useListeningPaintPermit, useListeningPageStyle} from '../dock/ListeningCoverBridge';
import type {DockOccupant} from '../dock/types';
import {usePublishedPlayerMorph} from '../dock/PlayerMorphBridge';
import {useOverlayPaintMode} from '../overlays/OverlayPaintContext';
import {nowPlayingSource} from '../../screens/destinations/music/NowPlayingQueue';

const t = defaultI18n.t;
const QUEUE_WIDTH = 366;
const SIDE_BY_SIDE = 700;
const ListeningChromeGate = createContext(false);

export function ListeningCover({fence, source}: {fence: PlaybackFence; source: DockOccupant['presentation']['artwork']}) {
  const theme = useTheme(), slot = useRef<View>(null);
  const gesture = useListeningChromeGesture(fence, false), shared = useListeningCoverSlot(fence, 'full', slot, true);
  return <ListeningCoverTarget slot={slot} onLayout={shared.measure} gesture={gesture}>
    <Animated.View style={[StyleSheet.absoluteFill, shared.style]}><Artwork source={source} style={[StyleSheet.absoluteFill, {borderRadius: theme.radii.artwork}]}/></Animated.View>
  </ListeningCoverTarget>;
}

function ListeningColumn({fence, source, playingFrom, header, identity, horizontal = false, cover, wide, height, measureOnly = false, testID, children}: {
  fence: PlaybackFence; source: DockOccupant['presentation']['artwork']; playingFrom?: string; cover: number; wide: boolean; height: number;
  header?: React.ReactNode; identity?: React.ReactNode; horizontal?: boolean; measureOnly?: boolean; testID?: string; children?: React.ReactNode;
}) {
  const theme = useTheme(), coverSlot = useRef<View>(null);
  const gesture = useListeningChromeGesture(fence, measureOnly);
  const present = useListeningPagePresent(fence);
  const sharedCover = useListeningCoverSlot(fence, 'full', coverSlot, !measureOnly || !present);
  return <ScrollView onScroll={sharedCover.measure} scrollEventThrottle={16} pointerEvents={measureOnly ? 'none' : 'auto'}
    contentContainerStyle={{flexGrow: 1, ...(wide ? {justifyContent: 'center' as const} : {})}}
    style={{flex: 1, minWidth: 0, ...(measureOnly ? {opacity: 0} : {})}} showsVerticalScrollIndicator={false}>
    <View testID={testID} style={{flex: 1, minWidth: 0, alignItems: 'center', paddingHorizontal: wide ? 28 : 24, paddingTop: wide ? 36 : 0}}>
      {header}
      {playingFrom ? <ListeningGroup fence={fence} index={0} height={height}><Text style={[theme.type.rowHelp, {marginBottom: 14, textAlign: 'center', color: theme.colors.textSecondary}]}>{t('next.music.playingFrom', {source: playingFrom})}</Text></ListeningGroup> : null}
      <View style={horizontal ? {width: '100%', maxWidth: 600, flexDirection: 'row', alignItems: 'flex-end', gap: 26} : undefined}>
      {source ? <ListeningCoverTarget gesture={gesture} slot={coverSlot} onLayout={sharedCover.measure} size={cover}>
        {!measureOnly && <Animated.View style={sharedCover.style}><Artwork source={source} width={cover} height={cover} style={{borderRadius: 14}}/></Animated.View>}
      </ListeningCoverTarget> : null}
        {identity}
      </View>
      {children}
    </View>
  </ScrollView>;
}
export function ListeningPageBackground({fence, height, ambient, fallbackVisible = true}: {fallbackVisible?: boolean; fence: PlaybackFence; height: number; ambient: DockOccupant['presentation']['ambient']}) {
  const theme = useTheme(), reportHeight = useListeningPageHeight(), style = useListeningPageStyle(fence, height, fallbackVisible);
  return <Animated.View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" onLayout={event => reportHeight(event.nativeEvent.layout.height)}
    style={[{position: 'absolute', inset: 0, zIndex: theme.layers.player + 1}, style]}><Layer kind="audio" art={ambient} style={{position: 'absolute', inset: 0}}/>
  </Animated.View>;
}
/** Paint only from the admitted mini descriptor. No full screen, queue/lyrics,
 * song read, guarded callback, controller or output is created by this preview. */
export function ListeningPagePreview({occupant, queue, sidePane = false, width, height, topInset, bottomInset}: {
  occupant: DockOccupant; queue?: QueuePlaybackView; sidePane?: boolean; width: number; height: number; topInset: number; bottomInset: number;
}) {
  const present = useListeningPagePresent(occupant.playback.fence);
  const wide = width >= SIDE_BY_SIDE, side = wide && sidePane;
  const segment = nowPlayingSource(queue);
  const book = occupant.mode === 'audiobook', panelWidth = width >= 1024 ? 380 : QUEUE_WIDTH;
  const cover = book ? Math.min(wide ? 250 : 290, width - 48) : wide ? Math.max(160, Math.min(410, width - (side ? panelWidth + 40 : 0) - 56)) : width - 48;
  return <View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{position: 'absolute', inset: 0, opacity: present ? 0 : 1}}>
    <View style={{flex: 1, paddingTop: topInset, paddingBottom: bottomInset, flexDirection: side ? 'row' : 'column'}}>
      <ListeningColumn fence={occupant.playback.fence} source={occupant.presentation.artwork} playingFrom={wide ? segment : undefined} header={!wide ? <View style={{height: 70}}/> : undefined} cover={cover} wide={wide} height={height} horizontal={book && wide && width - (side ? panelWidth + 40 : 0) - 56 >= 560} measureOnly/>
      {side && <View style={{width: panelWidth + 40}}/>}
    </View>
  </View>;
}


/** Only static audited UI outlives its publisher. This root owns no reader,
 * command, playback lease, queue or song controller. */
export function ListeningSurface({fence, testID, children, blocked = false}: {fence: PlaybackFence; testID?: string; children: React.ReactNode; blocked?: boolean}) {
  const mode = useOverlayPaintMode(), theme = useTheme(), active = useListeningPageActive(fence);
  useListeningPageOwner(fence);
  if (!mode.visible || !active) return null;
  return <ListeningChromeGate.Provider value={blocked}><View testID={testID} pointerEvents={mode.interactive ? 'box-none' : 'none'} accessible={false}
    accessibilityElementsHidden={!mode.interactive} importantForAccessibility={mode.interactive ? 'auto' : 'no-hide-descendants'}
    style={{position: 'absolute', inset: 0, zIndex: theme.layers.player}}>{children}</View></ListeningChromeGate.Provider>;
}


/** The cover is the physical swipe target, leaving queue/lyrics scrolling and
 * the scrubber to their original recognisers. Every target has its own handler. */
function ListeningCoverTarget({gesture, slot, onLayout, size = '100%', children}: {gesture?: ReturnType<NonNullable<ReturnType<typeof usePublishedPlayerMorph>>['chromeGesture']>; slot: React.RefObject<View | null>; onLayout: () => void; size?: number | '100%'; children: React.ReactNode}) {
  const view = <View ref={slot} collapsable={false} onLayout={onLayout} style={{width: size, height: size}}>{children}</View>;
  return gesture ? <GestureDetector gesture={gesture}>{view}</GestureDetector> : view;
}


function useListeningChromeGesture(fence: PlaybackFence, disabled: boolean) {
  const blocked = useContext(ListeningChromeGate), mode = useOverlayPaintMode();
  const controller = usePublishedPlayerMorph(fence), current = useSharedValue(false), permit = useListeningPaintPermit(fence);
  const allowed = !disabled && !blocked && mode.interactive;
  useLayoutEffect(() => {current.value = allowed; return () => {current.value = false;};}, [current, allowed]);
  return useMemo(() => controller?.chromeGesture(allowed, current, permit?.current), [controller, allowed, current, permit]);
}

export function ListeningPanel({children, opaque = false}: {children: React.ReactNode; opaque?: boolean}) {
  const theme = useTheme();
  return <View style={{position: 'absolute', inset: 0, zIndex: theme.layers.player + 1, backgroundColor: opaque ? theme.colors.ink : undefined}}>{children}</View>;
}

export function ListeningNoticeLine({text}: {text: string}) {
  const theme = useTheme();
  return <Text accessibilityRole="alert" accessibilityLiveRegion="polite" style={theme.type.caption}>{text}</Text>;
}
