import React, {useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Focusable} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';

/** The same collapsed bar for the owner pages; children retain their drafts on collapse. */
export function NativeAdvancedSection({children, summary = defaultI18n.t('next.settings.allDefaults'), open, onOpenChange, testID}: {
  children: React.ReactNode;
  summary?: string;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  testID?: string;
}) {
  const theme = useTheme(), [expanded, setExpanded] = useState(false), visible = open ?? expanded;
  return <View style={{marginTop: 24}}>
    <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={defaultI18n.t('settings.section.advanced')}
      accessibilityState={{expanded: visible}} accessibilityHint={!visible ? summary : undefined}
      onPress={() => {setExpanded(!visible); onOpenChange?.(!visible);}} focusKind="row" focusRadius={14}
      style={{flexDirection: 'row', alignItems: 'center', gap: 16, paddingVertical: 18, paddingHorizontal: 22, borderRadius: 14, backgroundColor: theme.colors.raised}}>
      <Text style={[shellType(16, 600), {color: theme.colors.text}]}>{defaultI18n.t('settings.section.advanced')}</Text>
      {!visible && <Text numberOfLines={1} style={[shellType(14), {color: theme.colors.textTertiary, flex: 1}]}>{summary}</Text>}
      <Icon name={visible ? 'i-up' : 'i-down'} size={22} color={theme.colors.textTertiary} style={{marginLeft: 'auto'}}/>
    </Focusable>
    <View style={{display: visible ? 'flex' : 'none', marginTop: 26, gap: 18}}>{children}</View>
  </View>;
}
