import React from 'react';
import type {SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {NativeAdvancedSection} from '../../../kit/settings/AdvancedSection';
import {NativeSettingControl} from '../../../kit/settings/SettingControl';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {StorageForms} from '../../../screens/settings-live/server/storage/forms';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useSegmentsBulkAction, useSegmentsForms, useServerSegments} from '../../shared/settings/useServerSegments';

type Props = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; onBackHandlerChange?: (handler: (() => boolean) | undefined) => void};
type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

export function ServerSegmentsPage({ports, runtime, retry, onBackHandlerChange, ...frame}: Frame & Props & {retry?: React.ReactNode}) {
  const group = useSegmentsForms(ports, runtime), {t} = runtime.text;
  return <SettingsPageFrame {...frame} title={t('next.settingsSpec.server.segments.title')}>
    {retry}
    {group ? <SegmentsSettings key={group.mount} group={group} ports={ports} runtime={runtime} onBackHandlerChange={onBackHandlerChange}/>
      : <SectionCard><StateRow kind="loading"/></SectionCard>}
  </SettingsPageFrame>;
}

function SegmentsSettings({group, ports, runtime, onBackHandlerChange}: Props & {group: StorageForms}) {
  const model = useServerSegments(group, runtime), {state, settings} = model, {t} = runtime.text;
  if (settings.loadError !== undefined && !settings.draft) return <StateView {...runtime.error(settings.loadError, model.retry, 'server-console')}/>;
  return <>
    {model.mode ? <SectionCard><NativeSettingControl {...model.mode}/></SectionCard> : <SectionCard><StateRow kind="loading"/></SectionCard>}
    {ports.renderSettingsPanel('server.segments', runtime, onBackHandlerChange)}
    <NativeAdvancedSection testID="server-segments-advanced" summary={model.summary}>
      {model.groups.map((groupRows, index) => <SectionCard key={index} label={groupRows.title ? t(groupRows.title) : undefined}>
        {groupRows.controls.map(({row, value}) => value === undefined ? <SegmentsBulkAction key={row.id} row={row} context={group.context} runtime={runtime}/>
          : <NativeSettingControl key={row.id + ':' + model.draftKey} {...model.rowProps(row, value)}/>)}
      </SectionCard>)}
    </NativeAdvancedSection>
    {state.dirty && <PanelBlock><OverlayFootRow layout="halves">
      <Button variant="glass" disabled={state.saving} onPress={model.discard}>{t('action.discard')}</Button>
      <Button loading={state.saving} disabled={!model.canSave} onPress={model.save}>{t('action.save')}</Button>
    </OverlayFootRow></PanelBlock>}
    {state.problem && <StateView title={t(state.problem as MessageId)} tone="error"/>}
    {settings.saveError !== undefined && <StateView {...runtime.error(settings.saveError, model.save, 'server-console')}/>}
  </>;
}

function SegmentsBulkAction({context, row, runtime}: {context: NativeShellBootstrapContext; row: SpecRow; runtime: NativeShellRuntime}) {
  const {t} = runtime.text, action = useSegmentsBulkAction(context, row);
  const label = t(row.control.label ?? row.label), failure = t('next.states.pageFailedBody');
  return <>
    <SettingsRow first label={t(row.label)} description={row.help ? t(row.help) : undefined}
      control={<Button size="compact" variant="glass" loading={action.busy && !action.asking} disabled={action.busy} onPress={action.open}>{label}</Button>}/>
    {action.failed && !action.asking && <StateView variant="inline" title={failure} tone="error"/>}
    {action.done && !action.failed && <SettingsHelp>{t('web.segments.bulkStarted')}</SettingsHelp>}
    {row.control.confirm && <BottomSheet open={action.asking} onOpenChange={open => {if (!open) action.close();}}
      title={label} description={t(row.control.confirm)} fitContent dismissOnBack={!action.busy} dismissOnGesture={!action.busy}
      dismissOnOutsidePress={!action.busy} footer={<OverlayFootRow layout="halves">
        <Button variant="glass" disabled={action.busy} onPress={action.close}>{t('next.settings.cancel')}</Button>
        <Button loading={action.busy} disabled={action.busy} onPress={() => {void action.run();}}>{label}</Button>
      </OverlayFootRow>}>
      {action.failed && <StateView variant="inline" title={failure} tone="error"/>}
    </BottomSheet>}
  </>;
}
