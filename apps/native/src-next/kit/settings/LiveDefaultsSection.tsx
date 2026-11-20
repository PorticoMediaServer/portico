import React from 'react';
import {Text, View} from 'react-native';
import {liveRewindRow, liveStabilityPatch, liveStabilityRow, liveStabilityValue} from '../../screens/settings-live/live-defaults-presentation';
import type {NativeLiveOwnerController} from '../../screens/settings-live/live-owner-controller';
import type {NativeShellRuntime} from '../../shell/runtime';
import {useLiveOwnerDefaults} from '../../pages/shared/settings/useLiveOwnerDefaults';
import {Button, Select, Switch, TextField} from '../controls';
import {PanelBlock, SectionCard} from '../frames';
import {SettingsRow} from '../shell';
import {StateView} from '../states';
import {useTheme} from '../theme';
import {NativeAdvancedSection} from './AdvancedSection';

export function LiveDefaultsSection({controller, runtime}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime}) {
  const theme = useTheme();
  const {t, advanced, setAdvanced, custom, setCustom, document, draft, error, busy, saved, dirty, patch, save, retry, guideDayChoices} =
    useLiveOwnerDefaults(controller, runtime);
  return <>
    {error !== undefined && <StateView testID="live-owner-defaults-error" {...runtime.error(error, retry)}/>}
    {!document && error === undefined && <SectionCard label={t('web.dvrSettings.liveDefaults')}><SettingsRow first label={t('web.librarySettings.loading')}/></SectionCard>}
    {draft && document && <SectionCard label={t('web.dvrSettings.liveDefaults')}><PanelBlock>
      <Select testID="live-owner-defaults-stability" label={t(liveStabilityRow.label)} value={custom ? 'custom' : liveStabilityValue(draft)}
        options={(liveStabilityRow.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined}))}
        onValueChange={value => {const next = liveStabilityPatch(value); setCustom(value === 'custom'); if (next) patch(next); else if (value === 'custom') setAdvanced(true);}}/>
      {draft.timeshiftMinutes !== undefined && <Select testID="live-owner-defaults-rewind" label={t(liveRewindRow.label)} description={liveRewindRow.help ? t(liveRewindRow.help) : undefined} value={String(draft.timeshiftMinutes)}
        options={(liveRewindRow.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label)}))}
        onValueChange={value => {const minutes = Number(value); if (minutes === 10 || minutes === 30 || minutes === 60 || minutes === 120) patch({timeshiftMinutes: minutes});}}/>}
      <Select testID="live-owner-defaults-days" label={t('web.dvrSettings.guideDays')} description={t('web.dvrSettings.guideDaysHelp')} value={String(draft.guideDays)}
        options={guideDayChoices.map(days => ({value: String(days), label: t('server.export.days', {count: days})}))}
        onValueChange={value => patch({guideDays: Number(value)})}/>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}><Text style={[theme.type.rowLabel, {color: theme.colors.text, flex: 1}]}>{t('web.dvrSettings.logoImport')}</Text><Switch testID="live-owner-defaults-logos" accessibilityLabel={t('web.dvrSettings.logoImport')} checked={draft.logoImport} onCheckedChange={logoImport => patch({logoImport})}/></View>
    </PanelBlock></SectionCard>}
    {draft && document && <>
      <NativeAdvancedSection testID="live-owner-defaults-advanced" open={advanced} onOpenChange={setAdvanced}>
        <PanelBlock>
          <TextField testID="live-owner-defaults-buffer" label={t('web.dvrSettings.buffer')} description={t('web.dvrSettings.bufferHelp')} keyboardType="number-pad" value={String(draft.streamBufferMs / 1000)} onChangeText={value => patch({streamBufferMs: Math.max(0, Math.round(Number(value) || 0)) * 1000})}/>
          <TextField testID="live-owner-defaults-retry" label={t('web.dvrSettings.retryWindow')} description={t('web.dvrSettings.retryWindowHelp')} keyboardType="number-pad" value={String(draft.retryWindowMs / 1000)} onChangeText={value => patch({retryWindowMs: Math.max(0, Math.round(Number(value) || 0)) * 1000})}/>
          <TextField testID="live-owner-defaults-agent" label={t('web.dvrSettings.userAgent')} description={t('web.dvrSettings.userAgentHelp')} value={draft.userAgent} autoCapitalize="none" autoCorrect={false} onChangeText={userAgent => patch({userAgent})}/>
          <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}><View style={{flex: 1}}><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{t('web.dvrSettings.discovery')}</Text><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('web.dvrSettings.discoveryHelp')}</Text></View><Switch testID="live-owner-defaults-discovery" accessibilityLabel={t('web.dvrSettings.discovery')} checked={draft.discoveryEnabled} onCheckedChange={discoveryEnabled => patch({discoveryEnabled})}/></View>
        </PanelBlock>
        <SectionCard label={t('server.live.limits')}>
          <PanelBlock>
            <TextField testID="live-owner-defaults-channels" label={t('server.live.limitChannels')} keyboardType="number-pad" value={String(draft.importLimits.channels)} onChangeText={value => patch({importLimits: {...draft.importLimits, channels: Math.max(1, Math.round(Number(value) || 1))}})}/>
            <TextField testID="live-owner-defaults-programmes" label={t('server.live.limitProgrammes')} keyboardType="number-pad" value={String(draft.importLimits.programmes)} onChangeText={value => patch({importLimits: {...draft.importLimits, programmes: Math.max(1, Math.round(Number(value) || 1))}})}/>
            <TextField testID="live-owner-defaults-size" label={`${t('server.live.limitSize')} (${t('settings.unit.mb')})`} keyboardType="number-pad" value={String(Math.round(draft.importLimits.bytes / 1048576))} onChangeText={value => patch({importLimits: {...draft.importLimits, bytes: Math.max(1, Math.round(Number(value) || 1)) * 1048576}})}/>
          </PanelBlock>
        </SectionCard>
      </NativeAdvancedSection>
      <SectionCard><PanelBlock>
        <Button testID="live-owner-defaults-save" loading={busy} disabled={!dirty} onPress={() => void save()}>{t('action.save')}</Button>
        {saved && <Text style={[theme.type.rowHelp, {color: theme.colors.healthy}]}>{t('server.live.saved')}</Text>}
      </PanelBlock></SectionCard>
    </>}
  </>;
}
