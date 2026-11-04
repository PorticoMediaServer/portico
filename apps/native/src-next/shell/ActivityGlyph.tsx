import React, {useEffect, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import {Icon} from '../kit/Icon';
import {shellType} from '../kit/shell/visual';
import {useTheme} from '../kit/theme';
import type {NativeOwnerActivity} from './owner-activity';

/** The busy pill's tint (owner-dashboard `.act.busy:has(.n)`). */
const busyTint = 'rgba(64,159,212,.14)';

/** The label the glyph's button carries: what it shows, or plain "Activity" while unknown. */
export function activityGlyphLabel(activity: NativeOwnerActivity | undefined): string {
  const state = activity?.getSnapshot();
  if (!state?.known) return defaultI18n.t('next.shell.activity');
  return defaultI18n.t(state.update ? 'next.shell.activityLabelUpdate' : 'next.shell.activityLabel', {count: state.count});
}

/**
 * owner-dashboard 1a and tablet-play 6 (`.act`): grey when idle, blue while anything runs, the stream
 * count beside it, the bell's dot for an update. Unknown draws the idle glyph with no count or dot. The
 * glyph holds the owner's summary live while it is drawn.
 */
export function ActivityGlyph({activity, size = 22, hovered = false}: {activity: NativeOwnerActivity; size?: number; hovered?: boolean}) {
  const theme = useTheme();
  const state = useSyncExternalStore(activity.subscribe, activity.getSnapshot);
  useEffect(() => activity.hold(), [activity]);
  const counted = state.busy && state.count > 0;
  return <View testID="activity-glyph" accessible={false} style={{height: 40, minWidth: 40, borderRadius: 20, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 5,
    paddingLeft: 9, paddingRight: counted ? 11 : 9, backgroundColor: counted ? busyTint : hovered ? `${theme.colors.text}1A` : 'transparent'}}>
    <Icon name="i-activity" size={size} color={state.busy ? theme.colors.accentStrong : hovered ? theme.colors.text : theme.colors.textSecondary}/>
    {counted && <Text testID="activity-glyph-count" style={[shellType(14, 800), {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{defaultI18n.number(state.count)}</Text>}
    {state.update && <View testID="activity-glyph-dot" pointerEvents="none" style={{position: 'absolute', top: 9, left: 23, width: 7, height: 7, borderRadius: 4, backgroundColor: theme.colors.accentStrong, borderWidth: 2, borderColor: theme.colors.ink}}/>}
  </View>;
}
