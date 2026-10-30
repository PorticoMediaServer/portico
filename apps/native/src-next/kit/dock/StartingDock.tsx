import React from 'react';
import {ActivityIndicator, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Artwork, type ArtworkSource} from '../artwork';
import {Focusable} from '../controls';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';
import {BlurView} from '../../platform/native-blur';
import {alpha, dockStyles as s, dockType} from './visual';
import type {DockBarFrame} from './model';

/** O-159's starting dock: an accepted Play drawn at once from the pressed card, in the dock's own
 * connecting look (art under a pending ring, the title, Stop only). It never shows Pause or
 * progress, and the real dock replaces it in place. On a television Stop takes the focus.
 * Untitled (a catalogue or group start before its first entry is known), it reads "Starting…"
 * with its kind's icon when known, and is named in place once the runtime learns it. A Live
 * start the server reports recoverable says why in its second line while it keeps trying. */
export function StartingDock({title, icon, artwork, reason, onStop, frame, testID = 'playback-starting-dock'}: {
  title?: string; icon?: IconName; artwork?: ArtworkSource; reason?: string; onStop: () => void; frame: DockBarFrame; testID?: string;
}) {
  const theme = useTheme(), t = defaultI18n.t, tv = theme.isTV;
  const artSize = tv ? 64 : 44;
  const stop = <Focusable testID={`${testID}.stop`} accessibilityRole="button" accessibilityLabel={t('next.dock.native.stopConnecting')} focusRadius={tv ? 36 : 20}
    hasTVPreferredFocus={tv} onPress={onStop} style={tv ? s.tvButton : s.phoneButton}>
    <Icon name="i-stop" size={tv ? 30 : 20}/>
  </Focusable>;
  const second = reason ?? (title ? t('next.dock.starting') : undefined);
  return <View testID={testID} accessibilityLabel={[title ?? t('next.dock.starting'), second].filter(Boolean).join(', ')} accessibilityState={{busy: true}}
    style={tv ? {position: 'absolute', top: 44, right: theme.layout.gutter.tvRight} : {position: 'absolute', left: frame.left, right: frame.right, bottom: frame.bottom, ...(frame.maxWidth ? {maxWidth: frame.maxWidth} : {})}}>
    <View style={[tv ? s.tvQuiet : s.phone, {backgroundColor: alpha(theme.colors.raisedBright, .88), borderWidth: 1, borderColor: alpha(theme.colors.text, .08)}]}>
      <BlurView pointerEvents="none" style={s.fill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.raisedBright}/>
      <View style={[s.art, {width: artSize, height: artSize, backgroundColor: theme.colors.shadow}]}>
        {artwork ? <Artwork source={artwork} width={artSize} height={artSize} priority imageStyle={{opacity: .55}}/>
          : icon ? <View style={[s.fill, s.center]}><Icon name={icon} size={tv ? 30 : 20} color={theme.colors.textSecondary}/></View> : null}
        <View style={[s.fill, s.center, {backgroundColor: alpha(theme.colors.dim, .55)}]}><ActivityIndicator size={tv ? 'large' : 20} color={theme.colors.text}/></View>
      </View>
      <View style={s.text}>
        <Text testID={`${testID}.title`} numberOfLines={1} ellipsizeMode="tail" style={[dockType(theme, tv ? 24 : 14, 700), {color: theme.colors.text}]}>{title ?? t('next.dock.starting')}</Text>
        {second && <Text testID={`${testID}.status`} numberOfLines={reason ? 2 : 1} style={[dockType(theme, tv ? 20 : 12, 400), {color: theme.colors.textSecondary}]}>{second}</Text>}
      </View>
      {stop}
    </View>
  </View>;
}
