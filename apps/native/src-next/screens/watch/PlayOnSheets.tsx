import React, {useEffect, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../kit/artwork';
import {Button, Focusable} from '../../kit/controls';
import type {CastDockIdentity} from '../../kit/dock';
import {BottomSheet, Dialog, Popover} from '../../kit/overlays';
import {nativeType, useTheme} from '../../kit/theme';
import {Icon, type IconName} from '../../kit/Icon';
import {hasAirPlay, isTablet} from '../../platform/device-platform';
import {NativeAirPlayRoutePicker} from '../../platform/play-on/AudioRoutePicker';
import type {NativePlayOn, PlayOnRemote} from '../../platform/play-on/controller';
import {NativePlayOnRemote} from './PlayOnRemote';

/** phone-player 7/tablet-play 13: real system destinations, then the receivers
 * this profile may send to. The one shell controller owns the remote afterwards. */
export type NativePlayOnSheetsProps = Readonly<{
  controller?: NativePlayOn;
  artwork: (path?: string) => ArtworkSource | undefined;
  ambient?: (path?: string) => AmbientArtSource | undefined;
  airPlayVideo?: boolean;
  skipBackSeconds: number;
  skipForwardSeconds: number;
  onLinkTV: () => void;
}>;

/** Product names are supplied only for known advertised platforms. Never show an
 * unrecognised wire/platform value as viewer-facing technical text. */
export function playOnPlatformName(platform: string): string | undefined {
  const t = defaultI18n.t;
  switch (platform) {
    case 'tvos': return t('services.platform.tvos');
    case 'androidtv': return t('services.platform.androidtv');
    case 'firetv': case 'vega': return t('services.platform.firetv');
    case 'ios': case 'ipados': return t('services.platform.ios');
    case 'android': return t('services.platform.android');
    default: return undefined;
  }
}
function receiverIcon(platform: string): IconName {
  return platform === 'ipados' ? 'i-tablet' : platform === 'ios' || platform === 'android' ? 'i-phone' : 'i-tvdev';
}

export function NativePlayOnSheets({controller, artwork, ambient, airPlayVideo, skipBackSeconds, skipForwardSeconds, onLinkTV}: NativePlayOnSheetsProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const state = useSyncExternalStore(controller?.subscribe ?? (() => () => {}), controller?.getSnapshot ?? (() => undefined));
  const [trackPanel, setTrackPanel] = useState<Readonly<{kind: 'audio' | 'subtitles'; control: CastDockIdentity}>>();
  useEffect(() => () => controller?.close(), [controller]);
  if (!controller || !state) return null;
  const cast = state.cast, castState = cast?.cast, castReport = cast?.receiver;
  if (castState?.takeover) return <Dialog open title={t('cast.takeover.title')} onOpenChange={open => {if (!open) controller.castCommand('deny-takeover', undefined, undefined, castState.takeover);}}>
    <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('cast.takeover.body', {requester: castState.takeover.requester})}</Text>
    <Button variant="glass" onPress={() => controller.castCommand('deny-takeover', undefined, undefined, castState.takeover)}>{t('cast.takeover.deny')}</Button>
    <Button variant="primary" onPress={() => controller.castCommand('allow-takeover', undefined, undefined, castState.takeover)}>{t('action.accept')}</Button>
  </Dialog>;
  const castRemoteId = cast?.resumed?.id ?? cast?.source?.id;
  const isCast = !!castRemoteId && (!!state.castRemoteOpen || !state.remote);
  const castKnown = !!castReport && castReport.itemId === castState?.itemId && !castState?.controlPending;
  const castControls = castState?.phase === 'casting' && castKnown && !!cast?.control && controller.cast.viewIsCurrent(cast.control);
  const remote: PlayOnRemote | undefined = isCast && castRemoteId ? {
    id: castRemoteId, device: castState?.deviceName ?? t('cast.theTV'), title: cast?.title, itemId: castReport?.itemId,
    entry: castReport?.itemId === cast?.source?.snapshot.entry?.id ? cast?.source?.snapshot.entry : undefined,
    positionSeconds: castReport?.positionSeconds ?? 0, durationSeconds: castReport?.durationSeconds ?? 0,
    positionKnown: castKnown, playing: !!castControls && !castReport?.paused, stopped: cast?.stopped === true,
    allows: {pause: !!castControls, resume: !!castControls, seek: !!castControls && (castReport?.durationSeconds ?? 0) > 0,
      stop: castState?.phase !== 'off' && !castState?.controlPending && !!cast?.control},
  } : state.remote;
  const remoteOpen = isCast ? state.castRemoteOpen : state.remoteOpen;
  const currentRemote = () => isCast ? !!cast?.control && controller.cast.viewIsCurrent(cast.control)
    : !!remote && controller.getSnapshot().remote?.id === remote.id && controller.getSnapshot().remote?.itemId === remote.itemId;
  const closeRemote = () => {setTrackPanel(undefined); if (isCast) controller.closeCastRemote(); else controller.closeRemote();};
  const seek = (position: number) => {
    if (!remote || !currentRemote()) return;
    if (isCast) controller.castCommand('seek', Math.max(0, Math.min(castReport?.durationSeconds || Number.POSITIVE_INFINITY, position)), cast?.control);
    else void controller.command('seek', position, remote.id);
  };
  const skip = (seconds: number) => {
    if (!remote || !currentRemote()) return;
    if (isCast) {
      const report = controller.getSnapshot().cast?.receiver;
      if (report && report.itemId === remote.itemId) seek(report.positionSeconds + seconds);
    } else void controller.skip(seconds, remote.id);
  };
  const openTracks = (kind: 'audio' | 'subtitles') => {
    if (cast?.control && currentRemote()) setTrackPanel({kind, control: cast.control});
  };
  const tracks = trackPanel?.kind === 'audio' ? castState?.audioTracks : castState?.textTracks;
  const panel = trackPanel && remoteOpen && castControls && controller.cast.viewIsCurrent(trackPanel.control) && tracks?.length ? <BottomSheet open
    testID="remote-tracks" title={t(trackPanel.kind === 'audio' ? 'title.audio' : 'player.opt.subtitles')}
    onOpenChange={open => {if (!open) setTrackPanel(undefined);}} fitContent>
    {trackPanel.kind === 'subtitles' && <Button testID="remote-subtitles-off" variant="text" onPress={() => {
      controller.cast.selectTrack('subtitles', null, trackPanel.control); setTrackPanel(undefined);
    }}>{t('player.opt.off')}</Button>}
    {tracks.map((track, index) => <Button key={track.id} testID={`remote-track-${track.id}`} variant="text" onPress={() => {
      controller.cast.selectTrack(trackPanel.kind, track.id, trackPanel.control); setTrackPanel(undefined);
    }}>{track.name || track.language || t('next.kit.ordinal', {number: index + 1})}</Button>)}
  </BottomSheet> : undefined;
  const artPath = remote?.entry?.stillUrl ?? remote?.entry?.backdropUrl ?? remote?.entry?.posterUrl ?? (isCast ? cast?.posterUrl : undefined);
  const remoteSheet = remote ? <NativePlayOnRemote key={remote.id} open={!!remoteOpen} remote={remote} artwork={artwork(artPath)} ambient={ambient?.(artPath)}
    skipBackSeconds={skipBackSeconds} skipForwardSeconds={skipForwardSeconds} close={closeRemote} seek={seek} skip={skip} panel={panel}
    pause={() => {if (currentRemote()) {if (isCast) controller.castCommand('pause', undefined, cast?.control); else void controller.pause(remote.id);}}}
    resume={() => {if (currentRemote()) {if (isCast) controller.castCommand('resume', undefined, cast?.control); else void controller.resume(remote.id);}}}
    startOver={() => seek(0)} stop={() => {if (currentRemote()) {if (isCast) controller.castCommand('stop', undefined, cast?.control); else void controller.stop(remote.id);}}}
    audio={castControls && !!castState?.audioTracks.length ? () => openTracks('audio') : undefined}
    subtitles={castControls && !!castState?.textTracks.length ? () => openTracks('subtitles') : undefined}/> : null;
  if (!state.open) return remoteSheet;
  const tablet = isTablet && !theme.isTV;
  const rowStyle = {minHeight: tablet ? 52 : 58, paddingVertical: 6, paddingHorizontal: 12, borderRadius: tablet ? 10 : 12,
    flexDirection: 'row' as const, alignItems: 'center' as const, gap: tablet ? 12 : 14};
  const titleStyle = [nativeType({size: tablet ? 15 : 16, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}];
  const helperStyle = [nativeType({size: tablet ? 12.5 : 13, weight: 500, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, marginTop: 1}];
  const airPlayReady = hasAirPlay && airPlayVideo && !state.sending && !castState?.transferPending
    && !['connecting', 'pairing', 'waiting', 'casting'].includes(castState?.phase ?? '');
  const directory = <View style={{paddingVertical: tablet ? 0 : 8}}>
    {airPlayReady && <View testID="play-on-airplay" style={rowStyle}>
      <Icon name="i-airplay" size={tablet ? 22 : 24} color={theme.colors.textSecondary}/>
      <View style={{flex: 1}}><Text style={titleStyle}>{t('playOn.airplay')}</Text><Text style={helperStyle}>{t('playOn.airplaySubtitle')}</Text></View>
      <NativeAirPlayRoutePicker testID="play-on-airplay-picker" accessibilityLabel={t('playOn.airplay')} style={{width: 44, height: 44}}/>
    </View>}
    {castState?.available && <Focusable focusId="play-on-cast" testID="play-on-cast" focusKind="row" accessibilityRole="button"
      accessibilityLabel={t('playOn.googleCast')} disabled={state.sending || castState.transferPending || ['connecting', 'pairing', 'waiting'].includes(castState.phase)}
      onPress={castState.phase === 'casting' ? controller.openCastRemote : controller.startCast}
      style={({focused}) => ({...rowStyle, backgroundColor: focused ? 'rgba(237,245,250,.12)' : 'transparent'})}>
      <Icon name="i-cast" size={tablet ? 22 : 24} color={theme.colors.textSecondary}/><View style={{flex: 1}}>
        <Text style={titleStyle}>{t('playOn.googleCast')}</Text><Text style={helperStyle}>{t('playOn.chooseDevice')}</Text>
      </View><Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
    </Focusable>}
    {castState && (['connecting', 'pairing', 'waiting'].includes(castState.phase) || castState.transferPending) && <View style={{flexDirection: 'row', gap: 12, alignItems: 'center', paddingVertical: 12}}>
      <Text style={[theme.type.body, {flex: 1, color: theme.colors.textSecondary}]}>{castState.phase === 'connecting' ? t('playOn.chooseDevice') : castState.phase === 'waiting' ? t('cast.waitingForAllow') : t('playOn.connectingTo', {device: castState.deviceName ?? t('cast.theTV')})}</Text>
      <Button variant="glass" onPress={() => controller.castCommand('stop', undefined, cast?.control)}>{t('action.cancel')}</Button>
    </View>}
    {castState?.phase === 'error' && <Text style={[theme.type.body, {color: theme.colors.textSecondary, paddingVertical: 12}]}>{castState.error ?? t('cast.error.unreachable')}</Text>}
    <Text testID="play-on-network" accessibilityRole="header" style={[nativeType({size: 13, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale),
      {color: theme.colors.textTertiary, paddingHorizontal: 12, paddingTop: 10, paddingBottom: 6}]}>{t('playOn.onThisNetwork')}</Text>
    {state.directoryPhase === 'loading' && !state.receivers.length && <Text style={[theme.type.body, {color: theme.colors.textTertiary, paddingVertical: 14}]}>{t('playOn.looking')}</Text>}
    {state.directoryPhase === 'error' && <Text style={[theme.type.body, {color: theme.colors.textSecondary, paddingVertical: 14}]}>{state.directoryError || t('playOn.devicesUnavailable')}</Text>}
    {state.receivers.map(receiver => <Focusable key={receiver.id} focusId={`play-on-${receiver.id}`} testID={`play-on-${receiver.id}`} focusKind="row" accessibilityRole="button"
      accessibilityLabel={`${receiver.displayName}, ${receiver.kind === 'portico' ? t('playOn.porticoDevice') : t('playOn.castDevice')}`}
      disabled={state.sending || castState?.transferPending || castState?.phase === 'casting'} onPress={() => {void controller.send(receiver);}}
      style={({focused}) => ({...rowStyle, backgroundColor: focused ? 'rgba(237,245,250,.12)' : 'transparent'})}>
      <Icon name={receiverIcon(receiver.platform)} size={tablet ? 22 : 24} color={theme.colors.textSecondary}/>
      <View style={{flex: 1}}><Text style={titleStyle}>{receiver.displayName}</Text>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10}}>
          <Text style={helperStyle}>{receiver.kind === 'portico' && playOnPlatformName(receiver.platform)
            ? t('next.cast.receiverOn', {platform: playOnPlatformName(receiver.platform)!})
            : receiver.kind === 'portico' ? t('playOn.porticoDevice') : t('playOn.castDevice')}</Text>
          {receiver.kind !== 'portico' && playOnPlatformName(receiver.platform) && <Text style={helperStyle}>{playOnPlatformName(receiver.platform)}</Text>}
          {receiver.presence === 'offline' && <Text style={helperStyle}>{t('playOn.offline')}</Text>}
        </View>
      </View><Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
    </Focusable>)}
    {state.directoryPhase === 'ready' && !state.receivers.length && <Text style={[theme.type.body, {color: theme.colors.textTertiary, paddingVertical: 14}]}>{t('playOn.noDevices')}</Text>}
    {tablet && <View style={{height: 1, marginVertical: 6, marginHorizontal: 10, backgroundColor: 'rgba(237,245,250,.08)'}}/>}
    <Focusable focusId="play-on-link" testID="play-on-link" focusKind="row" accessibilityRole="button" onPress={onLinkTV}
      style={({focused}) => ({...rowStyle, backgroundColor: focused ? 'rgba(237,245,250,.12)' : 'transparent'})}>
      <Icon name="i-tv" size={22} color={theme.colors.textSecondary}/>
      <View style={{flex: 1}}><Text style={titleStyle}>{t('playOn.linkTV')}</Text><Text style={helperStyle}>{t('next.cast.linkTvHint')}</Text></View>
      <Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
    </Focusable>
    <Text testID="play-on-handover" style={[...helperStyle, {paddingHorizontal: 12, paddingTop: 8, paddingBottom: tablet ? 6 : 0}]}>{t('playOn.stillHereUntilTakeover')}</Text>
    {state.phase !== 'idle' && state.phase !== 'done' && state.phase !== 'failed' && <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingVertical: 12}}>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary, flex: 1}]}>{state.message ?? t('playOn.connectingTo', {device: state.target?.displayName ?? ''})}</Text>
      <Button focusId="play-on-cancel" variant="glass" onPress={controller.close}>{t('action.cancel')}</Button>
    </View>}
    {state.phase === 'failed' && <View style={{paddingVertical: 12}}>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{state.error ?? state.message ?? t('playOn.notReachable', {device: state.target?.displayName ?? ''})}</Text>
    </View>}
  </View>;
  return <>{remoteSheet}{tablet && state.anchorRef ? <Popover open testID="play-on-sheet" title={t('player.playOn')} showTitle anchorRef={state.anchorRef}
    onOpenChange={open => {if (!open) controller.close();}} width={360} placement="bottom-end" offset={8} modal backdropOpacity={0}
    style={{borderRadius: 16, backgroundColor: 'rgba(22,44,58,.96)'}} contentStyle={{padding: 6}}
    titleType={{size: 13, weight: 700, tracking: 0, lineHeight: 'normal'}} titleTextStyle={{color: theme.colors.textTertiary, paddingHorizontal: 12, paddingTop: 10, paddingBottom: 6}}>
    <ScrollView keyboardShouldPersistTaps="handled">{directory}</ScrollView>
  </Popover> : <Dialog open testID="play-on-sheet" title={t('player.playOn')} onOpenChange={open => {if (!open) controller.close();}} showClose
    description={state.message ?? t('playOn.chooseDevice')}>{directory}</Dialog>}</>;
}
