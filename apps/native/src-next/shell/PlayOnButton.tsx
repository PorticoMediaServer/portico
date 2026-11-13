import React, {useEffect, useRef, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import {Focusable} from '../kit/controls';
import {Icon} from '../kit/Icon';
import {RoundBarButton} from '../kit/layout/TopBar';
import {useTheme} from '../kit/theme';
import type {NativePlayOn, PlayOnSnapshot} from '../platform/play-on/controller';

const none = () => () => {};

/** The top bar's Play on state: shown only where there is somewhere to play (an account device, Google Cast offered on
 * this server and network, or a choice or move already made), so it is never a dead control; and the device it plays
 * on, when one is chosen or playing. */
export function playOnButtonState(state: PlayOnSnapshot | undefined): Readonly<{visible: boolean; device?: string}> {
  if (!state) return {visible: false};
  const cast = state.cast?.cast, casting = cast?.phase === 'casting';
  const device = state.remote?.device ?? (casting ? cast?.deviceName ?? defaultI18n.t('playOn.googleCast') : undefined)
    ?? (state.chosen?.kind === 'device' ? state.chosen.device.displayName : state.chosen?.kind === 'cast' ? defaultI18n.t('playOn.googleCast') : undefined);
  return {visible: !!device || !!cast?.available || state.receivers.length > 0, ...(device ? {device} : {})};
}

/**
 * The one Play on button of the phone and tablet top bars (phone-app 1 to 3, phone-detail; `.ib` on Home, the round
 * glass button over a page's art). It opens the one Play on sheet; while a device is chosen or playing it turns the
 * accent colour and says where (phone-player 7, `.gr.on-tv`).
 */
export function PlayOnButton({playOn, variant, onPress, testID = 'play-on-top-bar'}: {
  playOn: NativePlayOn | undefined; variant: 'plain' | 'round'; onPress?: (anchor: React.RefObject<View | null>) => void; testID?: string;
}) {
  const theme = useTheme(), ref = useRef<View>(null);
  const state = useSyncExternalStore(playOn?.subscribe ?? none, () => playOn?.getSnapshot());
  useEffect(() => {playOn?.readDirectory();}, [playOn]);
  const {visible, device} = playOnButtonState(state);
  if (!playOn || !visible) return null;
  const playing = !!state?.remote?.device || state?.cast?.cast.phase === 'casting';
  const label = device ? playing ? defaultI18n.t('player.playingOn', {name: device})
    : state?.chosen?.kind === 'cast' ? defaultI18n.t('playOn.nextPlaysOnCast') : defaultI18n.t('playOn.nextPlaysOn', {device}) : defaultI18n.t('playOn.topBar');
  const open = () => {if (onPress) onPress(ref); else playOn.open(ref);};
  if (variant === 'round') return <RoundBarButton ref={ref} testID={testID} icon="i-cast" label={label} active={!!device} onPress={open}/>;
  return <Focusable ref={ref} testID={testID} accessibilityRole="button" accessibilityLabel={label} onPress={open} focusRadius={19} style={{width: 38, height: 38, borderRadius: 19}}>
    {({hovered}) => <View style={{width: 38, height: 38, borderRadius: 19, alignItems: 'center', justifyContent: 'center', backgroundColor: hovered ? `${theme.colors.text}1A` : 'transparent'}}>
      <Icon name="i-cast" size={22} color={device ? theme.colors.accentStrong : theme.colors.text}/>
    </View>}
  </Focusable>;
}
