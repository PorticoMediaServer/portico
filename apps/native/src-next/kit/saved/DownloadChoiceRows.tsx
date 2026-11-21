import React from 'react';
import {Text, View} from 'react-native';
import {Icon} from '../Icon';
import {MenuRow} from '../overlays';
import {useTheme} from '../theme';

/** One choice in the Download sheet's "Which episodes" or "Keep the next" card (collections 32), or a book's version
 * (tablet-read 12b): its label, ticked when chosen (the menu row draws the tick), and a version's size at its end. A
 * version this device can't take is dimmed in place with the reason under it. */
export function DownloadChoiceRow({label, description, detail, selected, disabled, onPress, testID}: {
  label: string; description?: string; detail?: string; selected: boolean; disabled?: boolean; onPress: () => void; testID?: string;
}) {
  const theme = useTheme();
  const trailing = detail ? <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{detail}</Text> : undefined;
  return <MenuRow testID={testID} variant="grouped" label={label} description={description} trailing={trailing} selected={selected}
    disabled={disabled} accessibilityState={{selected, disabled}} onPress={onPress}/>;
}

/** One quality in the Download sheet (collections 31a, 31c): its name and any note under it, then its size
 * ("about 5.4 GB", "for 9 episodes" or "the server prepares it first") and the download glyph. A quality the server has
 * no copy of is dimmed in place with the reason. */
export function DownloadQualityRow({label, description, size, note, available, disabled, onPress, testID}: {
  label: string; description?: string; size?: string; note?: string; available: boolean;
  disabled?: boolean; onPress: () => void; testID?: string;
}) {
  const theme = useTheme();
  const trailing = available ? <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
    {(size || note) && <View style={{alignItems: 'flex-end'}}>
      {size && <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{size}</Text>}
      {note && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{note}</Text>}
    </View>}
    <Icon name="i-dl" size={theme.scale.icon.tertiary.size} color={theme.colors.text}/>
  </View> : undefined;
  return <MenuRow testID={testID} variant="grouped" label={label} description={description} trailing={trailing}
    disabled={disabled || !available} onPress={onPress}/>;
}
