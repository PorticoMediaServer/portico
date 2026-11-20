import React from 'react';
import {hardwareFaultText, hardwareFormatName, hardwareMethodName, hardwareToneMapping, type OwnerStreamingHardware} from '../../../../../packages/client-core/src/server-admin/streaming-hardware';
import {settingsRowIndex} from '../../../../../packages/client-core/src/presentation/settings-spec';
import {serverNow} from '../../../../../packages/client-core/src/server-clock';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../controls';
import {SectionCard} from '../frames';
import {Banner} from '../overlays';
import {SettingsRow} from '../shell';
import {DetailsPanel} from '../states';

export function StreamingHardware({hardware, testing, tested, disabled, onTest, t}: {
  hardware: OwnerStreamingHardware; testing: boolean; tested?: 'ok' | 'problem'; disabled: boolean; onTest: () => void; t: typeof defaultI18n.t;
}) {
  const label = (id: string) => t(settingsRowIndex.get(id)!.row.label);
  const fault = hardwareFaultText(hardware, t), tone = hardwareToneMapping(hardware);
  const formats = settingsRowIndex.get('streaming.hwFormats')!.row.control, test = settingsRowIndex.get('streaming.hwTest')!.row;
  const names = (formats.columns ?? []).slice(1).map(column => t(column.label));
  const at = hardware.lastTest ? Date.parse(hardware.lastTest) : 0;
  const facts = hardware.formats.map(format => ({
    id: format.format,
    label: hardwareFormatName(format.format, t),
    value: (['decode', 'encode'] as const).map((job, index) =>
      [names[index], t(format[job] ? 'next.settings.hardware.job.hardware' : 'next.settings.hardware.job.processor')].filter(Boolean).join(' · ')).join('\n'),
  }));
  return <SectionCard label={t('next.settingsSpec.server.streaming.groups.3.title')}>
    {fault && <Banner placement="inline" variant="notice" tone="warning" icon="i-warn" message={fault}/>}
    {!!hardware.devices.length && <SectionCard label={label('streaming.hwDevices')}>
      {hardware.devices.map(device => <SettingsRow key={device.id} label={device.name}
        description={[hardwareMethodName(device.method, t), t(device.inUse ? 'next.settings.hardware.device.inUse' : 'next.settings.hardware.device.ready')].filter((fact): fact is string => !!fact)}/>)}
    </SectionCard>}
    {!!facts.length && <SectionCard label={label('streaming.hwFormats')}>
      <DetailsPanel compact audience="owner" testID="server-streaming-formats" facts={facts}/>
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
