import React from 'react';
import {Text, View} from 'react-native';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Focusable} from '../../../kit/controls/Focusable';
import {Icon} from '../../../kit/Icon';
import {alpha, shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import {ownerLibraryKind, ownerLibraryKinds} from './presentation';

/** The seven library types share the Add library tile recipe. */
export function LibraryKindTiles({kind, onChange, disabled = false, firstRun = false, t = defaultI18n.t}: {
  kind: ManagedLibrary['kind']; onChange: (kind: ManagedLibrary['kind']) => void; disabled?: boolean; firstRun?: boolean; t?: typeof defaultI18n.t;
}) {
  const theme = useTheme();
  return <>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 10}}>
      {ownerLibraryKinds.filter(option => !firstRun || option.kind !== 'comic').map(option => {
        const on = option.kind === kind;
        return <Focusable key={option.kind} accessibilityRole="radio" accessibilityLabel={t(option.label)} accessibilityState={{checked: on, disabled}} disabled={disabled} onPress={() => onChange(option.kind)} focusKind="card" focusRadius={14}
          style={{width: '47%', flexGrow: 1, minHeight: 74, flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 14, borderRadius: 14, backgroundColor: on ? alpha(theme.colors.accent, .16) : theme.colors.raised, borderWidth: 1.5, borderColor: on ? theme.colors.accentStrong : 'transparent'}}>
          <Icon name={option.icon} size={22} color={on ? theme.colors.accentStrong : theme.colors.textSecondary}/>
          <Text numberOfLines={1} style={[shellType(14.5, 700), {color: theme.colors.text, flexShrink: 1}]}>{t(option.label)}</Text>
        </Focusable>;
      })}
    </View>
    <Text style={[shellType(12.5, 500, 17.5), {color: theme.colors.textTertiary, marginTop: 8, paddingHorizontal: 4}]}>{t(ownerLibraryKind(kind).description)}</Text>
  </>;
}
