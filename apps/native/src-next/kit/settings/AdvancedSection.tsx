import React, {useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

/** The same collapsed bar for the owner pages; children retain their drafts on collapse. */
export function NativeAdvancedSection({children, summary = defaultI18n.t('next.settings.allDefaults'), open, onOpenChange, testID}: {
  children: React.ReactNode;
  summary?: string;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  testID?: string;
}) {
  const theme = useTheme(), frame = useFrameMetrics(), [expanded, setExpanded] = useState(false), visible = open ?? expanded;
  return <View style={{gap: frame.sectionGap}}>
    <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={defaultI18n.t('settings.section.advanced')}
      accessibilityState={{expanded: visible}} accessibilityHint={!visible ? summary : undefined}
      onPress={() => {setExpanded(!visible); onOpenChange?.(!visible);}} focusKind="row" focusRadius={frame.cardRadius}
      style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap, minHeight: frame.rowTwoLine, paddingVertical: theme.space.tight, paddingHorizontal: frame.cardPadding, marginHorizontal: frame.cardInset, borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{defaultI18n.t('settings.section.advanced')}</Text>
      {!visible && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, flex: 1}]}>{summary}</Text>}
      <Icon name={visible ? 'i-up' : 'i-down'} size={theme.type.rowLabel.fontSize} color={theme.colors.textTertiary} style={{marginLeft: 'auto'}}/>
    </Focusable>
    <View style={{display: visible ? 'flex' : 'none', gap: frame.sectionGap}}>{children}</View>
  </View>;
}
