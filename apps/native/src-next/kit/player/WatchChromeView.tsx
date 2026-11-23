import React, {useEffect, useState, useSyncExternalStore} from 'react';
import {TVControlLabel} from './TVControlLabel';
import {Text, View} from 'react-native';
import {GestureDetector} from 'react-native-gesture-handler';
import Animated, {cancelAnimation, Easing, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import Svg, {Defs, LinearGradient, Path, Rect, Stop, Text as SvgText} from 'react-native-svg';
import type {PlaybackFence, SegmentMarker} from '../../../../../packages/player-core/src';
import {channelSeconds} from '../../../../../packages/client-core/src/playback/linear-protocol';
import {channelTint, playbackIdentity} from '../../../../../packages/client-core/src/presentation/kit-model';
import {clockText, durationText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable} from '../controls';
import {PlayerPage} from '../../pages/touch/player/PlayerPage';
import {SidewaysPlayerPage, type SidewaysPill} from '../../pages/touch/player/SidewaysPlayerPage';
import {TVPlayerPage} from '../../pages/tv/player/PlayerPage';
import {FocusRegion} from '../focus';
import {Icon, type IconName} from '../Icon';
import {TVGroupPill} from '../../pages/tv/player/GroupPillPage';
import {textLines, useTheme} from '../theme';
import {shellType} from '../shell/visual';
import {useReducedMotion} from '../useMotion';
import type {NativeWatchPanel} from '../../screens/watch/controls';
import {NativeWatchTimeline} from './WatchTimeline';
import type {LandingStore} from '../../screens/watch/landing-store';
import {PlayerUpNext as NativeUpNextCard} from './PlayerUpNext';
import {NativeStillWatchingCard} from './StillWatchingCard';
import {Artwork} from '../artwork';
import {nowNext} from '../../../../../packages/client-core/src/guide/now-next';
import {playbackUserMessage} from '../../../../../packages/client-core/src/playback-messages';
import {NativeMiniGuide} from '../live/TVMiniGuide';
import {PlayerLiveActions as TVLiveActions} from './PlayerLiveActions';
import type {NativeWatchChromeProps, useWatchChrome} from '../../pages/shared/player/useWatchChrome';

export type WatchChromeViewProps = ReturnType<typeof useWatchChrome>;

const NOTICE_SECONDS = 6;
/** tv-player/phone-player 5 and 13: one quiet line, once per playback, that never
 * takes focus and clears the top row. The plan's own words, not a code. */
function LossNotice({notice, top, gutter, available}: {notice: NonNullable<NativeWatchChromeProps['notice']>; top: number; gutter: number; available: number}) {
  const theme = useTheme(), reduced = useReducedMotion(), line = useSharedValue(1);
  const [expired, setExpired] = useState<string | undefined>(undefined);
  useEffect(() => {
    setExpired(undefined);
    line.value = 1;
    line.value = withTiming(0, {duration: reduced ? 0 : NOTICE_SECONDS * 1000, easing: Easing.linear});
    const timer = setTimeout(() => setExpired(notice.key), NOTICE_SECONDS * 1000);
    return () => {clearTimeout(timer); cancelAnimation(line);};
  }, [notice.key, line, reduced]);
  const drain = useAnimatedStyle(() => ({transform: [{scaleX: Math.max(0, Math.min(1, line.value))}]}));
  if (expired === notice.key) return null;
  const phone = !theme.isTV;
  const icon = notice.value.icon === 'picture' ? 'i-film' : 'i-audio';
  // playback-info 13: the phone's note is one 44 pt line inside a 28 pt glass
  // circle, inset 54 pt from the picture's right edge, with the remaining-time
  // line as a 2 pt foot (the lane's own override of the 3 pt base). It grows
  // only when a longer message needs a second line.
  return <View pointerEvents="none" accessibilityLiveRegion="polite" accessibilityRole="alert" testID="watch-loss-notice"
    style={{position: 'absolute', top, right: gutter, maxWidth: phone ? Math.max(0, available - gutter * 2) : 560, borderRadius: phone ? 12 : 20, overflow: 'hidden',
      paddingLeft: phone ? 8 : 20, paddingRight: phone ? 16 : 26, paddingTop: phone ? 0 : 14, paddingBottom: phone ? 0 : 18,
      ...(phone ? {minHeight: 44, justifyContent: 'center' as const, backgroundColor: 'rgba(12,25,35,.84)'} : {backgroundColor: 'rgba(9,19,27,.82)'}),
      boxShadow: phone ? 'inset 0 0 0 1px rgba(237,245,250,.1), 0 18px 36px -14px rgba(0,0,0,1)'
        : 'inset 0 0 0 1px rgba(237,245,250,.1), 0 18px 40px -18px rgba(0,0,0,.85)'}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: phone ? 10 : 14}}>
      {phone
        ? <View style={{width: 28, height: 28, borderRadius: 14, alignItems: 'center', justifyContent: 'center', backgroundColor: 'rgba(237,245,250,.1)'}}>
          <Icon name={icon} size={16} color={theme.colors.text}/>
        </View>
        : <Icon name={icon} size={24} color={theme.colors.textSecondary}/>}
      <Text style={[shellType(phone ? 13.5 : 24, 600, 'normal', 0, theme.textScale), {flexShrink: 1, color: theme.colors.textSecondary}]}>
        <Text style={[shellType(phone ? 13.5 : 24, 700, 'normal', 0, theme.textScale), {color: theme.colors.text}]}>{notice.value.lead}</Text>
        {` ${notice.value.detail}`}
      </Text>
    </View>
    {!reduced && (phone
      ? <Animated.View style={[{position: 'absolute', left: 0, bottom: 0, width: '100%', transformOrigin: 'left center', height: 2, backgroundColor: 'rgba(237,245,250,.38)'}, drain]}/>
      : <View style={{marginTop: 10, height: 2, borderRadius: 1, overflow: 'hidden', backgroundColor: 'rgba(237,245,250,.18)'}}>
        <Animated.View style={[{width: '100%', transformOrigin: 'left center', height: 2, backgroundColor: theme.colors.accentStrong}, drain]}/>
      </View>)}
  </View>;
}

function SkipIcon({seconds, forward, size, color}: {seconds: number; forward?: boolean; size: number; color: string}) {
  return <Svg width={size} height={size} color={color} viewBox="0 0 32 32" accessible={false}>
    <Path d={forward ? 'M25 10a11 11 0 1 1-8-4M25 3v7h-7' : 'M7 10a11 11 0 1 0 8-4M7 3v7h7'} stroke="currentColor" strokeWidth={2.3} fill="none" strokeLinecap="round" strokeLinejoin="round"/>
    <SvgText x={16} y={23} textAnchor="middle" {...(({fontFamily, fontSize}) => ({fontFamily, fontSize}))(shellType(11, 700, 'normal', 0, 1))} fill="currentColor">{seconds}</SvgText>
  </Svg>;
}

/** Portico controls over the shell's one retained surface, from what `useWatchChrome` returns. This component owns no video view. */
export function WatchChromeView({props, dismissUndo, source, controls, width, height, compact, t, wallClock, theme, motion, state, playback, registry, focus, toggle, picture, guideButton, castButton, liveGuide, liveActions, landingStore, scrubbing, setLanding, returnAt, now, fence, current, visible, liveCurrent, miniOpen, actionsCurrent, liveOverlayOpen, miniExited, controlsEnabled, controlsCurrent, initialPlayer, minimizeSwipe, chromeMorphStyle, seekFocused, groupPillPresent, groupPillHost, groupSheetChange, policy, segments, setHeldUndo, held, undoKey, notice, undoLineStyle, view, nextItem, endCard, prompt, visibleOffer, countdown, sceneAfter, credits, promptMotion, promptEntry, skipNoticeLabel, fade}: WatchChromeViewProps) {
  if (!fence || !current() || source.dock.local?.expanded !== true) return null;
  const act = (command: (expected: PlaybackFence) => Promise<void>) => {if (controlsCurrent()) {controls.reveal(fence); void command(fence).catch(props.reportError);}};
  const skipSegment = (marker: Pick<SegmentMarker, 'id'>) => {if (!controlsCurrent() || !playback.policySkip) return; if (theme.isTV) controls.hide(fence); if (controlsCurrent()) void playback.policySkip(fence, marker.id).catch(props.reportError);};
  const panel = (value: NativeWatchPanel) => {if (controlsCurrent() && controls.open(value, fence, registry.capture())) props.onPanel?.(value, fence);};
  const identity = source.subject ? playbackIdentity(source.subject, 'full', props.text) : undefined;
  const queue = fence.kind === 'queue' && (source.queue.view?.items.length ?? 0) > 1;
  // tv-player 11: a channel is the normal player with the guide's programme in it.
  const linear = fence?.kind === 'channel' ? source.playback.linear : undefined;
  const liveOffer = actionsCurrent ? liveActions.offer : undefined;
  const liveRow = liveOffer?.channel ?? source.channelGuide?.row ?? liveGuide.rows?.find(row => row.step === 0)?.channel;
  const channelNumber = liveRow?.number;
  const recordState = liveActions.recording?.state ?? liveOffer?.programme.recording?.state;
  const recordingLabel = (value?: string): string => {
    switch (value) {
      case 'scheduled': return t('guide.recordingScheduled');
      case 'conflicted': return t('web.dvr.state.conflicted');
      case 'waiting-source': return t('web.dvr.state.waitingSource');
      case 'waiting-guide': return t('web.dvr.state.waitingGuide');
      case 'preparing': return t('web.dvr.state.preparing');
      case 'recording': return t('player.live.recording');
      case 'finalizing': return t('web.dvr.state.finalizing');
      case 'completed': return t('web.dvr.state.completed');
      case 'incomplete-playable': return t('web.dvr.state.incomplete');
      case 'failed': return t('web.dvr.state.failed');
      case 'cancelled': return t('web.dvr.state.cancelled');
      case 'pending-delete': return t('web.dvr.state.pendingDelete');
      case 'deleted': return t('web.dvr.state.deleted');
      default: return t('player.live.record');
    }
  };
  const liveConfirmation = liveActions.confirmed === 'series' ? t('guide.seriesSet') : liveActions.recording && liveOffer
    ? ['scheduled', 'recording', 'preparing'].includes(liveActions.recording.state)
      ? t('player.live.scheduled', {title: liveActions.recording.programme.title}) : `${recordingLabel(liveActions.recording.state)}: ${liveActions.recording.programme.title}` : undefined;
  const liveEdge = linear ? channelSeconds(linear.media.liveEdgeUs) : undefined;
  const behindLive = liveEdge === undefined ? 0 : Math.max(0, liveEdge - source.playback.positionSeconds);
  const liveEdgeAction = theme.isTV && !!linear && behindLive > 5 && !!props.goLive && !scrubbing;
  const audioTracks = source.fence?.kind === 'local' ? source.localTracks?.audio.length ?? 0
    : source.options?.versions.find(version => version.id === source.session?.versionId)?.audio.length ?? source.options?.versions[0]?.audio.length ?? 0;
  const initials = (name: string) => name.split(/\s+/).filter(Boolean).slice(0, 3).map(word => word[0]!.toUpperCase()).join('');
  const channelMark = linear ? (linear.media.logoUrl
    ? <Artwork source={props.artwork?.(linear.media.logoUrl)} width={64} height={64} style={{borderRadius: 15, overflow: 'hidden', marginRight: 24}}/>
    : <View style={{width: 64, height: 64, borderRadius: 15, alignItems: 'center', justifyContent: 'center', marginRight: 24, backgroundColor: theme.colors.accentDeep}}>
        <Text style={[shellType(21, 800, 'normal', 0, theme.textScale), {color: '#fff'}]}>{initials(linear.name)}</Text></View>) : null;
  if (theme.isTV && state.panel && props.tvPanelRight !== undefined) return <View testID="watch-tv-panel-backdrop" pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{width, height}}>
    {identity && <View style={{position: 'absolute', left: 96, right: props.tvPanelRight, top: 80}}>
      <Text {...textLines(1)} style={[shellType(44, 800, 52, -.015, theme.textScale), {color: theme.colors.text}]}>{identity.title}</Text>
      <Text {...textLines(1)} style={[shellType(24, 600, 32, 0, theme.textScale), {color: theme.colors.textSecondary}]}>{identity.subtitle.map(part => part.text).join(' ')}</Text>
    </View>}
    <View style={{position: 'absolute', left: 96, right: props.tvPanelRight, bottom: 118}}>
      <NativeWatchTimeline source={source} controls={controls} playback={playback} compact={false} enabled={false} tv reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
    </View>
    <View style={{position: 'absolute', left: 96, right: props.tvPanelRight, bottom: 80, flexDirection: 'row', justifyContent: 'space-between'}}>
      <Text style={[shellType(24, 600, 32, 0, theme.textScale), {color: theme.colors.textSecondary}]}>{clockText(source.playback.positionSeconds)}</Text>
      <Text style={[shellType(24, 600, 32, 0, theme.textScale), {color: theme.colors.textSecondary}]}>{clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}</Text>
    </View>
    <View style={{position: 'absolute', inset: 0, backgroundColor: '#04090d80'}}/>
  </View>;
  // The retained view answers whether it can start PiP; an admitted `unavailable`
  // (a television, an unsupported engine) hides the tool it cannot honour.
  const pipOffered = !!props.onPictureInPicture && !!source.pictureInPicture && source.pictureInPicture.state !== 'unavailable';
  const tablet = props.tablet === true, tabletop = props.tabletop, bar = props.bar === true && !tabletop;
  // foldable5: the 508-wide column is the frame's 46/56/84 transport with space
  // between its rows; foldable4 stacks the same rows with the frame's 12/12/9/10
  // steps over an 80 centre and keeps the Episodes pill above the two rows.
  const stacked = tabletop === 'stacked';
  const side = tabletop ? 22 : undefined;
  const pictureControlsBottom = compact ? 8 : 15;
  const big = theme.isTV ? 88 : bar ? 62 : tabletop ? (stacked ? 80 : 84) : compact ? (tablet ? 84 : 58) : 74;
  const small = theme.isTV ? 64 : bar ? 48 : tabletop ? 56 : compact ? 46 : 58;
  const channelStep = (direction: -1 | 1) => {
    const target = liveCurrent ? direction < 0 ? liveGuide.previous : liveGuide.next : undefined;
    const size = theme.isTV ? small : bar ? 48 : 46;
    if (!target) return <View style={{width: size, height: size}}/>;
    const id = direction < 0 ? 'watch-channel-down' : 'watch-channel-up';
    return <TVControlLabel focused={controlsEnabled && focus?.childId === id} label={t(direction < 0 ? 'player.channelDown' : 'player.channelUp')} detail={[target.channel.number, target.channel.name].filter(Boolean).join(' ')}>
      <Focusable focusId={id} testID={id} accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled || liveGuide.busy}
        accessibilityLabel={t(direction < 0 ? 'next.live.channelDown' : 'next.live.channelUp', {channel: target.channel.name, number: target.channel.number})}
        onPress={() => {if (controlsCurrent()) void props.liveGuide?.tune(target, liveGuide, props.reportError);}}
        style={{width: size, height: size, borderRadius: size / 2, alignItems: 'center', justifyContent: 'center'}}>
        {theme.isTV && direction < 0 && !!target.channel.number && <Text style={[shellType(20, 800, 20, 0, theme.textScale), {color: theme.colors.textSecondary}]}>{target.channel.number}</Text>}
        <Icon name={direction < 0 ? 'i-chdown' : 'i-chup'} size={theme.isTV || bar ? 24 : 30} color={theme.colors.text}/>
        {!theme.isTV && bar && !!target.channel.number && <Text style={[theme.type.captionStrong, shellType(11, 600, 'normal', 0, 1), {color: theme.colors.textSecondary}]}>{target.channel.number}</Text>}
        {theme.isTV && direction > 0 && !!target.channel.number && <Text style={[shellType(20, 800, 20, 0, theme.textScale), {color: theme.colors.textSecondary}]}>{target.channel.number}</Text>}
      </Focusable>
    </TVControlLabel>;
  };
  // E-mobile-14b: Minimize is presentation only. It needs this screen current and not retired,
  // never the playback-control gates (a stopped or failed session still folds away).
  const minimizeCurrent = () => !source.retired && !playback.getSnapshot().retired;
  const icon = (id: string, name: IconName, label: string, onPress: () => void, primary = false, disabled = false, size?: number, gate: () => boolean = controlsCurrent) => <TVControlLabel focused={controlsEnabled && focus?.childId === `watch-${id}`} label={label} primary={primary} align={id === 'options' || id === 'pip' ? 'end' : 'center'}><Button ref={id === 'cast' ? castButton : undefined} focusId={`watch-${id}`} testID={`watch-${id}`} variant={primary ? 'primary' : theme.isTV ? 'icon' : 'text'} iconOnly icon={name} accessibilityLabel={label}
    // phone-player `.ib`: the player's own glyphs are the text colour; the `text` variant's accent is for words such as Clear.
    iconColor={primary || theme.isTV ? undefined : theme.colors.text}
    focusable={controlsEnabled} disabled={disabled || !controlsEnabled} onPress={() => {if (gate()) onPress();}} style={{width: size ?? (primary ? big : small), height: size ?? (primary ? big : small), borderRadius: (size ?? (primary ? big : small)) / 2}}
    iconSize={size ? Math.round(size * .6) : primary ? theme.isTV ? 40 : 28 : theme.isTV ? 26 : 22}/></TVControlLabel>;
  const liveButtons = (size?: number, compact = true) => linear && liveOffer && props.liveActions ? <TVLiveActions enabled={controlsEnabled} size={size} compact={compact}
    record={liveOffer.channel.kind === 'live' && liveOffer.channel.recordAvailable ? {label: recordingLabel(recordState), recording: recordState === 'recording',
      busy: liveActions.busy, disabled: liveActions.busy || !!liveActions.recording || !!liveOffer.programme.recording,
      onPress: () => {if (controlsCurrent()) void props.liveActions?.record(liveActions, props.reportError);}} : undefined}
    more={{disabled: liveActions.busy, onPress: () => {if (controlsCurrent()) props.liveActions?.openMore(liveActions);}}}/> : null;
  const skip = (forward: boolean) => <TVControlLabel focused={controlsEnabled && focus?.childId === (forward ? 'watch-skip-forward' : 'watch-skip-back')} label={t(forward ? 'player.skipForwardSeconds' : 'player.skipBackSeconds', {seconds: forward ? props.skipForwardSeconds : props.skipBackSeconds})}><Focusable focusId={forward ? 'watch-skip-forward' : 'watch-skip-back'} testID={forward ? 'watch-skip-forward' : 'watch-skip-back'} focusable={controlsEnabled} disabled={!controlsEnabled || !source.capabilities.seek} accessibilityRole="button"
    accessibilityLabel={t(forward ? 'player.skipForwardSeconds' : 'player.skipBackSeconds', {seconds: forward ? props.skipForwardSeconds : props.skipBackSeconds})}
    onPress={() => act(expected => playback.skip(expected, forward ? props.skipForwardSeconds : -props.skipBackSeconds))}
    style={{width: small, height: small, alignItems: 'center', justifyContent: 'center', opacity: source.capabilities.seek ? 1 : .4}}>
    <SkipIcon color={theme.colors.text} forward={forward} seconds={forward ? props.skipForwardSeconds : props.skipBackSeconds} size={theme.isTV ? 36 : compact ? 30 : 38}/>
  </Focusable></TVControlLabel>;
  const option = (name: NativeWatchPanel, label: string, value?: string) => <Focusable focusId={`watch-option-${name}`} testID={`watch-option-${name}`} accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled} onPress={() => panel(name)}
    style={({focused}) => theme.isTV
      ? {height: 64, minWidth: 92, maxWidth: 200, flexShrink: name === 'subtitles' ? 4 : name === 'quality' ? 0 : 1, paddingHorizontal: 18, justifyContent: 'center', borderRadius: 14, backgroundColor: state.panel === name ? 'rgba(237,245,250,.24)' : focused ? 'rgba(237,245,250,.28)' : 'rgba(237,245,250,.14)'}
      : {minHeight: 36, paddingHorizontal: 12, paddingVertical: 6, justifyContent: 'center', borderRadius: 8, backgroundColor: theme.colors.raisedHigh}}>
    {({focused}) => theme.isTV ? <><Text {...textLines(1)} style={[shellType(20, 700, 24, 0, theme.textScale), {color: theme.colors.text}]}>{label}</Text>{value && <Text {...textLines(1)} ellipsizeMode="tail" style={[shellType(20, 600, 24, 0, theme.textScale), {color: focused ? theme.colors.white : theme.colors.textSecondary}]}>{value}</Text>}</> : <Text style={[theme.type.caption, {color: theme.colors.text}]}>{value ? label ? `${label} ${value}` : value : label}</Text>}
  </Focusable>;
  // phone-player 1: the pill row — Episodes, Subtitles, Audio and the Quality chip that
  // shows the current choice, last on the right.
  const chip = (name: NativeWatchPanel, icon: IconName, label: string, value: string | undefined, onPress?: () => void, tall = false) => <Focusable key={`${name}:${label}`}
    focusId={onPress ? `watch-chip-${name}` : `watch-option-${name}`} testID={onPress ? `watch-chip-${name}` : `watch-option-${name}`} accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
    onPress={() => {if (!controlsCurrent()) return;if (onPress) onPress(); else panel(name);}} style={{height: tall ? 44 : 36, paddingLeft: tall ? 14 : 11, paddingRight: tall ? 18 : 14, borderRadius: tall ? 22 : 18, flexDirection: 'row', alignItems: 'center', gap: 7,
      backgroundColor: 'rgba(237,245,250,.14)'}}>
    <Icon name={icon} size={18} color={theme.colors.textSecondary}/>
    {!!label && <Text style={[shellType(13.5, 700, 'normal', 0, 1), {color: theme.colors.text}]}>{label}</Text>}
    {!!value && <Text style={[shellType(13.5, 600, 'normal', 0, 1), {marginLeft: label ? 3 : 0, color: label ? theme.colors.textTertiary : theme.colors.text}]}>{value}</Text>}
  </Focusable>;
  const qualityChip = (small = false) => <Focusable focusId="watch-option-quality" testID="watch-option-quality" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
    onPress={() => panel('quality')} style={small ? {height: 28, paddingLeft: 8, paddingRight: 10, borderRadius: 14, flexDirection: 'row', alignItems: 'center', gap: 5, backgroundColor: 'rgba(237,245,250,.16)'}
      : {height: 36, paddingLeft: 11, paddingRight: 14, borderRadius: 18, flexDirection: 'row', alignItems: 'center', gap: 6, backgroundColor: 'rgba(237,245,250,.14)'}}>
    <Icon name="i-quality" size={small ? 16 : 18} color={theme.colors.textSecondary}/>
    <Text style={[shellType(small ? 12.5 : 13.5, 700, 'normal', 0, 1), {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{props.quality || t('player.opt.quality')}</Text>
  </Focusable>;
  const keycap = (label: string, icons?: IconName[]) => <View style={{minWidth: 44, height: 36, paddingHorizontal: 10, borderRadius: 9, borderWidth: 1.5, borderColor: 'rgba(237,245,250,.3)', flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 2}}>
    {icons ? icons.map(name => <Icon key={name} name={name} size={20} color={theme.colors.textSecondary}/>) : <Text style={[shellType(17, 800, 'normal', 0, theme.textScale), {color: theme.colors.textSecondary}]}>{label}</Text>}
  </View>;
  const hint = (index: number, cap: React.ReactNode, text: React.ReactNode, key = String(text)) => <View key={key} style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginLeft: index ? 26 : 0}}>{cap}
    <Text style={[shellType(20, 600, 'normal', 0, theme.textScale), {color: theme.colors.textTertiary}]}>{text}</Text></View>;
  // states 11: while the failure owns the screen the bar's entry is its first real
  // bottom control, and Up reaches the failure block only when it drew one.
  const barEntry = theme.isTV && source.playback.phase === 'error' && fence
    ? fence.kind === 'queue' && fence.showId ? 'watch-option-episodes' : 'watch-option-quality' : 'watch-toggle';
  const barUp = theme.isTV && source.playback.phase === 'error' && props.failureAction === true;
  // states 11: only the centred transport gives way while nothing can play.
  const transport = source.playback.phase === 'error' && theme.isTV ? null : <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 22 : bar ? 14 : tabletop ? 36 : compact ? 12 : 38}}>
    {fence.kind === 'channel' ? channelStep(-1) : queue && icon('previous', 'i-prev', t('player.previous'), () => act(playback.previous), false, !source.capabilities.previous, bar ? 48 : theme.isTV || compact ? undefined : 46)}
    {skip(false)}
    <TVControlLabel focused={controlsEnabled && focus?.childId === 'watch-toggle'} label={t(source.playback.intent === 'playing' ? 'action.pause' : 'action.play')} primary><Button ref={toggle} testID="watch-toggle" focusId="watch-toggle" variant="primary" iconOnly icon={source.playback.intent === 'playing' ? 'i-pause' : 'i-play'}
      accessibilityLabel={t(source.playback.intent === 'playing' ? 'action.pause' : 'action.play')} preferredFocus={false}
      focusable={controlsEnabled} disabled={!controlsEnabled || !source.capabilities.play && !source.capabilities.pause} onPress={() => act(playback.toggle)}
      style={{width: big, height: big, borderRadius: big / 2}} iconSize={theme.isTV ? 40 : compact && tablet ? 34 : 28}/></TVControlLabel>
    {skip(true)}
    {fence.kind === 'channel' ? channelStep(1) : queue && icon('next', 'i-next', t('player.next'), () => act(playback.next), false, !source.capabilities.next, bar ? 48 : theme.isTV || compact ? undefined : 46)}
  </View>;
  // tv-player 3 / phone-player 2: the same prompt, notice and offer on a phone,
  // where the controls may be down and the note's line rides its bottom edge.
  const phone = !theme.isTV;
  const endCurrent = () => {const live = playback.getSnapshot();return controlsCurrent() && live.policy?.finished === true && live.policy.attention === policy?.attention && live.queue.view?.next.entryId === nextItem?.entryId;};
  // phone-player 6e: on handhelds, Still watching? is its own centred question over a dimmed picture.
  const stillWatching = phone && endCard && policy?.attention === 'still-watching' && !!nextItem;
  const stillWatchingNode = stillWatching && nextItem ? <View pointerEvents="box-none" style={{position: 'absolute', inset: 0, alignItems: 'center', justifyContent: 'center'}}>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0, backgroundColor: 'rgba(3,8,12,.42)'}}/>
    <NativeStillWatchingCard item={nextItem}
      onConfirm={() => {
        const live = playback.getSnapshot();
        if (!endCurrent() || live.policy?.attention !== 'still-watching' || !playback.policyAdvance || live.queue.view?.next.entryId !== nextItem.entryId) return;
        void playback.policyAdvance(fence, 'still-watching').catch(props.reportError);
      }}
      onReplay={() => {if (endCurrent()) void playback.replay(fence).catch(props.reportError);}}
      onClose={() => {if (endCurrent()) void playback.stop(fence).catch(props.reportError);}}/>
  </View> : undefined;
  const skipOfferNode = !stillWatching && (endCard || visibleOffer) && nextItem ? <NativeUpNextCard compact={phone} item={nextItem} artwork={props.artwork?.(nextItem.stillUrl ?? nextItem.backdropUrl)} offer={visibleOffer}
    countdown={countdown} countdownTotal={view?.postPlay.countdownSeconds ?? 10} sceneAfter={!endCard && sceneAfter} onPlayNow={() => {
      const live = playback.getSnapshot(), mode = endCard && policy?.attention === 'still-watching' ? 'still-watching' : 'manual';
      if (!controlsCurrent() || !playback.policyAdvance || !live.queue.view?.next.available || live.queue.view.next.entryId !== nextItem.entryId
        || endCard && (!live.policy?.finished || (live.policy.attention === 'still-watching') !== (mode === 'still-watching'))) return;
      if (theme.isTV) controls.hide(fence);
      void playback.policyAdvance(fence, mode).catch(props.reportError);
    }}
    onWatchCredits={() => {if (controlsCurrent() && playback.policyDismiss?.(fence) && theme.isTV) controls.hide(fence);}} onSkipCredits={() => {if (credits) skipSegment(credits);}}
    postPlay={endCard ? {
      onCancel: () => {if (endCurrent() && playback.getSnapshot().policy?.endCountdown) playback.policyCancelAutoplay?.(fence);},
      onReplay: () => {if (endCurrent()) void playback.replay(fence).catch(props.reportError);},
      onClose: () => {if (endCurrent()) void playback.stop(fence).catch(props.reportError);},
    } : undefined}/> : undefined;
  const undoSkip = () => {
    if (!notice || !controlsCurrent()) return;
    const current = playback.getSnapshot().policy?.undo;
    if (theme.isTV) controls.hide(fence);
    if (current?.marker.id === notice.marker.id && playback.policyUndo) {setHeldUndo(undefined); void playback.policyUndo(fence).catch(props.reportError); return;}
    // A held screen-reader note outlived the policy's window: return to where the segment began.
    if (held && held.marker.id === notice.marker.id) {dismissUndo(); void playback.seek(fence, held.marker.startSeconds).catch(props.reportError);}
  };
  const skipNoticeNode = notice ? <View accessible accessibilityLabel={skipNoticeLabel}
    accessibilityActions={[{name: 'activate', label: t('action.undo')}, {name: 'dismiss', label: t('next.overlays.native.dismissToast')}]}
    onAccessibilityAction={event => {if (event.nativeEvent.actionName === 'activate') undoSkip(); else if (event.nativeEvent.actionName === 'dismiss') dismissUndo();}}
    style={{flexDirection: 'row', alignItems: 'center', gap: phone ? 14 : 28, paddingLeft: phone ? 16 : 30, paddingRight: 6, paddingVertical: phone ? 6 : 16,
    borderRadius: phone ? 12 : 22, overflow: 'hidden', backgroundColor: phone ? 'rgba(7,16,24,.76)' : 'rgba(9,19,27,.8)',
    boxShadow: `inset 0 0 0 1px rgba(237,245,250,.1), 0 14px 30px -12px rgba(0,0,0,.8)`}}>
    <View style={{gap: 8}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: phone ? 8 : 12}}>
        {!phone && <Icon name="i-check" size={26} color={theme.colors.accentStrong}/>}
        <Text style={phone ? [shellType(14.5, 700, 'normal', 0, 1), {color: theme.colors.textSecondary}] : [shellType(24, 700, 'normal', 0, theme.textScale), {color: theme.colors.text}]}>{t('player.skipped', {kind: notice.marker.kind})}</Text>
      </View>
      {!phone && <View style={{height: 3, width: 240, borderRadius: 2, backgroundColor: 'rgba(237,245,250,.18)'}}>
        <Animated.View style={[{width: '100%', transformOrigin: 'left center', height: 3, borderRadius: 2, backgroundColor: theme.colors.accentStrong}, undoLineStyle]}/>
      </View>}
    </View>
    <Button focusId="watch-skip-undo" testID="watch-skip-undo" variant={phone ? 'glass' : 'glass'} size={phone ? 'compact' : 'hero'} preferredFocus={false} icon="i-back" iconSize={phone ? 18 : undefined}
      style={phone ? {height: 36, paddingHorizontal: 14, borderRadius: 8} : undefined} textStyle={phone ? shellType(14.5, 700, 'normal', 0, 1) : undefined}
      onPress={undoSkip}>{t('action.undo')}</Button>
    {phone && <Animated.View style={[{position: 'absolute', left: 0, bottom: 0, width: '100%', transformOrigin: 'left center', height: 2, backgroundColor: 'rgba(237,245,250,.45)'}, undoLineStyle]}/>}
  </View> : undefined;
  const paintedPrompt = promptMotion.value;
  const skipPromptNode = paintedPrompt ? <Animated.View pointerEvents={promptMotion.interactive ? 'box-none' : 'none'} accessibilityElementsHidden={!promptMotion.interactive} importantForAccessibility={promptMotion.interactive ? 'auto' : 'no-hide-descendants'} style={[{flexDirection: 'row', alignItems: 'center', gap: 22}, promptMotion.bodyStyle]}>{paintedPrompt.kind === 'credits' && sceneAfter && !phone && <View style={{paddingHorizontal: 18, paddingVertical: 12, borderRadius: 12, backgroundColor: 'rgba(9,19,27,.78)'}}>
      <Text style={[shellType(22, 600, 'normal', 0, theme.textScale), {color: theme.colors.text}]}>{t('player.post.sceneAfter')}</Text>
    </View>}
    <Button focusId="watch-skip-action" testID="watch-skip-action" disabled={!promptMotion.interactive} focusable={promptMotion.interactive} pressDurationMs={90} pressScale={.96} variant="primary" size={phone ? 'compact' : 'hero'} preferredFocus={false} icon="i-next" iconSize={phone ? 18 : undefined}
      style={phone ? {height: 44, paddingHorizontal: 16, borderRadius: 10, opacity: 1} : {opacity: 1}} textStyle={phone ? shellType(15, 700, 'normal', 0, 1) : undefined}
      onPress={promptMotion.interactive && prompt ? () => skipSegment(prompt) : undefined}>{t('player.skip', {kind: paintedPrompt.kind})}</Button></Animated.View> : undefined;
  const skipNodes = skipOfferNode ?? skipNoticeNode ?? skipPromptNode;
  if (!theme.isTV && compact && width < 600 && !tablet && !linear && !props.edgeInsets?.right) {
    const admitted = !initialPlayer || initialPlayer.controlsAdmitted;
    return <GestureDetector gesture={minimizeSwipe}><Animated.View testID="watch-chrome" pointerEvents={admitted ? 'box-none' : 'none'}
      accessibilityElementsHidden={!admitted} importantForAccessibility={admitted ? 'auto' : 'no-hide-descendants'} style={[{width, height}, chromeMorphStyle]}>
      <PlayerPage chromeVisible={visible} controlsEnabled={controlsEnabled} scrubbing={scrubbing}
        picture={<Focusable ref={picture} testID="watch-picture" accessibilityRole="button" accessibilityLabel={identity?.title ?? t('player.opt.menu')}
          onPress={() => {if (controlsCurrent()) controls.toggleVisibility(fence);}} style={{flex: 1}}/>}
        onMinimize={() => {if (minimizeCurrent()) props.onMinimize(fence);}}
        onQuality={() => panel('quality')} quality={props.quality || t('player.opt.quality')}
        onFullWindow={props.fullWindowRequest?.() && controls.canFullWindow() && !state.fullWindow ? () => {
          if (controlsCurrent() && props.fullWindowRequest?.()) controls.setFullWindow(fence, true);
        } : undefined}
        tools={<>
          {props.onCast && icon('cast', 'i-cast', t('player.playOn'), () => props.onCast!(fence, castButton))}
          {pipOffered && icon('pip', 'i-pip', t('player.pictureInPicture'), () => props.onPictureInPicture!(fence))}
          {icon('options', 'i-sliders', t('player.opt.menu'), () => panel('options'))}
        </>}
        transport={props.waiting || stillWatching ? null : transport}
        times={{position: clockText(source.playback.positionSeconds) ?? '', duration: clockText(source.playback.duration)}}
        scrubber={<NativeWatchTimeline source={source} controls={controls} playback={playback} artwork={props.artwork} compact
          enabled={controlsEnabled} onPreview={setLanding} reportError={props.reportError} skipBackSeconds={props.skipBackSeconds}
          skipForwardSeconds={props.skipForwardSeconds}/>}
        prompts={skipOfferNode ? undefined : skipNodes} card={skipOfferNode} onControlsLayout={props.onPictureControlsLayout}/>
      {props.notice && state.panel === undefined && <LossNotice notice={props.notice} top={64} gutter={theme.gutter} available={width}/>}
      {stillWatchingNode}
    </Animated.View></GestureDetector>;
  }
  // tablet-play1: an expanded window draws the desktop player's two bars — a top
  // bar with the way out, the title and the tools, and a bottom bar with the seek,
  // the times and one row holding the pills either side of a centred transport.
  if (bar) return <GestureDetector gesture={minimizeSwipe}><Animated.View testID="watch-chrome" pointerEvents={initialPlayer && !initialPlayer.controlsAdmitted ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer && !initialPlayer.controlsAdmitted} importantForAccessibility={initialPlayer && !initialPlayer.controlsAdmitted ? 'no-hide-descendants' : 'auto'} style={[{width, height}, chromeMorphStyle]}>
    {/* finisher 21 Nov 14:30: a tap on the picture brings the hidden controls back (and hides them), as on every other form. */}
    <FocusRegion id="watch-surface" entry="watch-picture" style={{position: 'absolute', inset: 0}}><Focusable ref={picture} focusId="watch-picture" testID="watch-picture" focusTreatment="none" accessibilityRole="button" accessibilityLabel={identity?.title ?? t('player.opt.menu')}
      focusable={false} onPress={() => {if (controlsCurrent()) controls.toggleVisibility(fence);}} style={{position: 'absolute', inset: 0}}/></FocusRegion>
    <Animated.View pointerEvents={visible ? 'box-none' : 'none'} accessibilityElementsHidden={!visible} importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={[{position: 'absolute', inset: 0}, fade]}>
      <FocusRegion id="watch-controls" orientation="horizontal" entry="watch-toggle" style={{position: 'absolute', inset: 0}}>
        <View style={{flex: 1}}>
          <View style={{height: 115, paddingTop: 26, paddingHorizontal: 32, flexDirection: 'row', alignItems: 'flex-start', gap: 16}}>
            {icon('minimize', 'i-down', t('player.minimize'), () => props.onMinimize(fence), false, false, 48, minimizeCurrent)}
            {linear && <View testID="watch-live-identity" style={{flexDirection: 'row', alignItems: 'flex-start', gap: 14, flexShrink: 1, minWidth: 0, paddingTop: 2}}>
              <View style={{width: 48, height: 48, borderRadius: 11, overflow: 'hidden', alignItems: 'center', justifyContent: 'center', backgroundColor: liveRow?.mark ? channelTint(liveRow.mark.seed) : '#edf5fa14'}}>
                <Text {...textLines(1)} style={[shellType(13, 800, 'normal', -.01, theme.textScale), {color: theme.colors.text}]}>{liveRow?.mark?.text ?? channelNumber}</Text>
                {linear.media.logoUrl && <Artwork source={props.artwork?.(linear.media.logoUrl)} width={48} height={48} style={{position: 'absolute', inset: 0}}/>}
              </View>
              <View style={{flexShrink: 1, minWidth: 0}}>
                <Text {...textLines(1)} style={[shellType(26, 800, 'normal', -.03, theme.textScale), {color: theme.colors.text}]}>{linear.programme?.title ?? linear.name}</Text>
                <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 1}}>
                  <Text {...textLines(1)} style={[shellType(15, 700, 'normal', 0, theme.textScale), {color: theme.colors.textSecondary, flexShrink: 1}]}>{linear.name}</Text>
                  {!!channelNumber && <Text style={[theme.type.caption, shellType(15, 400, 'normal', 0, 1), {color: theme.colors.textSecondary}]}>{channelNumber}</Text>}
                  <View style={{flexDirection: 'row', alignItems: 'center', gap: 6}}><View style={{width: 7, height: 7, borderRadius: 3.5, backgroundColor: theme.colors.record}}/>
                    <Text style={[theme.type.captionStrong, shellType(13.5, 600, 'normal', 0, 1), {color: theme.colors.record}]}>{t('next.kit.live')}</Text></View>
                </View>
                {linear.programme && <Text {...textLines(1)} style={[theme.type.caption, shellType(15, 400, 'normal', 0, 1), {marginTop: 4, color: theme.colors.textSecondary}]}>{t('next.kit.range', {start: wallClock(linear.programme.startMs), end: wallClock(linear.programme.endMs)})}</Text>}
              </View>
            </View>}
            {!linear && identity && <View style={{flexShrink: 1, minWidth: 0, paddingTop: 2}}>
              <Text {...textLines(1)} style={[shellType(26, 800, 'normal', -.03 / 26, 1), {color: theme.colors.text}]}>{identity.title}</Text>
              <Text {...textLines(1)} style={[shellType(15, 600, 'normal', 0, 1), {marginTop: 1, color: theme.colors.textSecondary}]}>{identity.subtitle.map(part => part.text).join('  ')}</Text>
            </View>}
            <View style={{flex: 1}}/>
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
              {props.onCast && !linear && icon('cast', 'i-cast', t('player.playOn'), () => props.onCast!(fence, castButton), false, false, 48)}
              {pipOffered && icon('pip', 'i-pip', t('player.pictureInPicture'), () => props.onPictureInPicture!(fence), false, false, 48)}
              {icon('options', 'i-sliders', t('player.opt.menu'), () => panel('options'), false, false, 48)}
              {liveButtons(48, false)}
            </View>
          </View>
          <View style={{marginTop: 'auto', height: 164}}>
            <View style={{height: 44, marginHorizontal: 32}}>
              <NativeWatchTimeline source={source} controls={controls} playback={playback} compact={false} enabled={controlsEnabled} liveEdge={liveEdge} onPreview={setLanding}
                reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
            </View>
            <View style={{height: 18, marginHorizontal: 32, flexDirection: 'row', alignItems: 'center'}}>
              <Text style={[theme.type.bodyStrong, shellType(13, 600, 'normal', 0, 1), {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{clockText(source.playback.positionSeconds)}</Text>
              <View style={{flex: 1}}/>
              {linear && behindLive > 5 && props.goLive
                ? <Focusable focusId="watch-go-live" testID="watch-go-live" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
                    onPress={() => {if (controlsCurrent() && props.goLive) {controls.reveal(fence); props.goLive(fence);}}}
                    style={{height: 18, paddingHorizontal: 8, borderRadius: 9, flexDirection: 'row', alignItems: 'center', gap: 6, backgroundColor: 'rgba(237,245,250,.14)'}}>
                    <View style={{width: 6, height: 6, borderRadius: 3, backgroundColor: theme.colors.record}}/>
                    <Text style={[shellType(12, 700, 'normal', 0, 1), {color: theme.colors.text}]}>{t('player.live.goLive')}</Text>
                  </Focusable>
                : <Text style={[theme.type.bodyStrong, shellType(13, 600, 'normal', 0, 1), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}</Text>}
            </View>
            <View style={{height: 62, marginTop: 8}}>
              <View pointerEvents="box-none" style={{position: 'absolute', left: 32, right: 32, top: 0, height: 62, flexDirection: 'row', alignItems: 'center'}}>
                {linear
                  ? (props.onGuide ? chip('episodes', 'i-list', t('player.live.guide'), undefined, () => {if (controlsCurrent() && props.onGuide) props.onGuide();}, true) : null)
                  : fence.kind === 'queue' && fence.showId ? chip('episodes', 'i-stack', t('title.episodes'), undefined, undefined, true) : null}
                <View style={{flex: 1}}/>
                {source.capabilities.subtitleTrack && chip('subtitles', 'i-cc', linear ? t('player.live.captions') : t('player.opt.subtitles'), props.subtitle ?? (linear ? t('player.opt.off') : undefined), undefined, true)}
                {source.capabilities.audioTrack && audioTracks > 1 && chip('audio', 'i-audio', t('title.audio'), props.audio, undefined, true)}
                {chip('quality', 'i-quality', t('player.opt.quality'), props.quality, undefined, true)}
              </View>
              <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, top: 0, height: 62, alignItems: 'center', justifyContent: 'center'}}>{transport}</View>
            </View>
          </View>
        </View>
      </FocusRegion>
    </Animated.View>
    {stillWatchingNode}
    {skipNodes && <View pointerEvents="box-none" style={{position: 'absolute', right: 32, bottom: 180}}>
      <View testID="watch-skip-prompt" style={{flexDirection: 'row', alignItems: 'center', gap: 22}}>{skipNodes}</View>
    </View>}
    {props.notice && state.panel === undefined && <LossNotice notice={props.notice} top={115} gutter={32} available={width}/>}
  </Animated.View></GestureDetector>;
  if (tabletop) return <GestureDetector gesture={minimizeSwipe}><Animated.View testID="watch-chrome" pointerEvents={initialPlayer && !initialPlayer.controlsAdmitted ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer && !initialPlayer.controlsAdmitted} importantForAccessibility={initialPlayer && !initialPlayer.controlsAdmitted ? 'no-hide-descendants' : 'auto'} style={[{width, height}, chromeMorphStyle]}>
    <Animated.View pointerEvents={visible ? 'box-none' : 'none'} accessibilityElementsHidden={!visible} importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={[{position: 'absolute', inset: 0}, fade]}>
      <FocusRegion id="watch-controls" orientation="horizontal" entry="watch-toggle" style={{position: 'absolute', inset: 0}}>
        <View style={{flex: 1, flexDirection: 'column', justifyContent: stacked ? 'flex-start' : 'space-between', paddingTop: stacked ? 12 : 0}}>
          <View style={{height: 44, flexDirection: 'row', alignItems: 'center', gap: 6}}>
            {icon('minimize', 'i-down', t('player.minimize'), () => props.onMinimize(fence), false, false, 44, minimizeCurrent)}
            {!linear && identity && <View style={{flexShrink: 1, minWidth: 0, marginLeft: 8}}>
              <Text {...textLines(1)} style={[shellType(20, 800, 'normal', -.025 / 20, 1), {color: theme.colors.text}]}>{identity.title}</Text>
              <Text {...textLines(1)} style={[shellType(13.5, 600, 'normal', 0, 1), {marginTop: 1, color: theme.colors.textSecondary}]}>{identity.subtitle.map(part => part.text).join('  ')}</Text>
            </View>}
            <View style={{flex: 1}}/>
            {props.onCast && !linear && icon('cast', 'i-cast', t('player.playOn'), () => props.onCast!(fence, castButton), false, false, 44)}
            {pipOffered && icon('pip', 'i-pip', t('player.pictureInPicture'), () => props.onPictureInPicture!(fence), false, false, 44)}
            {icon('options', 'i-sliders', t('player.opt.menu'), () => panel('options'), false, false, 44)}
            {liveButtons(44)}
          </View>
          <View style={{height: 44, flexDirection: 'row', alignItems: 'center', gap: 14, marginTop: stacked ? 12 : 0}}>
            <Text style={[theme.type.bodyStrong, shellType(12.5, 600, 'normal', 0, 1), {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{clockText(source.playback.positionSeconds)}</Text>
            <View style={{flex: 1}}>
              <NativeWatchTimeline source={source} controls={controls} playback={playback} compact={false} enabled={controlsEnabled} liveEdge={liveEdge} onPreview={setLanding}
                reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
            </View>
            <Text style={[theme.type.bodyStrong, shellType(12.5, 600, 'normal', 0, 1), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}</Text>
          </View>
          <View style={{height: stacked ? 80 : 84, alignItems: 'center', justifyContent: 'center', marginTop: stacked ? 9 : 0}}>{transport}</View>
          <View style={{height: 44, flexDirection: 'row', alignItems: 'center', gap: 8, marginTop: stacked ? 10 : 0}}>
            {(stacked || linear) && (linear
              ? (props.onGuide ? chip('episodes', 'i-list', t('player.live.guide'), undefined, () => {if (controlsCurrent() && props.onGuide) props.onGuide();}, true) : null)
              : fence.kind === 'queue' && fence.showId ? chip('episodes', 'i-stack', t('title.episodes'), undefined, undefined, true) : null)}
            <View style={{flex: 1}}/>
            {source.capabilities.subtitleTrack && chip('subtitles', 'i-cc', linear ? t('player.live.captions') : t('player.opt.subtitles'), props.subtitle ?? (linear ? t('player.opt.off') : undefined), undefined, true)}
            {source.capabilities.audioTrack && audioTracks > 1 && chip('audio', 'i-audio', t('title.audio'), props.audio, undefined, true)}
            {chip('quality', 'i-quality', t('player.opt.quality'), props.quality, undefined, true)}
          </View>
        </View>
      </FocusRegion>
    </Animated.View>
    {stillWatchingNode}
    {skipNodes && <View pointerEvents="box-none" style={{position: 'absolute', left: 22, right: 22, bottom: 52}}>
      <View testID="watch-skip-prompt" style={{flexDirection: 'row', alignItems: 'center', gap: 22}}>{skipNodes}</View>
    </View>}
    {props.notice && state.panel === undefined && <LossNotice notice={props.notice} top={4} gutter={22} available={width}/>}
  </Animated.View></GestureDetector>;
  if (theme.isTV) {
    const group = <FocusRegion id="watch-group" entry="player-group-pill" orientation="horizontal" enabled={controlsEnabled && groupPillPresent}
      edges={{down: 'watch-seek-region', right: linear && liveOffer && props.liveActions ? 'watch-live-actions' : undefined}} stops={{up: true}}>
      <TVGroupPill onHost={groupPillHost} onSheetChange={groupSheetChange}/>
    </FocusRegion>;
    const liveTools = linear && liveOffer && props.liveActions ? <FocusRegion id="watch-live-actions" entry="watch-live-more" orientation="horizontal"
      enabled={controlsEnabled} edges={{down: 'watch-seek-region', left: groupPillPresent ? 'watch-group' : undefined}} stops={{up: true}}>{liveButtons()}</FocusRegion> : undefined;
    const titleData = linear ? {
      title: linear.programme?.title ?? linear.name, subtitle: [linear.name, channelNumber].filter(Boolean).join('  '),
      mark: channelMark, live: t('next.kit.live'),
      detail: [linear.programme && t('next.kit.range', {start: wallClock(linear.programme.startMs), end: wallClock(linear.programme.endMs)}),
        linear.next && t('player.live.next', {title: linear.next.title})].filter(Boolean).join('  '),
      accessory: group,
    } : identity ? {
      title: identity.title, subtitle: identity.subtitle.map(part => part.text).join('  '), accessory: group,
    } : {accessory: group};
    const held = scrubbing && seekFocused;
    const remainingSeconds = Math.max(0, source.playback.duration - source.playback.positionSeconds);
    const remaining = clockText(remainingSeconds);
    const seek = <FocusRegion id="watch-seek-region" entry="watch-seek" stops={{left: true, right: true, up: !groupPillPresent && !liveTools}}
      edges={{up: groupPillPresent ? 'watch-group' : liveTools ? 'watch-live-actions' : undefined, down: liveEdgeAction ? 'watch-live-edge' : 'watch-transport'}}>
      <NativeWatchTimeline source={source} controls={controls} playback={playback} artwork={props.artwork} compact={compact} enabled={controlsEnabled} tv
        focused={seekFocused} onPreview={setLanding} liveEdge={liveEdge} reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
    </FocusRegion>;
    const markers = held ? [...new Map<string, SegmentMarker>(segments.filter(segment => segment.kind === 'intro' || segment.kind === 'recap' || segment.kind === 'credits')
      .map(segment => [segment.kind, segment] as const)).values()].map(segment => {
      const at = source.playback.duration > 0 ? Math.min(1, segment.startSeconds / source.playback.duration) : 0;
      const label = segment.kind === 'intro' ? t('player.marker.intro') : segment.kind === 'recap' ? t('player.marker.recap') : t('player.marker.credits');
      return <Text key={segment.kind} style={[theme.type.playerTime, {position: 'absolute', color: theme.colors.textSecondary},
        at >= .9 ? {right: 0} : {left: `${at * 100}%`}]}>{label}</Text>;
    }) : undefined;
    const hints = held ? <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center'}}>
      {hint(0, keycap('', ['i-left', 'i-right']), t('player.hint.move'))}
      {hint(1, keycap(t('player.hint.hold')), t('player.hint.faster'))}
      {hint(2, keycap(t('title.ok')), <LandingTime store={landingStore} fallback={returnAt}/>, 'play-from')}
      {hint(3, keycap(t('action.back')), t('player.hint.returnTo', {time: clockText(returnAt)}))}
    </View> : undefined;
    const prompt = skipNodes && <Animated.View pointerEvents="box-none" style={skipNodes === skipPromptNode ? promptMotion.positionStyle : undefined}>
      <FocusRegion id="watch-skip" orientation="horizontal" entry={promptEntry} edges={visible ? {left: 'watch-transport'} : undefined}
        screenStart={{regionId: 'watch-surface', childId: 'watch-picture'}}>
        <View testID="watch-skip-prompt" style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>{skipNodes}</View>
      </FocusRegion>
    </Animated.View>;
    return <GestureDetector gesture={minimizeSwipe}><Animated.View testID="watch-chrome" pointerEvents={initialPlayer && !initialPlayer.controlsAdmitted ? 'none' : 'box-none'}
      accessibilityElementsHidden={!!initialPlayer && !initialPlayer.controlsAdmitted}
      importantForAccessibility={initialPlayer && !initialPlayer.controlsAdmitted ? 'no-hide-descendants' : 'auto'} style={[{width, height}, chromeMorphStyle]}>
      <TVPlayerPage testID="watch-tv-frame" chromeVisible={visible} identity={titleData} tools={liveTools} picture={<FocusRegion id="watch-surface" entry="watch-picture" style={{position: 'absolute', inset: 0}}><Focusable ref={picture} focusId="watch-picture" testID="watch-picture" focusTreatment="none" accessibilityRole="button" accessibilityLabel={identity?.title ?? t('player.opt.menu')}
      preferredFocus={!visible && !liveOverlayOpen && !promptEntry} focusable={!visible && !state.panel && !liveOverlayOpen} onPress={() => {if (controlsCurrent()) controls.key(fence);}} style={{position: 'absolute', inset: 0}}/></FocusRegion>}
        focus={{controls: {id: 'watch-controls', entryRegion: 'watch-transport'},
          transport: {id: 'watch-transport', orientation: 'horizontal', entry: barEntry, stops: {down: true},
            edges: {up: barUp ? 'watch-failed' : liveEdgeAction ? 'watch-live-edge' : 'watch-seek-region', right: promptEntry ? 'watch-skip' : undefined}}}}
        scrubber={seek} scrubbing={held} markers={markers} hints={hints}
        times={{position: clockText(source.playback.positionSeconds) ?? '',
          remaining: remaining === undefined ? undefined : linear ? remaining : `-${remaining}`,
          behind: linear && behindLive > 30 ? t('player.live.behindLive', {duration: durationText(Math.max(60, Math.round(behindLive / 60) * 60), t)}) : undefined,
          clock: !linear ? wallClock(now) : undefined,
          endsAt: !linear ? t('next.watch.ends', {time: wallClock(now + remainingSeconds * 1000)}) : undefined}}
        timeAction={liveEdgeAction ? <FocusRegion id="watch-live-edge" entry="watch-go-live" edges={{up: 'watch-seek-region', down: 'watch-transport'}} enabled={controlsEnabled}><Focusable focusId="watch-go-live" testID="watch-go-live" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
                  onPress={() => {if (controlsCurrent() && props.goLive) {controls.reveal(fence); props.goLive(fence);}}}
                  style={{height: 48, paddingHorizontal: 20, borderRadius: 12, flexDirection: 'row', alignItems: 'center', gap: 12, backgroundColor: 'rgba(237,245,250,.14)'}}>
                  <View style={{width: 12, height: 12, borderRadius: 6, backgroundColor: theme.colors.record}}/>
                  <Text style={[shellType(21, 700, 'normal', 0, theme.textScale), {color: theme.colors.text}]}>{t('player.live.goLive')}</Text>
                </Focusable></FocusRegion> : undefined}
        transport={transport} prompts={skipOfferNode ? undefined : prompt} card={skipOfferNode ? prompt : undefined}
        leading={linear ? (props.onGuide ? <Focusable ref={guideButton} focusId="watch-guide" testID="watch-guide" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled} onPress={() => {if (controlsCurrent() && props.onGuide) props.onGuide();}}
                    style={{height: 64, paddingHorizontal: 26, borderRadius: 32, flexDirection: 'row', alignItems: 'center', gap: 12, backgroundColor: 'rgba(237,245,250,.14)'}}>
                    <Icon name="i-list" size={28} color={theme.colors.text}/><Text style={[shellType(22, 600, 'normal', 0, theme.textScale), {color: theme.colors.text}]}>{t('player.live.guide')}</Text>
                  </Focusable> : null) : fence.kind === 'queue' && fence.showId && option('episodes', t('title.episodes'))}
        trailing={<>
          {source.capabilities.subtitleTrack && option('subtitles', linear ? t('player.live.captions') : t('player.opt.subtitles'), props.subtitle ?? (linear ? t('player.opt.off') : undefined))}
          {source.capabilities.audioTrack && audioTracks > 1 && option('audio', t('title.audio'), props.audio)}
          {option('quality', t('player.opt.quality'), props.quality)}
          {icon('options', 'i-sliders', t('player.opt.menu'), () => panel('options'))}
          {pipOffered && icon('pip', 'i-pip', t('player.pictureInPicture'), () => props.onPictureInPicture!(fence))}
        </>}/>
      {props.notice && state.panel === undefined && <LossNotice notice={props.notice} top={168} gutter={96} available={width}/>}
      {stillWatchingNode}
      {visible && actionsCurrent && liveActions.confirmed && liveConfirmation && !liveOverlayOpen && <View testID="watch-live-confirmation" pointerEvents="none"
        accessibilityRole="alert" accessibilityLiveRegion="polite" style={{position: 'absolute', right: 96, top: 168, maxWidth: width - 192, flexDirection: 'row', alignItems: 'center', gap: 14,
          paddingVertical: 16, paddingHorizontal: 24, borderRadius: 16, backgroundColor: 'rgba(16,33,45,.94)',
          boxShadow: '0 24px 60px -18px rgba(0,0,0,.85), inset 0 0 0 1px rgba(237,245,250,.1)'}}>
        <View style={{width: 14, height: 14, borderRadius: 7, backgroundColor: theme.colors.record}}/>
        <Text {...textLines(1)} style={[shellType(23, 700, 'normal', 0, theme.textScale), {color: theme.colors.text, flexShrink: 1}]}>{liveConfirmation}</Text>
      </View>}
      {props.liveGuide && liveGuide.timezone && <NativeMiniGuide key={JSON.stringify(fence)} open={miniOpen} onExited={miniExited} rows={liveGuide.rows.map(row => ({channel: row.channel, ...nowNext(row.programmes, now), watching: row.step === 0}))}
        now={now} timezone={liveGuide.timezone} loading={liveGuide.phase === 'loading' || !!liveGuide.programmesLoading && liveGuide.rows.every(row => !row.programmes.length)} busy={liveGuide.busy} artwork={props.artwork}
        onSelect={channel => {const row = liveGuide.rows.find(value => value.channel === channel);if (current() && row) void props.liveGuide?.tune(row, liveGuide);}}
        onClose={() => {if (current()) props.liveGuide?.close(liveGuide);}} onFullGuide={() => {if (current()) props.liveGuide?.fullGuide(liveGuide);}}
        error={liveGuide.error ? <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{playbackUserMessage(liveGuide.error)}</Text> : undefined}/>}
    </Animated.View></GestureDetector>;
  }
  // phone-player 1: a phone turns sideways only in the video player, and there it is the frame's sideways form.
  if (!theme.isTV && !compact && !tablet && !linear && width > height && height < 600) {
    const admitted = !initialPlayer || initialPlayer.controlsAdmitted;
    const pill = (id: NativeWatchPanel, icon: IconName, name: string, value?: string): SidewaysPill => ({id, icon, name, value, open: state.panel === id});
    return <GestureDetector gesture={minimizeSwipe}><Animated.View testID="watch-chrome" pointerEvents={admitted ? 'box-none' : 'none'}
      accessibilityElementsHidden={!admitted} importantForAccessibility={admitted ? 'auto' : 'no-hide-descendants'} style={[{width, height}, chromeMorphStyle]}>
      <SidewaysPlayerPage chromeVisible={visible} scrubbing={scrubbing}
        identity={identity ? {title: identity.title, subtitle: identity.subtitle.map(part => part.text).join('  ')} : undefined}
        picture={<Focusable ref={picture} testID="watch-picture" accessibilityRole="button" accessibilityLabel={identity?.title ?? t('player.opt.menu')}
          onPress={() => {if (controlsCurrent()) controls.toggleVisibility(fence);}} style={{flex: 1}}/>}
        onMinimize={() => {if (minimizeCurrent()) props.onMinimize(fence);}}
        controlsEnabled={controlsEnabled} castRef={castButton} onOptions={() => {if (controlsCurrent()) panel('options');}}
        onCast={props.onCast ? () => {if (controlsCurrent()) props.onCast!(fence, castButton);} : undefined}
        onPictureInPicture={pipOffered ? () => {if (controlsCurrent()) props.onPictureInPicture!(fence);} : undefined}
        transport={props.waiting || stillWatching ? null : transport}
        times={{position: clockText(source.playback.positionSeconds) ?? '',
          remaining: `-${clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}`}}
        scrubber={<NativeWatchTimeline source={source} controls={controls} playback={playback} artwork={props.artwork} compact={false}
          enabled={controlsEnabled} onPreview={setLanding} reportError={props.reportError} skipBackSeconds={props.skipBackSeconds}
          skipForwardSeconds={props.skipForwardSeconds}/>}
        leading={fence.kind === 'queue' && fence.showId ? pill('episodes', 'i-stack', t('title.episodes')) : undefined}
        pills={[
          ...(source.capabilities.subtitleTrack ? [pill('subtitles', 'i-cc', t('player.opt.subtitles'), props.subtitle)] : []),
          ...(source.capabilities.audioTrack && audioTracks > 1 ? [pill('audio', 'i-audio', t('title.audio'), props.audio)] : []),
          pill('quality', 'i-quality', t('player.opt.quality'), props.quality),
        ]} onPill={id => {if (controlsCurrent()) panel(id as NativeWatchPanel);}}
        prompts={skipOfferNode ? undefined : skipNodes} card={skipOfferNode} onControlsLayout={props.onPictureControlsLayout}/>
      {props.notice && state.panel === undefined && <LossNotice notice={props.notice} top={64} gutter={54} available={width}/>}
      {stillWatchingNode}
    </Animated.View></GestureDetector>;
  }
  return <GestureDetector gesture={minimizeSwipe}><Animated.View testID="watch-chrome" pointerEvents={initialPlayer && !initialPlayer.controlsAdmitted ? 'none' : 'box-none'} accessibilityElementsHidden={!!initialPlayer && !initialPlayer.controlsAdmitted} importantForAccessibility={initialPlayer && !initialPlayer.controlsAdmitted ? 'no-hide-descendants' : 'auto'} style={[{width, height}, chromeMorphStyle]}>
    <FocusRegion id="watch-surface" entry="watch-picture" style={{position: 'absolute', inset: 0}}><Focusable ref={picture} focusId="watch-picture" testID="watch-picture" focusTreatment="none" accessibilityRole="button" accessibilityLabel={identity?.title ?? t('player.opt.menu')}
      focusable={false} onPress={() => {if (controlsCurrent()) controls.toggleVisibility(fence);}} style={{position: 'absolute', inset: 0}}/></FocusRegion>
    <Animated.View pointerEvents={visible ? 'box-none' : 'none'} accessibilityElementsHidden={!visible} importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={[{position: 'absolute', inset: 0}, fade]}>
      <Svg pointerEvents="none" width="100%" height="100%" style={{position: 'absolute', inset: 0}}><Defs>
        <LinearGradient id="watch-top" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor="#03080c" stopOpacity={.82}/><Stop offset="0.36" stopColor="#03080c" stopOpacity={0}/></LinearGradient>
        <LinearGradient id="watch-bottom" x1="0" y1="1" x2="0" y2="0"><Stop offset="0" stopColor="#03080c" stopOpacity={.92}/><Stop offset="0.5" stopColor="#03080c" stopOpacity={0}/></LinearGradient>
      </Defs><Rect width="100%" height="100%" fill="url(#watch-top)"/><Rect width="100%" height="100%" fill="url(#watch-bottom)"/></Svg>
      <FocusRegion id="watch-controls" orientation="horizontal" entry={barEntry} style={{position: 'absolute', inset: 0}}>
        <View style={{position: 'absolute', top: (compact ? 4 : 12) + (props.edgeInsets?.top ?? 0), left: (side ?? (compact ? 6 : 54)) + (props.edgeInsets?.left ?? 0), right: (side ?? (compact ? 6 : 54)) + (props.edgeInsets?.right ?? 0), flexDirection: 'row', alignItems: 'center'}}>
          {icon('minimize', 'i-down', t('player.minimize'), () => props.onMinimize(fence), false, false, undefined, minimizeCurrent)}
          {linear && <View style={{flexShrink: 1, minWidth: 0}}>
            <Text {...textLines(1)} style={[shellType(16.5, 800, 'normal', -.015, theme.textScale), {color: theme.colors.text}]}>{linear.programme?.title ?? linear.name}</Text>
            <View style={{flexDirection: 'row', alignItems: 'center', minWidth: 0, marginTop: 1}}>
              <Text {...textLines(1)} style={[theme.type.bodyStrong, shellType(13, 600, 'normal', 0, 1), {color: theme.colors.textSecondary, flexShrink: 0}]}>{linear.name}</Text>
              <View style={{flexDirection: 'row', alignItems: 'center', gap: 5, marginLeft: 10}}>
                <View style={{width: 7, height: 7, borderRadius: 3.5, backgroundColor: theme.colors.record}}/>
                <Text style={[shellType(12, 600, 'normal', 0, 1), {color: theme.colors.text}]}>{t('next.kit.live')}</Text>
              </View>
            </View>
          </View>}
          {!linear && !compact && identity && <View style={{flexShrink: 1, minWidth: 0, marginLeft: 8}}>
            <Text {...textLines(1)} style={[shellType(16.5, 800, 'normal', -.015, theme.textScale), {color: theme.colors.text}]}>{identity.title}</Text>
            <Text {...textLines(1)} style={[theme.type.bodyStrong, shellType(13, 600, 'normal', 0, 1), {marginTop: 1, color: theme.colors.textSecondary}]}>{identity.subtitle.map(part => part.text).join('  ')}</Text>
          </View>}
          <View style={{flex: 1}}/>
          {props.onCast && !linear && icon('cast', 'i-cast', t('player.playOn'), () => props.onCast!(fence, castButton))}
          {pipOffered && icon('pip', 'i-pip', t('player.pictureInPicture'), () => props.onPictureInPicture!(fence))}
          {icon('options', 'i-sliders', t('player.opt.menu'), () => panel('options'))}
          {liveButtons(compact ? 46 : 48)}
        </View>
        {!props.waiting && <View style={{position: 'absolute', left: 0, right: 0, top: height / 2 - big / 2, alignItems: 'center'}}>{transport}</View>}
        <View testID="watch-picture-controls" onLayout={event => props.onPictureControlsLayout?.(event.nativeEvent.layout.height + pictureControlsBottom)}
          style={{position: 'absolute', left: (side ?? (compact ? 14 : 60)) + (props.edgeInsets?.left ?? 0), right: (side ?? (compact ? 6 : 60)) + (props.edgeInsets?.right ?? 0), bottom: pictureControlsBottom}}>
          {!compact && <View style={{marginBottom: 8}}>
            <NativeWatchTimeline source={source} controls={controls} playback={playback} artwork={props.artwork} compact={compact} enabled={controlsEnabled} liveEdge={liveEdge} onPreview={setLanding} reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
          </View>}
          <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
            <Text style={[theme.type.bodyStrong, shellType(12.5, 600, 'normal', 0, 1), {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{clockText(source.playback.positionSeconds)}</Text>
            {linear && behindLive > 30 && <Text style={[theme.type.bodyStrong, shellType(12, 600, 'normal', 0, 1), {marginLeft: 8, color: theme.colors.textSecondary}]}>{t('player.live.behindLive', {duration: durationText(Math.max(60, Math.round(behindLive / 60) * 60), t)})}</Text>}
            {/* A channel has no length to show: its upright row reads the position (and how far behind live), never "/ 0:00". */}
            {compact && !linear && <Text style={[theme.type.bodyStrong, shellType(12, 600, 'normal', 0, 1), {color: theme.colors.textSecondary}]}>{`/ ${clockText(source.playback.duration)}`}</Text>}
            <View style={{flex: 1}}/>
            {!compact && (linear && behindLive > 5 && props.goLive
              ? <Focusable focusId="watch-go-live" testID="watch-go-live" accessibilityRole="button" focusable={controlsEnabled} disabled={!controlsEnabled}
                  onPress={() => {if (controlsCurrent() && props.goLive) {controls.reveal(fence); props.goLive(fence);}}}
                  style={{height: 28, paddingHorizontal: 12, borderRadius: 14, flexDirection: 'row', alignItems: 'center', gap: 8, backgroundColor: 'rgba(237,245,250,.14)'}}>
                  <View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.record}}/>
                  <Text style={[shellType(12.5, 700, 'normal', 0, 1), {color: theme.colors.text}]}>{t('player.live.goLive')}</Text>
                </Focusable>
              : <Text style={[theme.type.bodyStrong, shellType(12.5, 600, 'normal', 0, 1), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{clockText(Math.max(0, source.playback.duration - source.playback.positionSeconds))}</Text>)}
            {compact && !linear && <>{qualityChip(true)}{props.fullWindowRequest?.() && controls.canFullWindow() && !controls.getSnapshot().fullWindow && icon('fullscreen', 'i-fs', t('next.states.shortcut.fullscreen'), () => {if (current() && props.fullWindowRequest?.()) controls.setFullWindow(fence, true);})}</>}
          </View>
          {compact && linear && <View testID="watch-live-pills" style={{flexDirection: 'row', alignItems: 'center', marginTop: 6, gap: 8}}>
            {props.onGuide && chip('episodes', 'i-list', t('player.live.guide'), undefined, props.onGuide)}
            <View style={{flex: 1}}/>{qualityChip(true)}
            {props.fullWindowRequest?.() && controls.canFullWindow() && !controls.getSnapshot().fullWindow && icon('fullscreen', 'i-fs', t('next.states.shortcut.fullscreen'), () => {if (current() && props.fullWindowRequest?.()) controls.setFullWindow(fence, true);})}
          </View>}
          {/* phone-player 1b: while you drag, the chips step aside (kept in place, hidden) until you let go. */}
          {!compact && <View testID="watch-chip-row" pointerEvents={scrubbing ? 'none' : 'auto'} accessibilityElementsHidden={scrubbing} importantForAccessibility={scrubbing ? 'no-hide-descendants' : 'auto'}
            style={{flexDirection: 'row', alignItems: 'center', marginTop: 10, gap: 8, opacity: scrubbing ? 0 : 1}}>
            {linear
              ? (props.onGuide ? chip('episodes', 'i-list', t('player.live.guide'), undefined, () => {if (controlsCurrent() && props.onGuide) props.onGuide();}) : null)
              : fence.kind === 'queue' && fence.showId ? chip('episodes', 'i-stack', t('title.episodes'), undefined) : null}
            <View style={{flex: 1}}/>
            {source.capabilities.subtitleTrack && chip('subtitles', 'i-cc', linear ? t('player.live.captions') : t('player.opt.subtitles'), props.subtitle ?? (linear ? t('player.opt.off') : undefined))}
            {source.capabilities.audioTrack && audioTracks > 1 && chip('audio', 'i-audio', t('title.audio'), props.audio)}
            {qualityChip()}
          </View>}
        </View>
        {compact && <View style={{position: 'absolute', left: (props.edgeInsets?.left ?? 0), right: (props.edgeInsets?.right ?? 0), bottom: -21}}>
          <NativeWatchTimeline source={source} controls={controls} playback={playback} artwork={props.artwork} compact enabled={controlsEnabled} onPreview={setLanding} reportError={props.reportError} skipBackSeconds={props.skipBackSeconds} skipForwardSeconds={props.skipForwardSeconds}/>
        </View>}
      </FocusRegion>
    </Animated.View>
    {props.notice && state.panel === undefined && <LossNotice notice={props.notice} top={64} gutter={54} available={width}/>}
    {stillWatchingNode}
    {skipNodes && <Animated.View pointerEvents="box-none" style={[{position: 'absolute', right: 60,
      bottom: skipNodes === skipPromptNode && !motion.reduce ? 20 : visible ? 110 : 20}, skipNodes === skipPromptNode ? promptMotion.positionStyle : undefined]}>
      <View testID="watch-skip-prompt" style={{flexDirection: 'row', alignItems: 'center', gap: 22}}>{skipNodes}</View>
    </Animated.View>}
  </Animated.View></GestureDetector>;
}

/** The only node a held scrub re-renders in the chrome: "Play from 12:34". */
function LandingTime({store, fallback}: {store: LandingStore; fallback: number}) {
  const landing = useSyncExternalStore(store.subscribe, store.get, store.get);
  return <>{defaultI18n.t('player.hint.playFrom', {time: clockText(landing ?? fallback)})}</>;
}
