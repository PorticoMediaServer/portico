import React from 'react';
import {Text, View} from 'react-native';
import {hardwareFaultText, hardwareFormatName, hardwareMethodName, hardwareToneMapping, type OwnerStreamingHardware} from '../../../../../../packages/client-core/src/server-admin/streaming-hardware';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';

/** Settings › Server › Streaming › Hardware: the rebuilt server's measured report (`GET /v1/admin/hardware`), in the
 * words every app shares (client-core streaming-hardware.ts). A row with no measured fact is absent. */
export function NativeStreamingHardware({hardware, testing, tested, disabled, onTest, t}: {hardware: OwnerStreamingHardware; testing: boolean; tested?: 'ok' | 'problem'; disabled: boolean; onTest: () => void; t: typeof defaultI18n.t}) {
  const theme = useTheme(), label = (id: string) => t(settingsRowIndex.get(id)!.row.label);
  const fault = hardwareFaultText(hardware, t), tone = hardwareToneMapping(hardware);
  const grid = settingsRowIndex.get('streaming.hwFormats')!.row.control, test = settingsRowIndex.get('streaming.hwTest')!.row;
  const at = hardware.lastTest ? Date.parse(hardware.lastTest) : 0;
  return <View style={{gap: 12}} testID="server-streaming-hardware">
    <Text style={[theme.type.rowHeading, {color: theme.colors.textSecondary}]}>{t('next.settingsSpec.server.streaming.groups.3.title')}</Text>
    <View style={{backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
      {fault && <SettingsRow first label={label('streaming.hwError')} control={<Text style={[shellType(13), {color: theme.colors.amber, flexShrink: 1}]}>{fault}</Text>}/>}
      {hardware.devices.length > 0 && <View style={{padding: 14, gap: 10}}>
        <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{label('streaming.hwDevices')}</Text>
        {hardware.devices.map(device => <View key={device.id} style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
          <View style={{flex: 1}}><Text style={[shellType(15, 600), {color: theme.colors.text}]}>{device.name}</Text>
            {hardwareMethodName(device.method, t) && <Text style={[shellType(12.5), {color: theme.colors.textSecondary}]}>{hardwareMethodName(device.method, t)}</Text>}</View>
          <Text style={[shellType(12.5, 600), {color: device.inUse ? theme.colors.healthy : theme.colors.textTertiary}]}>{t(device.inUse ? 'next.settings.hardware.device.inUse' : 'next.settings.hardware.device.ready')}</Text>
        </View>)}
      </View>}
      {hardware.formats.length > 0 && <View testID="server-streaming-formats" style={{padding: 14, gap: 8}}>
        <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{label('streaming.hwFormats')}</Text>
        <View style={{flexDirection: 'row', gap: 8}}><View style={{flex: 1}}/>{(grid.columns ?? []).slice(1).map(column => <Text key={column.id} style={[shellType(12.5, 600), {color: theme.colors.textSecondary, flex: 1}]}>{t(column.label)}</Text>)}</View>
        {hardware.formats.map(format => <View key={format.format} style={{flexDirection: 'row', gap: 8, paddingVertical: 8, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft}}>
          <Text style={[shellType(13), {color: theme.colors.text, flex: 1}]}>{hardwareFormatName(format.format, t)}</Text>
          {(['decode', 'encode'] as const).map(job => <Text key={job} style={[shellType(13, 600), {color: format[job] ? theme.colors.healthy : theme.colors.textTertiary, flex: 1}]}>{t(format[job] ? 'next.settings.hardware.job.hardware' : 'next.settings.hardware.job.processor')}</Text>)}
        </View>)}
      </View>}
      {tone && <SettingsRow label={label('streaming.hwToneMapping')} control={<Text style={[shellType(13), {color: tone === 'hardware' ? theme.colors.healthy : theme.colors.textSecondary, flexShrink: 1}]}>{t(tone === 'hardware' ? 'next.settings.hardware.tone.hardware' : 'next.settings.hardware.tone.processor')}</Text>}/>}
      <SettingsRow first={!fault && !hardware.devices.length && !hardware.formats.length} label={t(test.label)}
        description={at > 0 ? t('next.settings.hardware.lastTest', {when: defaultI18n.relativeTime(at, serverNow(), {inSentence: true})}) : t('next.settings.hardware.neverTested')}/>
    </View>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, flexWrap: 'wrap'}}>
      <Button testID="server-streaming-test-hardware" variant="glass" size="compact" disabled={disabled} loading={testing} onPress={onTest}>{test.control.label ? t(test.control.label) : t(test.label)}</Button>
      {tested && <Text accessibilityRole="text" style={[shellType(13), {color: tested === 'ok' ? theme.colors.healthy : theme.colors.amber, flexShrink: 1}]}>{t(tested === 'ok' ? 'next.settings.hardware.testResult.ok' : 'next.settings.hardware.testResult.problem')}</Text>}
    </View>
  </View>;
}
