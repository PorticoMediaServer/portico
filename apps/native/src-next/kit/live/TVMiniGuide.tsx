import React, {useCallback, useEffect, useRef, useState} from 'react';
import {Modal, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {GuideChannelRow, GuideProgram} from '../../../../../packages/client-core/src/guide/types';
import {shellLayout} from '../../shell/model';
import {FocusBoundary} from '../../kit/overlays/surface';
import {useOverlayLifecycle} from '../../kit/overlays/OverlayHost';
import {MiniGuideAll, MiniGuideHead, MiniGuideRow} from '../../kit/live/MiniGuide';
import type {ArtworkSource} from '../../kit/artwork/types';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {useTheme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';
import {useTVPanelPresence, useTVPresentationCompletion} from '../../kit/overlays/TVPanelMotion';
import {useNativeModalPresence} from '../../kit/overlays/native-dismissal';

/** One supplied channel: the canonical channel row and the programmes the parent
 * proved for it. Nothing here is read, resolved or invented by this component. */
export type NativeMiniGuideRow = Readonly<{
  channel: GuideChannelRow;
  now?: GuideProgram;
  next?: GuideProgram;
  watching: boolean;
}>;

export type NativeMiniGuideProps = Readonly<{
  open?: boolean;
  /** Presentation completion only; the caller still owns original-opener proof. */
  onExited?: () => void;
  /** At most five real channels: the current one and the known neighbours. */
  rows: readonly NativeMiniGuideRow[];
  /** The viewer's current clock, already on the parent's authority. */
  now: number;
  /** The guide's published timezone. A rejected name draws no time, never another zone. */
  timezone: string;
  loading?: boolean;
  busy?: boolean;
  error?: React.ReactNode;
  /** Resolves a served logo path to a displayable source, as the rest of the shell does. */
  artwork?: (path?: string) => ArtworkSource | undefined;
  onSelect: (channel: GuideChannelRow) => void;
  onClose: () => void;
  onFullGuide: () => void;
}>;

/** The published clock time, or nothing when the supplied zone cannot name one. */
const clock = (at: number, locale: string, timezone: string): string | undefined => {
  if (!Number.isFinite(at)) return undefined;
  try {return new Intl.DateTimeFormat(locale, {hour: 'numeric', minute: '2-digit', timeZone: timezone}).format(new Date(at));}
  catch {return undefined;}
};

/** tv-player 11's mini guide: the picture stays, and this panel lists the supplied
 * channels beside it, drawn by the kit's `MiniGuideHead`, `MiniGuideRow` and `MiniGuideAll`. TV only; the touch screen never mounts it. Focus enters on the
 * channel actually playing when it can be tuned, else the first tuneable row; OK
 * tunes, Back closes. The caller owns the opening and the real host's restoration. */
export function NativeMiniGuide(props: NativeMiniGuideProps) {
  const open = props.open ?? true;
  const presence = useTVPanelPresence(open ? props : undefined, undefined);
  const exited = () => {if (!open) {presence.exited(); props.onExited?.();}};
  return presence.value ? <MiniGuideSurface {...presence.value} open={open} onExited={exited}/> : null;
}

function MiniGuideSurface(props: NativeMiniGuideProps & {open: boolean; onExited: () => void}) {
  const theme = useTheme(), window = useWindowDimensions(), t = defaultI18n.t, locale = defaultI18n.locale;
  const motion = useMotion(), opacity = useSharedValue(0), finished = useRef(props.onExited);
  finished.current = props.onExited;
  const completion = useTVPresentationCompletion();
  // `out`: the fade out has ended and the guide's Modal is leaving. It has exited (and the caller returns focus) once
  // the platform reports the Modal gone (useNativeModalPresence).
  const [out, setOut] = useState(false), native = useNativeModalPresence(!out);
  const exited = useCallback((ticket: number) => {if (completion.current(ticket)) setOut(true);}, [completion]), open = props.open;
  useEffect(() => {if (out && !native.mounted) finished.current();}, [out, native.mounted]);
  useEffect(() => {
    const ticket = completion.begin();
    if (open) setOut(false);
    opacity.value = withTiming(open ? 1 : 0, {...motion.timing(open ? 'standard' : 'quick', open ? 'out' : 'in'),
      duration: motion.reduce ? 160 : open ? 180 : 120}, done => {if (done && !open) runOnJS(exited)(ticket);});
    return () => {completion.cancel(ticket); cancelAnimation(opacity);};
  }, [open, motion, opacity, exited, completion]);
  const fade = useAnimatedStyle(() => ({opacity: opacity.value}));
  // The frame's own limit; supplied rows are never stretched to fill it.
  const rows = props.rows.slice(0, 5);
  const tuneable = (row: NativeMiniGuideRow) => row.channel.tuneAvailable;
  const watching = rows.findIndex(row => row.watching && tuneable(row));
  const entry = watching >= 0 ? watching : rows.findIndex(tuneable);
  const first = useRef<View>(null);
  useOverlayLifecycle(theme.isTV && props.open, true, () => props.onClose(), first, undefined, 0, 'tv-mini-guide', false);
  if (!theme.isTV) return null;
  const canvas = shellLayout(window.width, window.height, true), glass = glassRecipe(), radius = 24;
  // Cross-source channels can share an id, so a row is named by kind, source and id.
  const rowKey = (row: NativeMiniGuideRow) => `${row.channel.kind}/${row.channel.sourceId ?? ''}/${row.channel.id}`;
  const progress = (row: NativeMiniGuideRow): number | undefined => {
    const start = row.now?.start, end = row.now?.end;
    if (!Number.isFinite(props.now) || typeof start !== 'number' || typeof end !== 'number' || !Number.isFinite(start) || !Number.isFinite(end) || end <= start) return undefined;
    return Math.max(0, Math.min(1, (props.now - start) / (end - start)));
  };
  return <Modal key={native.key} transparent visible={native.visible} onDismiss={native.onDismiss} onShow={native.onShow} animationType="none" onRequestClose={props.onClose} statusBarTranslucent navigationBarTranslucent>
    <Animated.View pointerEvents={props.open ? 'auto' : 'none'} accessibilityElementsHidden={!props.open} importantForAccessibility={props.open ? 'auto' : 'no-hide-descendants'} style={[StyleSheet.absoluteFill, fade]} accessibilityViewIsModal>
      <View pointerEvents="box-none" testID="tv-mini-guide-canvas" style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height, transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
        {/* The drawn shell keeps its shadow outside the clipped glass layer. */}
        <View style={{position: 'absolute', right: 96, top: 80, width: 680, borderRadius: radius,
          boxShadow: '0 40px 80px -24px rgba(0,0,0,.9), inset 0 0 0 1px rgba(237,245,250,.08)'}}>
          <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: radius, overflow: 'hidden'}]}>
            {glass.nativeBlur && <BlurView style={StyleSheet.absoluteFill} blurType="dark" blurAmount={30} reducedTransparencyFallbackColor={theme.colors.ink}/>}
            <View style={[StyleSheet.absoluteFill, {backgroundColor: 'rgba(11,23,32,.86)'}]}/>
          </View>
          <FocusBoundary modal testID="tv-mini-guide" style={{padding: 18}}>
            <MiniGuideHead/>
            {!!props.error && <View style={{paddingHorizontal: 12, paddingBottom: 12}}>{props.error}</View>}
            {props.loading && <Text style={[theme.type.tvMiniGuideNote, {color: theme.colors.textTertiary, paddingHorizontal: 12, paddingBottom: 12}]}>{t('next.browse.native.markAllPreparing')}</Text>}
            {rows.map((row, index) => <MiniGuideRow key={rowKey(row)} ref={index === entry ? first : undefined} id={rowKey(row)} channel={row.channel} title={row.now?.title} progress={progress(row)}
              logo={row.channel.logoUrl ? props.artwork?.(row.channel.logoUrl) : undefined} next={row.next && {title: row.next.title, at: clock(row.next.start, locale, props.timezone)}}
              watching={row.watching} preferredFocus={props.open && index === entry} disabled={!props.open || !!props.busy || !tuneable(row)} onPress={() => props.onSelect(row.channel)}/>)}
            <MiniGuideAll ref={entry < 0 ? first : undefined} disabled={!props.open || !!props.busy} onPress={props.onFullGuide}/>
          </FocusBoundary>
        </View>
      </View>
    </Animated.View>
  </Modal>;
}
