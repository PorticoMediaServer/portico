import React from 'react';
import {Text, View} from 'react-native';
import {hardwareFaultText, hardwareFormatName, hardwareMethodName, hardwareToneMapping, type OwnerStreamingHardware} from '../../../../../packages/client-core/src/server-admin/streaming-hardware';
import {settingsRowIndex} from '../../../../../packages/client-core/src/presentation/settings-spec';
import {serverNow} from '../../../../../packages/client-core/src/server-clock';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../controls';
import {SectionCard} from '../frames';
import {Banner} from '../overlays';
import {SettingsRow} from '../shell';
import {useTheme} from '../theme';
import {alpha} from '../shell/visual';

export function StreamingHardware({hardware, testing, tested, disabled, onTest, t}: {
  hardware: OwnerStreamingHardware; testing: boolean; tested?: 'ok' | 'problem'; disabled: boolean; onTest: () => void; t: typeof defaultI18n.t;
}) {
  const theme = useTheme();
  const label = (id: string) => t(settingsRowIndex.get(id)!.row.label);
  const fault = hardwareFaultText(hardware, t), tone = hardwareToneMapping(hardware);
  const formats = settingsRowIndex.get('streaming.hwFormats')!.row.control, test = settingsRowIndex.get('streaming.hwTest')!.row;
  const columns = formats.columns ?? [];
  const at = hardware.lastTest ? Date.parse(hardware.lastTest) : 0;
  const row = {flexDirection: 'row', alignItems: 'flex-start', gap: theme.space.xs} as const;
  return <SectionCard label={t('next.settingsSpec.server.streaming.groups.3.title')}>
    {fault && <Banner placement="inline" variant="notice" tone="warning" icon="i-warn" message={fault}/>}
    {!!hardware.devices.length && <SectionCard label={label('streaming.hwDevices')}>
      {hardware.devices.map(device => <SettingsRow key={device.id} label={device.name}
        description={[hardwareMethodName(device.method, t), t(device.inUse ? 'next.settings.hardware.device.inUse' : 'next.settings.hardware.device.ready')].filter((fact): fact is string => !!fact)}/>)}
    </SectionCard>}
    {!!hardware.formats.length && <SectionCard label={label('streaming.hwFormats')}>
      <View testID="server-streaming-formats" style={{paddingHorizontal: theme.space.sm, paddingVertical: theme.space.xs}}>
        <View style={[row, {paddingBottom: theme.space.xs}]}>
          {columns.map(column => <Text key={column.id} style={[theme.type.rowHeading, {color: theme.colors.textSecondary, flex: 1}]}>{t(column.label)}</Text>)}
        </View>
        {hardware.formats.map(format => <View key={format.format} style={[row, {paddingVertical: theme.space.xs, borderTopWidth: theme.space.hairline, borderColor: theme.colors.lineSoft}]}>
          <Text style={[theme.type.rowLabel, {color: theme.colors.text, flex: 1}]}>{hardwareFormatName(format.format, t)}</Text>
          {(['decode', 'encode'] as const).map(job => <View key={job} style={{flex: 1, alignItems: 'flex-start'}}>
            <Text style={[theme.type.rowHelp, {color: format[job] ? theme.colors.healthy : theme.colors.textSecondary,
              backgroundColor: format[job] ? alpha(theme.colors.healthy, .16) : theme.colors.raisedHigh,
              paddingHorizontal: theme.space.xs, paddingVertical: theme.space.hairline * 2, borderRadius: theme.baseRadii.control}]}>
              {t(format[job] ? 'next.settings.hardware.job.hardware' : 'next.settings.hardware.job.processor')}
            </Text>
          </View>)}
        </View>)}
      </View>
    </SectionCard>}
    {tone && <SettingsRow label={label('streaming.hwToneMapping')} value={t(tone === 'hardware' ? 'next.settings.hardware.tone.hardware' : 'next.settings.hardware.tone.processor')}/>}
    <SettingsRow label={t(test.label)}
      description={at > 0 ? t('next.settings.hardware.lastTest', {when: defaultI18n.relativeTime(at, serverNow(), {inSentence: true})}) : t('next.settings.hardware.neverTested')}/>
    <Button testID="server-streaming-test-hardware" variant="glass" size="compact" align="stretch"
      disabled={disabled} loading={testing} onPress={onTest}>{test.control.label ? t(test.control.label) : t(test.label)}</Button>
    {tested && <Banner placement="inline" variant="notice" tone={tested === 'ok' ? 'success' : 'warning'}
      message={t(tested === 'ok' ? 'next.settings.hardware.testResult.ok' : 'next.settings.hardware.testResult.problem')}/>}
  </SectionCard>;
}
