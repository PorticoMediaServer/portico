import React, {useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Switch} from '../../kit/controls';
import {Menu} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';

/**
 * The episodes bar under a show's tabs, drawn by role as the frames draw it: a viewer gets the season chip, the
 * season's count and Hide watched (phone-app `ep-bar`); whoever has the ⋯ actions gets the chip, the count and ⋯,
 * with Hide watched as the first item inside it (phone-detail `ep-bar`, lead's ruling 13 Nov). The count never
 * gives way; a long season name ("Unassigned absolute episodes") truncates in its chip.
 */
export function TitleSeasonBar({label, count, onSeasons, hideWatched, onHideWatched, settings}: {
  label: string; count?: string; onSeasons: () => void;
  hideWatched: boolean; onHideWatched: (value: boolean) => void;
  settings?: Readonly<{label: string; onPress: () => void}>;
}) {
  const theme = useTheme(), t = defaultI18n.t, phone = theme.platform === 'phone';
  const more = useRef<View>(null), [open, setOpen] = useState(false);
  return <View testID="watch-season-bar" style={{flexDirection: 'row', alignItems: 'center', gap: phone ? 8 : 10}}>
    <Button testID="watch-seasons-open" variant="glass" size="small" trailingIcon="i-chdown" style={{flexShrink: 1, minWidth: 0}} onPress={onSeasons}>{label}</Button>
    {count ? <Text testID="watch-season-count" numberOfLines={1} style={[theme.type.caption, {color: theme.colors.textTertiary, flexShrink: 0}]}>{count}</Text> : null}
    <View style={{flex: 1}}/>
    {!theme.isTV && !settings && <View testID="watch-season-controls" style={{flexDirection: 'row', alignItems: 'center', gap: 8, flexShrink: 0}}>
      <Text style={[theme.type.bodyStrong, {fontSize: 12.5, color: theme.colors.textSecondary}]}>{t('next.controls.hideWatched')}</Text>
      <Switch testID="watch-hide-watched" variant={phone ? 'episode' : 'selection'} checked={hideWatched} accessibilityLabel={t('next.controls.hideWatched')} onCheckedChange={onHideWatched}/>
    </View>}
    {settings && <>
      <Button ref={more} testID="watch-show-settings-open" variant="icon" icon="i-more" iconOnly style={{flexShrink: 0}} accessibilityLabel={settings.label} accessibilityState={{expanded: open}} onPress={() => setOpen(true)}/>
      <Menu testID="watch-season-menu" title={settings.label} open={open} onOpenChange={setOpen} anchorRef={more} returnFocusRef={more} items={[
        {id: 'hide-watched', icon: 'i-eyeoff', label: t('next.controls.hideWatched'), selected: hideWatched, onSelect: () => onHideWatched(!hideWatched)},
        {id: 'show-settings', icon: 'i-filter', label: t('title.showSettings'), onSelect: settings.onPress},
      ]}/>
    </>}
  </View>;
}
