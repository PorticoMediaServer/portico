import React from 'react';
import {Text} from 'react-native';
import {when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {useReportsControls, type Group} from '../../../pages/shared/settings/useTroubleshootingReports';
import {Button, FormError, Select} from '../../controls';
import {PanelBlock, SectionCard} from '../../frames';
import {BottomSheet} from '../../overlays';
import {OverlayFootRow} from '../../overlays/surface';
import {SettingsRow} from '../../shell';
import {useTheme} from '../../theme';
import {SettingsHelp} from '../SettingsParts';

const LANES = [{value: 'runtime', label: 'web.logs.laneServer'}, {value: 'audit', label: 'web.logs.laneAudit'}] as const;

export function NativeReports({group}: {group: Group}) {
  const report = useReportsControls({group}), theme = useTheme();
  const {t, records, state, source, current, held, heldConfirmation, busy, error, body} = report;
  const ready = state.phase === 'ready';
  const recordsCurrent = () => current() && source.getSnapshot().records === records;
  return <>
    <SectionCard label={t('web.diagnostics.clientReports')} help={t('server.reports.lede')}>
      {state.phase === 'error' && <PanelBlock><FormError placement="inline">{t('error.unavailable')}</FormError></PanelBlock>}
      {state.phase === 'loading' && <SettingsRow first label={t('next.settings.loading')}/>}
      {ready && !state.reports.length && <SettingsRow first label={t('server.reports.none')}/>}
      {state.reports.map((item, index) => <SettingsRow key={item.id} first={index === 0}
        label={item.deviceName || t('server.reports.unknownSender')}
        description={[item.message?.split('\n')[0], when(Date.parse(item.createdAt))].filter((value): value is string => !!value)}
        navigates={ready} onPress={ready ? () => {void report.open(item);} : undefined}/>)}
      <PanelBlock>
        <Button size="compact" variant="glass" disabled={state.phase === 'loading'} onPress={report.refresh}>{t('web.selection.refresh')}</Button>
        {state.next && <Button size="compact" disabled={!ready} onPress={report.more}>{t('action.showMore')}</Button>}
      </PanelBlock>
    </SectionCard>
    {records && <SectionCard label={t('settings.server.records')} help={t('next.settings.recordsHelp')}>
      <PanelBlock>
        <Select label={t('settings.server.records')} value={records.lane}
          options={LANES.map(lane => ({value: lane.value, label: t(lane.label)}))}
          onValueChange={value => {
            const lane = LANES.find(option => option.value === value);
            if (lane && recordsCurrent()) void source.selectLane(lane.value);
          }}/>
      </PanelBlock>
      {records.items.map((item, index) => <SettingsRow key={`${item.at}:${index}`} first={index === 0}
        label={`${when(Date.parse(item.at))} · ${item.message}`}
        description={[item.actor, item.target, item.detail].filter((value): value is string => !!value)}/>)}
      {!records.items.length && <SettingsRow first label={t('settings.server.records.none')}/>}
      <PanelBlock><OverlayFootRow layout="halves">
        <Button align="stretch" size="compact" variant="glass" onPress={() => {if (recordsCurrent()) void source.latestRecords();}}>{t('next.settings.recordsLatest')}</Button>
        {records.next && <Button align="stretch" size="compact" variant="glass" onPress={() => {if (recordsCurrent()) void source.moreRecords();}}>{t('next.settings.recordsOlder')}</Button>}
      </OverlayFootRow></PanelBlock>
    </SectionCard>}
    <BottomSheet open={!!held} onOpenChange={open => {if (!open) report.close();}}
      title={held ? held.report.deviceName || t('server.reports.unknownSender') : undefined}
      footer={<>
        <Button variant="glass" align="stretch" onPress={report.close}>{t('action.close')}</Button>
        {report.savePort && <Button variant="glass" align="stretch" disabled={busy || !ready} onPress={() => {void report.save();}}>{t('action.save')}</Button>}
        <Button variant="glass" align="stretch" disabled={busy || !ready} onPress={report.ask}>{t('action.delete')}</Button>
      </>}>
      <PanelBlock inset={false}>
        {busy && body === undefined && <SettingsHelp>{t('next.settings.loading')}</SettingsHelp>}
        {body !== undefined && <Text selectable style={[theme.type.rowHelp, {color: theme.colors.textSecondary, fontFamily: 'monospace'}]}>
          {body || t('server.reports.empty')}
        </Text>}
        {error && !heldConfirmation && <FormError placement="inline">{t('error.unavailable')}</FormError>}
      </PanelBlock>
    </BottomSheet>
    <BottomSheet open={!!heldConfirmation} onOpenChange={open => {if (!open) report.closeConfirmation();}} fitContent
      title={t('server.reports.deleteTitle')} description={t('server.reports.deleteBody')}
      footer={<>
        <Button variant="glass" align="stretch" onPress={report.closeConfirmation}>{t('action.cancel')}</Button>
        <Button variant="danger" align="stretch" disabled={busy || !ready} loading={busy} onPress={() => {void report.remove();}}>{t('action.delete')}</Button>
      </>}>
      {error && <FormError placement="inline">{t('error.unavailable')}</FormError>}
    </BottomSheet>
  </>;
}
