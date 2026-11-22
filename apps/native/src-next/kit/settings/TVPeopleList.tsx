import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {Focusable} from '../controls';
import {shellType} from '../shell/visual';
import {StateView} from '../states';
import {useTheme} from '../theme';

type Row = Readonly<{id: string; title: string; meta: readonly string[]; status?: string}>;
type Group = Readonly<{id: string; title: MessageId; rows: readonly Row[]; empty?: MessageId; pending: boolean; error?: unknown;
  retry: () => void}>;

/** TV37's People page, read only: each group's heading, its rows (initial, name, what they are, how they stand), its empty
 * line, and its loading or failure line under them. */
export function TVPeopleList({groups, failure}: {groups: readonly Group[];
  failure: (error: unknown, retry: () => void) => React.ComponentProps<typeof StateView>}) {
  const theme = useTheme(), t = defaultI18n.t;
  return <View>{groups.map(group => <View key={group.id} style={{marginBottom: 30}}>
    <Text accessibilityRole="header" style={[shellType(24, 700), {color: theme.colors.textSecondary, marginBottom: 12}]}>{t(group.title)}</Text>
    {group.rows.map(row => <Focusable key={row.id} focusId={`tv-people:${row.id}`} focusKind="row" accessibilityRole="text"
      accessibilityLabel={[row.title, ...row.meta, row.status].filter(Boolean).join('. ')}
      style={{flexDirection: 'row', gap: 20, alignItems: 'center', padding: 22, marginBottom: 8, borderRadius: 16, backgroundColor: theme.colors.raised}}>
      <View style={{width: 44, height: 44, borderRadius: 22, alignItems: 'center', justifyContent: 'center', backgroundColor: `${theme.colors.accent}33`}}>
        <Text style={[shellType(22, 700), {color: theme.colors.accentStrong}]}>{row.title.slice(0, 1).toUpperCase()}</Text>
      </View>
      <View style={{flex: 1, gap: 5}}>
        <Text style={[shellType(26, 700), {color: theme.colors.text}]}>{row.title}</Text>
        <Text style={[shellType(20), {color: theme.colors.textSecondary}]}>{row.meta.filter(Boolean).join(' · ')}</Text>
      </View>
      {row.status && <Text style={[shellType(20, 600), {color: theme.colors.textSecondary, maxWidth: '38%'}]}>{row.status}</Text>}
    </Focusable>)}
    {!group.rows.length && group.empty && <StateView title={t(group.empty)}/>}
    {group.pending && <StateView title={t('status.loading')}/>}
    {group.error !== undefined && <StateView {...failure(group.error, group.retry)} testID={`tv-people-${group.id}-error`}/>}
  </View>)}</View>;
}
