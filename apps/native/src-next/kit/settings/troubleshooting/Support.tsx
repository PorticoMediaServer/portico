import React from 'react';
import {Text} from 'react-native';
import {SUPPORT_COMPONENTS, type SupportComponent, OWNER_SUPPORT_WINDOWS, SUPPORT_CAPABILITY_STATES, supportCapabilityName} from '../../../../../../packages/client-core/src/server-admin/support-next';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {useSupportControls, type Group} from '../../../pages/shared/settings/useTroubleshootingSupport';
import {Button, FormError, Select, Switch} from '../../controls';
import {PanelBlock, SectionCard, StateRow} from '../../frames';
import {SettingsRow} from '../../shell';
import {useTheme} from '../../theme';
import {SettingsHelp} from '../SettingsParts';

const PARTS: Readonly<Record<SupportComponent, MessageId>> = {
  runtime: 'web.logs.includeServer', client: 'web.logs.includeClient', audit: 'web.logs.includeAudit', snapshot: 'web.logs.includeSnapshot',
};
const WINDOWS = OWNER_SUPPORT_WINDOWS.map((minutes, index) => ({
  minutes, label: `next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.${index}.label` as MessageId,
}));

export function NativeSupport({group}: {group: Group}) {
  const support = useSupportControls({group}), theme = useTheme();
  const {t, state, permissions, skipped, window, parts, paths, exporting, failed, savePort} = support;
  return <>
    {state.phase === 'error' && <SectionCard><StateRow kind="error" icon="i-alert" title={t('error.unavailable')}
      action={{label: t('action.tryAgain'), onPress: support.load}}/></SectionCard>}
    {state.phase === 'loading' && !state.facts && <SectionCard><SettingsRow first label={t('next.settings.loading')}/></SectionCard>}
    {permissions?.readableByOthers && <SectionCard label={t('web.maintenance.permissionsTitle')}>
      <PanelBlock>
        <SettingsHelp>{permissions.fixable ? t('web.maintenance.permissionsBody') : permissions.message ?? t('web.maintenance.permissionsBody')}</SettingsHelp>
        {permissions.fixable && <Button size="compact" variant="glass" loading={support.fixing} disabled={support.fixing}
          onPress={() => {void support.fix();}}>{t('web.maintenance.fixPermissions')}</Button>}
      </PanelBlock>
    </SectionCard>}
    {support.fixed && !permissions?.readableByOthers && <SectionCard><SettingsRow first label={t('web.maintenance.permissionsFixed')}/></SectionCard>}
    {state.facts && <SectionCard label={t('web.capabilities.title')} help={t('web.capabilities.help')}>
      {state.facts.capabilities.map((capability, index) => {
        const word = SUPPORT_CAPABILITY_STATES[capability.state];
        const color = word?.tone === 'ok' ? theme.colors.healthy : word?.tone === 'warn' ? theme.colors.amber : theme.colors.textTertiary;
        return <React.Fragment key={`${capability.area}/${capability.name}/${capability.path ?? ''}`}>
          <SettingsRow first={index === 0} label={supportCapabilityName(capability, t)}
            description={[capability.reason, capability.detail].filter((value): value is string => !!value)}
            control={word && <Text style={[theme.type.rowHelp, {color, flexShrink: 1}]}>{t(word.label)}</Text>}/>
          {capability.path && <PanelBlock><Text selectable style={[theme.type.rowHelp, {color: theme.colors.textTertiary, fontFamily: 'monospace'}]}>
            {capability.path}
          </Text></PanelBlock>}
        </React.Fragment>;
      })}
    </SectionCard>}
    <SectionCard label={t('settings.server.skippedParts')} help={t('settings.server.skippedPartsLede')}>
      {skipped.map((part, index) => <SettingsRow key={`${part.document}:${part.part}`} first={index === 0}
        label={part.document} description={part.part} value={t('settings.server.skippedPartsCount', {count: part.count})}/>)}
      {!skipped.length && <SettingsRow first label={t('settings.server.skippedPartsNone')}/>}
      {skipped.length > 0 && <PanelBlock>
        <Button size="compact" variant="glass" onPress={support.clearSkipped}>{t('action.clearAll')}</Button>
      </PanelBlock>}
    </SectionCard>
    <SectionCard label={t('web.logs.exportTitle')} help={t('web.logs.exportLede')}>
      <PanelBlock>
        <Select label={t('web.logs.timeWindow')} value={String(window)} disabled={exporting}
          options={WINDOWS.map(entry => ({value: String(entry.minutes), label: t(entry.label)}))}
          onValueChange={value => {
            const found = WINDOWS.find(entry => String(entry.minutes) === value);
            if (found) support.setWindow(found.minutes);
          }}/>
        {SUPPORT_COMPONENTS.map(component => <Switch key={component} checked={parts.includes(component)} disabled={exporting}
          onCheckedChange={checked => support.changePart(component, checked)}>{t(PARTS[component])}</Switch>)}
        <Switch checked={paths} disabled={exporting || !parts.includes('snapshot')} onCheckedChange={support.setPaths}>
          {t('next.settingsSpec.server.troubleshooting.groups.1.rows.0.children.0.label')}
        </Switch>
        <SettingsHelp>{t('web.logs.pathsHelp')}</SettingsHelp>
        {savePort ? <Button testID="server-support-export" size="compact" disabled={exporting || !parts.length}
          loading={exporting} onPress={() => {void support.exportNow();}}>{t('web.logs.prepare')}</Button>
          : <SettingsHelp>{t('error.unavailable')}</SettingsHelp>}
        {failed && <FormError placement="inline">{t('error.unavailable')}</FormError>}
      </PanelBlock>
    </SectionCard>
  </>;
}
