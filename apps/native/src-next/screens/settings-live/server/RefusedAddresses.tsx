import React, {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {RefusedAddressesSource, type RefusedAddressesApi} from '../../../../../../packages/client-core/src/server-admin/refused-addresses';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';

const K = 'next.settingsSpec.server.remote.groups.2.rows.0.children.0.children.0';

/** Refused addresses (O-169) on the phone: each name, why, how often, and its
 * one fix. "Use Standard" changes the whole server, so it asks first. */
export function NativeRefusedAddresses({api, isCurrent, t, onAllowed, onStandard, onCertificate}: Readonly<{
  api: RefusedAddressesApi; isCurrent: () => boolean; t: (id: string, values?: Record<string, unknown>) => string;
  onAllowed: () => void; onStandard: () => void; onCertificate: () => void;
}>) {
  const theme = useTheme();
  const source = useMemo(() => new RefusedAddressesSource(api, isCurrent, onAllowed), [api, isCurrent, onAllowed]);
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [confirming, setConfirming] = useState(false);
  useEffect(() => {void source.load();}, [source]);
  return <View testID="remote-refused" style={{paddingHorizontal: 16, paddingVertical: 12, gap: 10}}>
    <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{t(`${K}.label`)}</Text>
    <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textSecondary}]}>{t(`${K}.help`)}</Text>
    {!state.names.length && <Text testID="remote-refused-empty" style={[shellType(13), {color: theme.colors.textSecondary}]}>{state.failed ? t('next.remoteStatus.failed') : t(`${K}.control.empty`)}</Text>}
    {state.names.map(item => <View key={item.name} testID={`remote-refused-${item.name}`} style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <Icon name={item.reason === 'proxy' ? 'i-server' : 'i-globe'} size={20} color={theme.colors.textSecondary}/>
      <View style={{flex: 1}}>
        <Text style={[shellType(14, 700), {color: theme.colors.text}]}>{item.name}</Text>
        <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textSecondary}]}>{t(`${K}.reason.${item.reason}`)} {t(`${K}.asked`, {count: item.count})}</Text>
      </View>
      {item.reason === 'name'
        ? <Button testID={`remote-refused-allow-${item.name}`} variant="glass" size="compact" disabled={!!state.allowing} loading={state.allowing === item.name} onPress={() => {void source.allow(item.name);}}>{t(`${K}.control.itemAction.label`)}</Button>
        : item.reason === 'certificate'
          ? <Button testID={`remote-refused-certificate-${item.name}`} variant="glass" size="compact" onPress={onCertificate}>{t(`${K}.control.certificateAction.label`)}</Button>
          : <Button testID={`remote-refused-standard-${item.name}`} variant="glass" size="compact" onPress={() => setConfirming(true)}>{t(`${K}.control.standardAction.label`)}</Button>}
    </View>)}
    <BottomSheet open={confirming} onOpenChange={setConfirming} title={t(`${K}.control.standardAction.title`)} description={t(`${K}.control.standardAction.confirm`)}
      footer={<View style={{flexDirection: 'row', gap: 12}}>
        <Button testID="remote-refused-standard-cancel" variant="glass" style={{flex: 1}} onPress={() => setConfirming(false)}>{t('action.cancel')}</Button>
        <Button testID="remote-refused-standard-confirm" variant="primary" style={{flex: 1}} onPress={() => {setConfirming(false);onStandard();}}>{t(`${K}.control.standardAction.label`)}</Button>
      </View>}>{null}</BottomSheet>
  </View>;
}
