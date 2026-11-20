import React from 'react';
import {Text, View} from 'react-native';
import {Focusable} from '../../controls/Focusable';
import {useTheme} from '../../theme';

export function MetadataAgentChoice({option, checked, first, onPress}: {option: Readonly<{id: string; name: string; description: string}>; checked: boolean; first: boolean; onPress: () => void}) {
  const theme = useTheme();
  return <Focusable accessibilityRole="radio" accessibilityLabel={option.name} accessibilityHint={option.description} accessibilityState={{checked}} onPress={onPress} focusKind="row" focusRadius={10}
    style={{flexDirection: 'row', alignItems: 'flex-start', gap: 12, paddingHorizontal: 14, paddingVertical: 12, minHeight: 52, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft}}>
    <View style={{width: 21, height: 21, marginTop: 1, borderRadius: 11, borderWidth: 2, borderColor: checked ? theme.colors.accentStrong : theme.colors.lineStrong, alignItems: 'center', justifyContent: 'center'}}>{checked && <View style={{width: 9, height: 9, borderRadius: 5, backgroundColor: theme.colors.text}}/>}</View>
    <View style={{flex: 1}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{option.name}</Text>
      {option.description ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 2}]}>{option.description}</Text> : null}
    </View>
  </Focusable>;
}

