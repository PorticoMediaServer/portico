import React from 'react';
import {Text, View} from 'react-native';
import {Segmented} from '../../kit/controls';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';

/** tablet-play6 and desktop-live: at expanded widths Live TV's title carries the Guide / Recordings pair,
 * on the guide and on the Recordings page alike. Touch only; TV keeps its own controls row. */
export function NativeLiveTabs({value, onGuide, onRecordings, conflicts = 0, t, children}: {
  value: 'guide' | 'recordings'; onGuide?: () => void; onRecordings?: () => void;
  /** tablet-play6: at this width the Recordings tab's conflict note is a red dot; the words are on the Recordings page. */
  conflicts?: number;
  t: (key: 'search.chip.liveTV' | 'channels.tabGuide' | 'recordings.title' | 'next.live.recordingsConflicts', values?: Readonly<{count: number}>) => string; children?: React.ReactNode;
}) {
  const theme = useTheme();
  return <View testID="live-header" style={{flexDirection: 'row', alignItems: 'center', gap: 14, minHeight: 44, marginBottom: 14}}>
    <Text accessibilityRole="header" style={[shellType(34, 800, 'normal', -.04), {color: theme.colors.text}]}>{t('search.chip.liveTV')}</Text>
    {(onGuide || onRecordings) && <Segmented testID="live-tabs" variant="browse" accessibilityLabel={t('search.chip.liveTV')} value={value}
      onValueChange={next => {if (next === value) return; if (next === 'guide') onGuide?.(); else onRecordings?.();}}
      itemProps={item => ({testID: item.value === 'recordings' ? 'live-recordings' : 'live-tab-guide', accessibilityRole: 'tab',
        ...(item.value === 'recordings' && conflicts > 0 ? {accessibilityLabel: t('next.live.recordingsConflicts', {count: conflicts})} : {})})}
      items={[{value: 'guide', label: t('channels.tabGuide')}, {value: 'recordings', label: conflicts > 0
        ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 6}}><Text style={[theme.type.button, {color: value === 'recordings' ? theme.colors.text : theme.colors.textTertiary, fontSize: 14 * theme.textScale}]}>{t('recordings.title')}</Text><LiveConflictDot/></View>
        : t('recordings.title')}]}/>}
    <View style={{flex: 1}}/>
    {children}
  </View>;
}

/** The conflict mark the tablet frames draw on Recordings: an 8 pt dot in the record colour, never focusable or spoken (its
 * control's label says it). */
export function LiveConflictDot({style}: {style?: React.ComponentProps<typeof View>['style']}) {
  const theme = useTheme();
  return <View testID="live-recordings-conflict" pointerEvents="none" accessible={false} style={[{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.record}, style]}/>;
}

