import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Switch} from '../../kit/controls';
import {useTheme} from '../../kit/theme';

/**
 * The episodes bar under a show's tabs (phone-detail `ep-bar`): the season chip, the season's
 * count, Hide watched and, for the owner, Show settings. W-10: the switch and ⋯ never leave the
 * screen; a long season name ("Unassigned absolute episodes") truncates in its chip, and the
 * count gives way first.
 */
export function TitleSeasonBar({label, count, onSeasons, hideWatched, onHideWatched, settings}: {
  label: string; count?: string; onSeasons: () => void;
  hideWatched: boolean; onHideWatched: (value: boolean) => void;
  settings?: Readonly<{label: string; onPress: () => void}>;
}) {
  const theme = useTheme(), t = defaultI18n.t, phone = theme.platform === 'phone';
  return <View testID="watch-season-bar" style={{flexDirection: 'row', alignItems: 'center', gap: phone ? 8 : 10}}>
    <Button testID="watch-seasons-open" variant="glass" size="small" trailingIcon="i-chdown" style={{flexShrink: 1, minWidth: 0}} onPress={onSeasons}>{label}</Button>
    {count ? <Text testID="watch-season-count" numberOfLines={1} style={[theme.type.caption, {color: theme.colors.textTertiary, flexShrink: 4, minWidth: 0}]}>{count}</Text> : null}
    <View style={{flex: 1}}/>
    {!theme.isTV && <View testID="watch-season-controls" style={{flexDirection: 'row', alignItems: 'center', gap: 8, flexShrink: 0}}>
      <Text style={[theme.type.bodyStrong, {fontSize: 12.5, color: theme.colors.textSecondary}]}>{t('next.controls.hideWatched')}</Text>
      <Switch testID="watch-hide-watched" variant={phone ? 'episode' : 'selection'} checked={hideWatched} accessibilityLabel={t('next.controls.hideWatched')} onCheckedChange={onHideWatched}/>
    </View>}
    {settings && <Button testID="watch-show-settings-open" variant="icon" icon="i-more" iconOnly style={{flexShrink: 0}} accessibilityLabel={settings.label} onPress={settings.onPress}/>}
  </View>;
}
