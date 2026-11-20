import React from 'react';
import type {SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {Button, FormError} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import {DetailsPanel, StateView} from '../../../kit/states';
import {NativeAdvancedSection} from '../../../screens/settings-live/server/AdvancedSection';
import {NativeBroadcastSheet} from '../../../screens/settings-live/server/BroadcastSheet';
import {NativeSettingControl} from '../../../screens/settings-live/server/SettingControl';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useServerGeneral} from './useServerGeneral';

type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;
type Group = ReturnType<typeof useServerGeneral>['main'][number];

export function ServerGeneralPage({ports, runtime, retry, ...frame}: Frame & {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; retry?: React.ReactNode;
}) {
  const general = useServerGeneral({ports, runtime}), {t} = general;

  const renderRow = (row: SpecRow, first: boolean): React.ReactNode => {
    switch (row.id) {
      case 'general.version': return general.updates
        ? <SettingsRow key={row.id} first={first} label={t(row.label)} value={general.updates.version} description={general.versionFacts}/>
        : general.updateError !== undefined
          ? <StateView key={row.id} variant="inline" {...runtime.error(general.updateError, general.loadUpdates, 'server-console')}/> : null;
      case 'general.checkNow': return <Button key={row.id} testID="server-general-check" size="compact" align="stretch"
        variant="glass" icon="i-refresh" loading={general.checking} disabled={!general.enabled(row)}
        onPress={() => general.loadUpdates(true)}>{t('server.updates.check')}</Button>;
      case 'general.pauseBackground': return <Button key={row.id} testID="server-general-pause" size="compact" align="stretch"
        variant="glass" loading={general.busy} onPress={general.pause}>
        {t(general.paused ? 'settings.server.resumeBackground' : 'settings.server.pauseBackground')}</Button>;
      case 'general.message': return <SettingsRow key={row.id} first={first} testID="server-general-message" label={t('server.messageEveryone')}
        icon="i-mail" navigates onPress={() => general.setMessageOpen(true)}/>;
      case 'general.restart': return <React.Fragment key={row.id}>
        <SettingsHelp>{t('server.restart.body')}</SettingsHelp>
        <Button testID="server-general-restart-open" size="compact" align="stretch" variant="danger" disabled={general.busy}
          onPress={() => general.setRestarting(true)}>{t('web.maintenance.serverRestart')}</Button>
      </React.Fragment>;
      case 'general.configFile': return general.configuration?.path
        ? <DetailsPanel key={row.id} compact audience="owner" facts={[{id: row.id, label: t(row.label), value: general.configuration.path}]}/> : null;
      case 'general.serverId': return <DetailsPanel key={row.id} compact audience="owner" facts={[{id: row.id, label: t(row.label), value: general.serverId}]}/>;
    }
    if (!general.hasSetting(row.id) || row.enableWhen?.hide && !general.enabled(row)) return null;
    return <NativeSettingControl key={row.id} row={row} value={general.value(row.id)} t={t} insetField
      disabled={general.saving || !general.enabled(row)} onChange={next => general.change(row, next)}
      onCustom={row.control.options?.some(option => option.value === 'custom') ? () => general.openCustom(row) : undefined}/>;
  };

  const renderGroup = (group: Group, index: number) => {
    const rows = group.rows.map((row, rowIndex) => renderRow(row, rowIndex === 0)).filter(Boolean);
    return rows.length ? <SectionCard key={index} label={group.title ? t(group.title) : undefined}>{rows}</SectionCard> : null;
  };
  const failure = !general.configuration && general.loadError !== undefined
    ? runtime.error(general.loadError, general.load, 'server-console') : undefined;
  const state = failure && {icon: failure.icon ?? 'i-alert' as const, tone: failure.tone,
    title: typeof failure.title === 'string' ? failure.title : t('action.tryAgain'),
    body: typeof failure.body === 'string' ? failure.body : undefined, primaryAction: failure.primaryAction,
    secondaryAction: failure.secondaryAction, help: failure.help, testID: failure.testID};

  return <>
    <SettingsPageFrame {...frame} title={t('settings.server.general')} testID="server-general" state={state}>
      {retry}
      {general.configuration ? general.main.map(renderGroup) : <SectionCard><StateRow kind="loading"/></SectionCard>}
      {general.configuration && general.extra.length > 0 && <NativeAdvancedSection testID="server-general-advanced" open={general.advanced}
        onOpenChange={general.setAdvanced} summary={general.advancedSummary}>{general.extra.map(renderGroup)}</NativeAdvancedSection>}
      {general.problem && <PanelBlock><FormError placement="inline">{general.problem}</FormError></PanelBlock>}
      {general.hasDrafts && <SectionCard><OverlayFootRow layout="halves">
        <Button testID="server-general-discard" variant="glass" align="stretch" disabled={general.saving} onPress={general.discard}>{t('action.discard')}</Button>
        <Button testID="server-general-save" align="stretch" loading={general.saving} onPress={general.save}>{t('action.save')}</Button>
      </OverlayFootRow></SectionCard>}
      {general.saveError !== undefined && <StateView variant="inline" {...runtime.error(general.saveError, general.save, 'server-console')}/>}
    </SettingsPageFrame>
    <NativeBroadcastSheet ports={ports} runtime={runtime} open={general.messageOpen} onOpenChange={general.setMessageOpen}/>
    <BottomSheet open={general.restarting} onOpenChange={open => {if (!open && !general.busy) general.setRestarting(false);}} testID="server-general-restart"
      title={t('web.maintenance.serverRestart')} description={t('server.restart.body')} children={null}
      footer={<>
        <Button variant="glass" align="stretch" disabled={general.busy} onPress={() => general.setRestarting(false)}>{t('action.cancel')}</Button>
        <Button variant="danger" align="stretch" loading={general.busy} onPress={general.restart}>{t('web.maintenance.serverRestart')}</Button>
      </>}/>
  </>;
}
