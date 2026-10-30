import React from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {useTheme} from '../../kit/theme';

/** The caller's real Record action for the programme it proved is on now. The label
 * and every state come from the caller; nothing here reads rights or receipt state. */
export type TVLiveRecordAction = Readonly<{
  label: string;
  onPress: () => void;
  busy?: boolean;
  disabled?: boolean;
  /** The caller's real accepted recording for this programme. Never inferred. */
  recording?: boolean;
}>;

/** The caller's More entry. Which source actions it offers is the caller's fact. */
export type TVLiveMoreAction = Readonly<{
  onPress: () => void;
  busy?: boolean;
  disabled?: boolean;
}>;

export type TVLiveActionsProps = Readonly<{
  /** True only while this live surface is the presented one. */
  enabled: boolean;
  record?: TVLiveRecordAction;
  more?: TVLiveMoreAction;
  /** The mobile bar's actual button size; TV retains its own 64 pt recipe. */
  size?: number;
  compact?: boolean;
}>;

/** tv-player 11 and 12: the live top row's Record glass pill and round More, where
 * the normal player keeps its clock. The caller positions the row; an action that is
 * not supplied is omitted rather than drawn as a disabled stand-in, and no focus is
 * taken automatically. */
export function TVLiveActions({enabled, record, more, size = 48, compact = true}: TVLiveActionsProps) {
  const theme = useTheme(), t = defaultI18n.t;
  if (!enabled || (!record && !more)) return null;
  if (!theme.isTV) return <View testID="handheld-live-actions" style={{flexDirection: 'row', alignItems: 'center', flexShrink: 0, gap: compact ? 0 : 10}}>
    {record && <Button variant={compact ? 'text' : 'glass'} size="compact" icon="i-rec" iconSize={22} iconColor={theme.colors.record} iconOnly={compact}
      focusId="watch-live-record" testID="watch-live-record" accessibilityLabel={record.label}
      style={{height: size, borderRadius: size / 2, ...(compact ? {width: size} : {paddingHorizontal: 16}), ...(record.recording ? {backgroundColor: 'rgba(241,121,121,.2)'} : {})}}
      loading={record.busy} disabled={record.disabled} onPress={record.onPress}>{record.label}</Button>}
    {more && <Button variant="text" icon="i-more" iconOnly iconSize={22} iconColor={theme.colors.text}
      focusId="watch-live-more" testID="watch-live-more" accessibilityLabel={t('next.controls.more')}
      style={{width: size, height: size, borderRadius: size / 2}} loading={more.busy} disabled={more.disabled} onPress={more.onPress}/>}
  </View>;
  // The frame's .ltools: pushed to the end of the caller's top row, 16 apart after a
  // 14 left inset; each control 64 high with 26 of horizontal padding and radius 14,
  // the round one exactly 64 square. Nothing here is positioned absolutely.
  return <View testID="tv-live-actions" style={{flexDirection: 'row', alignItems: 'center', flexShrink: 0, marginLeft: 'auto', gap: 16, paddingLeft: 14}}>
    {record && <Button variant="glass" size="compact" icon="i-rec" iconSize={28} focusId="watch-live-record" testID="watch-live-record"
      style={record.recording ? {backgroundColor: 'rgba(241,121,121,.2)'} : undefined}
      loading={record.busy} disabled={record.disabled} onPress={record.onPress}>{record.label}</Button>}
    {more && <Button variant="glass" size="compact" icon="i-more" iconSize={28} iconOnly focusId="watch-live-more" testID="watch-live-more"
      accessibilityLabel={t('next.controls.more')} loading={more.busy} disabled={more.disabled} onPress={more.onPress}/>}
  </View>;
}
