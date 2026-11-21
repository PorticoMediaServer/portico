import React from 'react';
import {Text, View} from 'react-native';
import {serverNow} from '../../../../../packages/client-core/src/server-clock';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {DirectoryRow} from '../../screens/accounts/directory-chooser';
import {Button} from '../controls';
import {Icon} from '../Icon';
import {CountdownRing} from '../states';
import {useTheme} from '../theme';

const t = defaultI18n.t;

function lastSeen(row: DirectoryRow, tv: boolean, now: number) {
  if (!row.subtitle) return undefined;
  const at = tv ? row.lastSeen?.at : undefined;
  if (at && Number.isFinite(Date.parse(at))) {
    const day = (value: number) => defaultI18n.formatDate(value, {year: 'numeric', month: '2-digit', day: '2-digit'});
    if (day(Date.parse(at)) === day(now)) return t('web.chooser.lastSeenToday', {time: defaultI18n.time(at)});
  }
  return t(row.subtitle.key, row.subtitle.values);
}

export function OfflineServerRows({rows, busy, activeId, retry, retryIn, retryProgress}: {
  rows: readonly DirectoryRow[]; busy: boolean; activeId?: string; retry: (id: string) => void;
  retryIn?: number; retryProgress?: number;
}) {
  const theme = useTheme(), tv = theme.isTV, now = serverNow();
  return <>
    {rows.map((row, index) => {
      const seen = lastSeen(row, tv, now);
      return <View key={row.id} style={{flexDirection: tv ? 'row' : 'column', alignItems: tv ? 'center' : undefined,
        gap: tv ? theme.space.md : theme.space.sm, padding: theme.space.sm, borderRadius: tv ? 22 : 14,
        borderWidth: tv ? 1 : 0, borderColor: theme.colors.line, backgroundColor: theme.colors.raised, minHeight: tv ? 112 : undefined}}>
        {tv && <View style={{width: 68, height: 68, borderRadius: 18, alignItems: 'center', justifyContent: 'center',
          flexShrink: 0, backgroundColor: theme.colors.raisedBright}}>
          <Icon name="i-server" size={34} color={theme.colors.textTertiary}/>
        </View>}
        <View style={{flex: tv ? 1 : undefined, minWidth: 0, gap: theme.space.xs}}>
          <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{row.name}</Text>
          {seen && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{seen}</Text>}
        </View>
        {tv && row.owner && <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary, backgroundColor: theme.colors.ink,
          paddingHorizontal: theme.space.sm, paddingVertical: theme.space.xs, borderRadius: 8}]}>{t('accounts.settings.owner')}</Text>}
        <Button focusId={`retry:${row.id}`} variant="glass" size={tv ? 'compact' : undefined} icon={tv ? 'i-retry' : undefined}
          preferredFocus={tv && index === 0} disabled={busy} loading={busy && activeId === row.id || undefined}
          onPress={() => retry(row.id)}>{t('web.chooser.tryAgain')}</Button>
      </View>;
    })}
    {retryIn !== undefined && !busy && <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: theme.space.sm}}>
      {tv && <CountdownRing progress={retryProgress}/>}
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t('web.chooser.checkingAgain', {seconds: retryIn})}</Text>
    </View>}
  </>;
}
