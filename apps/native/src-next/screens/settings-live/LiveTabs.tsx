import React from 'react';
import {Text, View} from 'react-native';
import {Segmented} from '../../kit/controls';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';

/** tablet-play6 and desktop-live: at expanded widths Live TV's title carries the Guide / Recordings pair,
 * on the guide and on the Recordings page alike. Touch only; TV keeps its own controls row. */
export function NativeLiveTabs({value, onGuide, onRecordings, t, children}: {
  value: 'guide' | 'recordings'; onGuide?: () => void; onRecordings?: () => void;
  t: (key: 'search.chip.liveTV' | 'channels.tabGuide' | 'recordings.title') => string; children?: React.ReactNode;
}) {
  const theme = useTheme();
  return <View testID="live-header" style={{flexDirection: 'row', alignItems: 'center', gap: 14, minHeight: 44, marginBottom: 14}}>
    <Text accessibilityRole="header" style={[shellType(34, 800, 'normal', -.04), {color: theme.colors.text}]}>{t('search.chip.liveTV')}</Text>
    {(onGuide || onRecordings) && <Segmented testID="live-tabs" variant="browse" accessibilityLabel={t('search.chip.liveTV')} value={value}
      onValueChange={next => {if (next === value) return; if (next === 'guide') onGuide?.(); else onRecordings?.();}}
      itemProps={item => ({testID: item.value === 'recordings' ? 'live-recordings' : 'live-tab-guide', accessibilityRole: 'tab'})}
      items={[{value: 'guide', label: t('channels.tabGuide')}, {value: 'recordings', label: t('recordings.title')}]}/>}
    <View style={{flex: 1}}/>
    {children}
  </View>;
}
