import React, {useEffect, useMemo, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {SecuritySummarySource, securityBlockLine, type SecuritySummaryApi} from '../../../../../../packages/client-core/src/server-admin/security-summary';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';

const K = 'next.settingsSpec.server.remote.groups.2.rows.1';
const words = {t: defaultI18n.t as never, time: defaultI18n.time, dateTime: defaultI18n.dateTime};

/** Blocked networks with Unblock; refused sign-in counters are not shown. */
export function NativeSecuritySummary({api, isCurrent, t}: Readonly<{api: SecuritySummaryApi; isCurrent: () => boolean; t: (id: string, values?: Record<string, unknown>) => string}>) {
  const theme = useTheme();
  const source = useMemo(() => new SecuritySummarySource(api, isCurrent), [api, isCurrent]);
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  useEffect(() => {void source.load();}, [source]);
  const blocks = state.summary?.blocks ?? [];
  return <View testID="remote-security-summary" style={{paddingHorizontal: 16, paddingVertical: 12, gap: 10, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft}}>
    <Text style={[shellType(14, 600), {color: theme.colors.text, marginTop: 4}]}>{t(`${K}.children.0.label`)}</Text>
    <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textSecondary}]}>{t(`${K}.children.0.help`)}</Text>
    {!blocks.length && <Text testID="remote-security-blocked-empty" style={[shellType(13), {color: theme.colors.textSecondary}]}>{state.unblocked ? t(`${K}.children.0.control.itemAction.result`) : t(`${K}.children.0.control.empty`)}</Text>}
    {blocks.map(block => <View key={block.id} testID={`remote-security-blocked-${block.network}`} style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <Icon name="i-shield" size={20} color={theme.colors.textSecondary}/>
      <View style={{flex: 1}}>
        <Text style={[shellType(14, 700), {color: theme.colors.text}]}>{block.network}</Text>
        <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textSecondary}]}>{securityBlockLine(block, words)}</Text>
      </View>
      <Button testID={`remote-security-unblock-${block.network}`} variant="glass" size="compact" disabled={!!state.unblocking} loading={state.unblocking === block.id} onPress={() => {void source.unblock(block.id);}}>{t(`${K}.children.0.control.itemAction.label`)}</Button>
    </View>)}
    {state.failed && state.summary && <Text accessibilityRole="alert" style={[shellType(13), {color: theme.colors.amber}]}>{t('next.remoteStatus.failed')}</Text>}
  </View>;
}
