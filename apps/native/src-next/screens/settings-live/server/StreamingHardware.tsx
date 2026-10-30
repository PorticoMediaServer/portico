import React from 'react';
import {Text, View} from 'react-native';
import type {TranscodeCapacity} from '../../../../../../packages/client-core/src/telemetry';
import {when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';


/** These are measured facts, never the example devices or grid from the design spec. */
export function NativeStreamingHardware({capacity, testing, disabled, onTest, t}: {capacity: TranscodeCapacity; testing: boolean; disabled: boolean; onTest: () => void; t: typeof defaultI18n.t}) {
  const theme = useTheme(), hardware = capacity.hardware, label = (id: string) => t(settingsRowIndex.get(id)!.row.label);
  const formats: Readonly<Record<string, string>> = {h264: 'H.264', hevc8: 'HEVC 8-bit', hevc10: 'HEVC 10-bit', av1: t('next.kit.codec.av1'), vp9: t('next.kit.codec.vp9'), vc1: t('next.kit.codec.vc1'), mpeg2: t('next.kit.codec.mpeg2')};
  const grid = settingsRowIndex.get('streaming.hwFormats')!.row.control, test = settingsRowIndex.get('streaming.hwTest')!.row;
  return <View style={{gap: 12}} testID="server-streaming-hardware">
    <Text style={[theme.type.rowHeading, {color: theme.colors.textSecondary}]}>{t('next.settingsSpec.server.streaming.groups.3.title')}</Text>
    <View style={{backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
      <SettingsRow first label={t('settings.server.hardware')} value={hardware.effective}/>
      {hardware.lastError && <SettingsRow label={label('streaming.hwError')} control={<Text style={[shellType(13), {color: theme.colors.amber, flexShrink: 1}]}>{hardware.lastError.detail || t('error.unavailable')}</Text>}/>}
      {hardware.devices.length > 0 ? <View style={{padding: 14, gap: 10}}>
        <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{label('streaming.hwDevices')}</Text>
        {hardware.devices.map(device => <View key={device.id} style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
          <View style={{flex: 1}}><Text style={[shellType(15, 600), {color: theme.colors.text}]}>{device.name}</Text>
            {device.method && <Text style={[shellType(12.5), {color: theme.colors.textSecondary}]}>{device.method}</Text>}</View>
          {device.inUse && <Text style={[shellType(12.5, 600), {color: theme.colors.healthy}]}>{t('web.metadata.inUse')}</Text>}
        </View>)}
      </View> : !!hardware.device && <SettingsRow label={t('settings.server.hardwareDevice')} value={hardware.device}/>}
      {hardware.formats.length > 0 && <View testID="server-streaming-formats" style={{padding: 14, gap: 8}}>
        <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{label('streaming.hwFormats')}</Text>
        <View style={{flexDirection: 'row', gap: 8}}><View style={{flex: 1}}/>{(grid.columns ?? []).slice(1).map(column => <Text key={column.id} style={[shellType(12.5, 600), {color: theme.colors.textSecondary, flex: 1}]}>{t(column.label)}</Text>)}</View>
        {hardware.formats.map(format => <View key={format.format} style={{flexDirection: 'row', gap: 8, paddingVertical: 8, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft}}>
          <Text style={[shellType(13), {color: theme.colors.text, flex: 1}]}>{formats[format.format] ?? format.format}</Text>
          {(['decode', 'encode'] as const).map(operation => <Text key={operation} style={[shellType(13, 600), {color: format[operation] ? theme.colors.healthy : theme.colors.textTertiary, flex: 1}]}>{format[operation] ? t('settings.server.hardware') : '—'}</Text>)}
        </View>)}
      </View>}
      {!!capacity.hdrToneMapping.detail && <SettingsRow label={label('streaming.hwToneMapping')} control={<Text style={[shellType(13), {color: capacity.hdrToneMapping.status === 'available' ? theme.colors.healthy : capacity.hdrToneMapping.status === 'limited' ? theme.colors.amber : theme.colors.textSecondary, flexShrink: 1}]}>{capacity.hdrToneMapping.detail}</Text>}/>}
      <SettingsRow label={t(test.label)} value={hardware.lastTest ? when(hardware.lastTest * 1000) : '—'}/>
    </View>
    <Button testID="server-streaming-test-hardware" variant="glass" size="compact" disabled={disabled} loading={testing} onPress={onTest}>{test.control.label ? t(test.control.label) : t(test.label)}</Button>
  </View>;
}
