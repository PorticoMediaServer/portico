import React from 'react';
import type {SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {Button, FormError} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {StreamingHardware} from '../../../kit/settings/StreamingHardware';
import {StateView} from '../../../kit/states';
import {NativeAdvancedSection} from '../../../screens/settings-live/server/AdvancedSection';
import {NativeSettingControl} from '../../../screens/settings-live/server/SettingControl';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useServerStreaming} from './useServerStreaming';

type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

export function ServerStreamingPage({ports, runtime, retry, ...frame}: Frame & {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; retry?: React.ReactNode;
}) {
  const streaming = useServerStreaming({ports, runtime}), {t, section, state, hardware, testing, tested} = streaming;
  const renderRow = (row: SpecRow): React.ReactNode => {
    const field = streaming.field(row), children = row.children?.map(renderRow);
    if (!field && !children?.length) return null;
    return <React.Fragment key={row.id}>
      {field && <NativeSettingControl key={field.key} {...field.control}/>}
      {children}
    </React.Fragment>;
  };
  const groups = (entries: NonNullable<typeof section>['groups']) => entries?.map((group, index) => {
    const rows = group.rows.map(renderRow).filter(Boolean);
    return rows.length ? <SectionCard key={index} label={group.title ? t(group.title) : undefined}>{rows}</SectionCard> : null;
  });
  const title = section ? t(section.title) : t('settings.server.playback');
  return <>
    <SettingsPageFrame {...frame} title={title} testID="server-streaming">
      {retry}
      {streaming.configurationError !== undefined && <StateView variant="inline" {...runtime.error(streaming.configurationError, () => streaming.loadConfiguration(), 'server-console')}/>}
      {streaming.formStates.map((form, index) => form.loadError !== undefined
        ? <StateView key={index} variant="inline" {...runtime.error(form.loadError, () => {void streaming.forms[index]!.load();}, 'server-console')}/>
        : form.loading ? <SectionCard key={index}><StateRow kind="loading"/></SectionCard> : null)}
      {state.saveError !== undefined && <StateView variant="inline" {...runtime.error(state.saveError, streaming.save, 'server-console')}/>}
      {state.problem && <PanelBlock><FormError placement="inline">{t(state.problem as Parameters<typeof t>[0])}</FormError></PanelBlock>}
      {groups(section?.groups)}
      {streaming.hardwareError !== undefined && <StateView variant="inline" {...runtime.error(streaming.hardwareError, streaming.loadHardware, 'server-console')}/>}
      {hardware && <StreamingHardware hardware={hardware} testing={testing} tested={tested} disabled={testing || state.saving} onTest={streaming.runHardwareTest} t={t}/>}
      <NativeAdvancedSection testID="server-streaming-advanced" open={streaming.advanced} onOpenChange={streaming.setAdvanced} summary={streaming.advancedSummary}>
        {groups(section?.advanced?.groups)}
      </NativeAdvancedSection>
    </SettingsPageFrame>
    <PanelBlock><OverlayFootRow layout="halves">
      <Button variant="glass" disabled={!streaming.canDiscard} onPress={streaming.discard}>{t('action.discard')}</Button>
      <Button disabled={!streaming.canSave} loading={state.saving} onPress={streaming.save}>{t('action.save')}</Button>
    </OverlayFootRow></PanelBlock>
  </>;
}
