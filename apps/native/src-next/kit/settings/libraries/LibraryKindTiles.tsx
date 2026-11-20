import React from 'react';
import {Text, View} from 'react-native';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Focusable} from '../../controls/Focusable';
import {Icon} from '../../Icon';
import {alpha} from '../../shell/visual';
import {useTheme} from '../../theme';
import {ownerLibraryKind, ownerLibraryKinds} from '../../../screens/settings-live/owner-libraries/presentation';

/** The seven library types share the Add library tile recipe. */
export function LibraryKindTiles({kind, onChange, disabled = false, firstRun = false, t = defaultI18n.t}: {
  kind: ManagedLibrary['kind']; onChange: (kind: ManagedLibrary['kind']) => void; disabled?: boolean; firstRun?: boolean; t?: typeof defaultI18n.t;
}) {
  const theme = useTheme();
  // At large text a kind's name ("Audiobooks") no longer fits half the width, so the tiles go one per row.
  const wide = theme.fontScale * theme.textScale >= 1.3;
  return <>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'space-between', gap: theme.space.sm, paddingTop: theme.space.sm}}>
      {ownerLibraryKinds.filter(option => !firstRun || option.kind !== 'comic').map(option => {
        const on = option.kind === kind;
        return <Focusable key={option.kind} accessibilityRole="radio" accessibilityLabel={t(option.label)} accessibilityState={{checked: on, disabled}} disabled={disabled} onPress={() => onChange(option.kind)} focusKind="card" focusRadius={14}
          style={{width: wide ? '100%' : '48%', flexGrow: firstRun ? 1 : 0, minHeight: firstRun ? 74 : 52, flexDirection: 'row', alignItems: 'center', gap: 10, paddingHorizontal: 14, borderRadius: 14, backgroundColor: on ? alpha(theme.colors.accent, .16) : firstRun ? theme.colors.raised : theme.colors.raisedHigh, borderWidth: 1.5, borderColor: on ? theme.colors.accentStrong : 'transparent'}}>
          <Icon name={option.icon} size={22} color={on ? theme.colors.accentStrong : theme.colors.textSecondary}/>
          <Text style={[theme.type.rowLabel, {color: theme.colors.text, flexShrink: 1}]}>{t(option.label)}</Text>
        </Focusable>;
      })}
    </View>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginVertical: theme.space.sm, paddingHorizontal: 4}]}>{t(ownerLibraryKind(kind).description)}</Text>
  </>;
}
