import React, {useCallback, useEffect, useRef} from 'react';
import {Modal, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {GuideChannelRow, GuideProgram} from '../../../../../packages/client-core/src/guide/types';
import {channelTint} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {shellLayout} from '../../shell/model';
import {Focusable} from '../../kit/controls';
import {FocusBoundary} from '../../kit/overlays/surface';
import {useOverlayLifecycle} from '../../kit/overlays/OverlayHost';
import {Icon} from '../../kit/Icon';
import {Artwork} from '../../kit/artwork/Artwork';
import type {ArtworkSource} from '../../kit/artwork/types';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {nativeType, useTheme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';
import {useTVPanelPresence, useTVPresentationCompletion} from '../../kit/overlays/TVPanelMotion';

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
 * channels beside it. TV only; the touch screen never mounts it. Focus enters on the
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
  const exited = useCallback((ticket: number) => {if (completion.current(ticket)) finished.current();}, [completion]), open = props.open;
  useEffect(() => {
    const ticket = completion.begin();
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
  const mark = (row: NativeMiniGuideRow) => {
    const logo = row.channel.logoUrl ? props.artwork?.(row.channel.logoUrl) : undefined;
    // Served mark first, exactly as the shell draws a channel's tile; else its served
    // logo; else its own number. No logo or text is ever made up.
    if (row.channel.mark) return <View style={{width: 60, height: 60, borderRadius: 60 * 8 / 34, marginRight: 20, overflow: 'hidden', backgroundColor: channelTint(row.channel.mark.seed), alignItems: 'center', justifyContent: 'center'}}>
      <Text numberOfLines={1} adjustsFontSizeToFit allowFontScaling={false} style={[nativeType({size: row.channel.mark.text.length > 2 ? 60 * 0.32 : 60 * 0.38, weight: 800, tracking: -.01, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{row.channel.mark.text}</Text>
      {logo ? <Artwork source={logo} sized={false} visible style={StyleSheet.absoluteFillObject}/> : null}</View>;
    if (logo) return <Artwork source={logo} width={60} height={60} style={{borderRadius: 14, overflow: 'hidden', marginRight: 20}}/>;
    return <View style={{width: 60, height: 60, borderRadius: 14, marginRight: 20, alignItems: 'center', justifyContent: 'center', backgroundColor: '#edf5fa14'}}>
      <Text numberOfLines={1} adjustsFontSizeToFit style={[nativeType({size: 20, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{row.channel.number}</Text></View>;
  };
  return <Modal transparent visible animationType="none" onRequestClose={props.onClose} statusBarTranslucent navigationBarTranslucent>
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
            <View style={{flexDirection: 'row', alignItems: 'baseline', justifyContent: 'space-between', paddingHorizontal: 12, paddingTop: 4, paddingBottom: 12}}>
              <Text style={[nativeType({size: 30, weight: 800, tracking: -.02, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('next.controls.guide')}</Text>
              <Text style={[nativeType({size: 19, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{t('next.live.miniGuideHint')}</Text>
            </View>
            {!!props.error && <View style={{paddingHorizontal: 12, paddingBottom: 12}}>{props.error}</View>}
            {props.loading && <Text style={[nativeType({size: 19, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, paddingHorizontal: 12, paddingBottom: 12}]}>{t('next.browse.native.markAllPreparing')}</Text>}
            {rows.map((row, index) => {
              const bar = progress(row), next = row.next, at = next ? clock(next.start, locale, props.timezone) : undefined, disabled = !props.open || props.busy || !tuneable(row);
              return <Focusable key={rowKey(row)} ref={index === entry ? first : undefined} focusId={`tv-mini-guide-${rowKey(row)}`} testID={`tv-mini-guide-${rowKey(row)}`}
                focusKind="row" focusRadius={16} hasTVPreferredFocus={props.open && index === entry} disabled={disabled} accessibilityRole="button"
                accessibilityLabel={[row.channel.name, row.channel.number, row.watching ? t('next.live.watching') : undefined,
                  row.now?.title ?? t('guide.noInformation'), next && at ? t('next.live.nextAt', {time: at}) : undefined, next?.title].filter(Boolean).join(', ')}
                accessibilityState={{selected: row.watching}}
                onPress={() => {if (!disabled) props.onSelect(row.channel);}}
                style={({focused}) => ({flexDirection: 'row', alignItems: 'center', height: 112, paddingHorizontal: 16, borderRadius: 16,
                  marginTop: index ? 4 : 0, opacity: row.channel.tuneAvailable ? 1 : .55,
                  backgroundColor: focused ? 'rgba(237,245,250,.16)' : row.watching ? 'rgba(237,245,250,.07)' : 'transparent'})}>
                {mark(row)}
                <View style={{flex: 1, minWidth: 0}}>
                  <View style={{flexDirection: 'row', alignItems: 'center'}}>
                    <Text numberOfLines={1} style={[nativeType({size: 19, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{row.channel.name}</Text>
                    <Text style={[nativeType({size: 19, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, marginLeft: 10}]}>{row.channel.number}</Text>
                    {row.watching && <Text style={[nativeType({size: 19, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.accentStrong, marginLeft: 'auto'}]}>{t('next.live.watching')}</Text>}
                  </View>
                  <Text numberOfLines={1} style={[nativeType({size: 25, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: row.now ? theme.colors.text : theme.colors.textTertiary, marginTop: 2}]}>{row.now?.title ?? t('guide.noInformation')}</Text>
                  <View style={{flexDirection: 'row', alignItems: 'center', gap: 14, marginTop: 8}}>
                    {bar !== undefined && <View style={{width: 150, height: 5, borderRadius: 3, backgroundColor: '#edf5fa26', overflow: 'hidden'}}>
                      <View style={{width: `${Math.round(bar * 100)}%`, height: '100%', backgroundColor: theme.colors.accentStrong}}/></View>}
                    {!!next && <Text numberOfLines={1} style={[nativeType({size: 18, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, flexShrink: 1}]}>
                      {at ? <>{t('next.live.nextAt', {time: at})}<Text style={{color: theme.colors.textSecondary}}>{`  ${next.title}`}</Text></> : next.title}</Text>}
                  </View>
                </View>
              </Focusable>;
            })}
            <Focusable ref={entry < 0 ? first : undefined} focusId="tv-mini-guide-all" testID="tv-mini-guide-all" focusKind="row" focusRadius={12} disabled={!props.open || props.busy}
              onPress={() => {if (props.open && !props.busy) props.onFullGuide();}} style={({focused}) => ({flexDirection: 'row', alignItems: 'center', gap: 6,
                paddingTop: 14, paddingHorizontal: 16, paddingBottom: 4, borderRadius: 12, backgroundColor: focused ? 'rgba(237,245,250,.12)' : 'transparent'})}>
              <Text style={[nativeType({size: 22, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('next.live.openFullGuide')}</Text>
              <Icon name="i-right" size={22} color={theme.colors.textSecondary}/>
            </Focusable>
          </FocusBoundary>
        </View>
      </View>
    </Animated.View>
  </Modal>;
}
