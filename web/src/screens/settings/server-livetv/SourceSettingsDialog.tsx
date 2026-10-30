import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {LiveSourceKind} from '@core/administration.ts';
import {lanConfirmationRoots} from '@core/linear-api.ts';
import {guideOnlyPatch} from '@core/server-admin/panel-logic.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button, Segmented, Select, Switch, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell/SettingsSurfaces';
import {dialogAction} from '../../live/owner/dialog-lifetime';
import type {SourceSettingsController} from './source-settings-controller';
import type {SourceChoicesController} from './source-choices-controller';
import {SourceChoicePicker} from './SourceChoicePicker';
import {bufferingPresets, matchingPreset} from '../../live/owner/settings-presets';
import {SettingsAdvanced} from '../../../kit/settings/SettingsGroups';
import styles from './source-settings.module.css';
const t = defaultI18n.t;
const kinds: Record<LiveSourceKind, MessageId> = {playlist: 'server.live.sourceKind.playlist', 'xmltv-guide': 'server.live.sourceKind.guide', hdhomerun: 'server.live.sourceKind.tuner'};
const filters = {categories: 'server.live.filter.categories', countries: 'server.live.filter.countries', keywords: 'server.live.filter.keywords'} as const;
export function SourceSettingsDialog({controller, choices, name, guides, acquisition, close, changed, sourceBusy = false}: {controller: SourceSettingsController; choices: {groups: SourceChoicesController; channels: SourceChoicesController}; name: string; guides: readonly {id: string; name: string}[]; acquisition?: 'upload' | 'remote'; enabled?: boolean; setEnabled?: (enabled: boolean) => void; sourceBusy?: boolean; close: () => void; changed: () => void}) {
  const owner = useRef(controller);owner.current = controller;
  const [advanced, setAdvanced] = useState(false), [channelChoice, setChannelChoice] = useState<string>(), [guideAddress, setGuideAddress] = useState(false);
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot), {draft, document: doc} = state;
  useEffect(() => {setChannelChoice(undefined);setGuideAddress(false);setAdvanced(false);void controller.load();return () => {controller.dispose();choices.groups.dispose();choices.channels.dispose();};}, [controller, choices]);
  const act = (callback: () => void) => dialogAction(owner, controller, callback);
  const busy = state.saving || state.loading || !!state.logoBusy || sourceBusy, dirty = !!draft && !!doc && JSON.stringify(draft) !== JSON.stringify(doc.settings), guideOnly = draft?.kind === 'xmltv-guide';
  const closeCurrent = act(() => {controller.dispose();if (owner.current === controller) close();});
  const number = (label: MessageId, value: number | null, set: (value: number | null) => void, scale = 1, max = 10000000, placeholder?: string) => <TextField label={t(label)} type="number" min={0} max={max} step={1} disabled={busy} value={value === null ? '' : value / scale} placeholder={placeholder}
    onChange={event => {const value = event.currentTarget.value === '' ? null : event.currentTarget.valueAsNumber;if (value === null || Number.isSafeInteger(value) && value >= 0 && value <= max) {if (owner.current === controller && controller.isActive()) set(value === null ? null : value * scale);}}}/>;
  const filterActive = !!draft && Object.values(draft.filters).some(rule => rule.values.length);
  const modes = [draft?.enabledGroups.length ? 'groups' : '', draft?.enabledChannels.length ? 'picked' : '', filterActive ? 'filter' : ''].filter(Boolean);
  const included = channelChoice ?? (modes.length > 1 ? 'custom' : modes[0] || 'all');
  const chooseChannels = (value: string) => {
    if (busy || !draft || !['all', 'groups', 'picked', 'filter'].includes(value)) return;
    setChannelChoice(value);
    controller.edit({enabledGroups: value === 'groups' ? draft.enabledGroups : [], enabledChannels: value === 'picked' ? draft.enabledChannels : [], filters: value === 'filter' ? draft.filters : {categories: {mode: 'include', values: []}, countries: {mode: 'include', values: []}, keywords: {mode: 'exclude', values: []}}});
  };
  return <Dialog open title={name} width={620} onOpenChange={open => {if (!open) closeCurrent();}}
    footer={<><Button size="small" variant="text" onClick={closeCurrent}>{t('action.close')}</Button><Button size="small" variant="primary" loading={state.saving} disabled={!dirty || busy}
      onClick={act(() => void controller.save().then(saved => {if (saved && owner.current === controller && controller.isActive()) changed();}))}>{t('action.save')}</Button></>}>
    <div className={styles.fields}>
      {state.saved && <p role="status">{t(acquisition === 'upload' ? 'server.live.changesAfterUpload' : acquisition === 'remote' ? 'server.live.settingsSaved' : 'settings.saved')}</p>}
      {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error)}</p>}
      {!draft && state.loading && <p role="status">{t('web.librarySettings.loading')}</p>}
      {draft && doc && <>
        {draft.enabled !== undefined && <SettingsRow label={t('next.settingsSpec.server.live.groups.2.rows.0.label')}><Switch checked={draft.enabled} disabled={busy} aria-label={t('next.settingsSpec.server.live.groups.2.rows.0.label')} onCheckedChange={enabled => controller.edit({enabled})}/></SettingsRow>}
        {draft.viewerAccess && <Select label={t('next.settingsSpec.server.live.groups.2.rows.1.label')} value={draft.viewerAccess} disabled={busy} options={['server-members', 'owner-only'].map((value, index) => ({value, label: t(`next.settingsSpec.server.live.groups.2.rows.1.control.options.${index}.label` as MessageId)}))} onValueChange={viewerAccess => controller.edit({viewerAccess: viewerAccess as 'owner-only' | 'server-members'})}/>}
        <Select label={t('web.liveSource.provides')} value={draft.kind} disabled={busy} options={doc.availableKinds.map(value => ({value, label: t(kinds[value])}))}
          onValueChange={kind => controller.edit(guideOnlyPatch(kind) as Partial<typeof draft>)}/>
        {guideOnly ? <p>{t('web.liveSource.guideOnlyNote')}</p> : <>
          <Select label={t('next.settingsSpec.server.live.groups.2.rows.2.label')} description={t('next.settingsSpec.server.live.groups.2.rows.2.help')} value={draft.guideSourceId || (guideAddress || draft.remote?.guideUrl || draft.remote?.guideUrlRedacted ? 'url' : 'own')} disabled={busy}
            options={[{value: 'own', label: t('next.settingsSpec.server.live.groups.2.rows.2.control.options.0.label')}, ...guides.filter(source => source.id !== controller.sourceId).map(source => ({value: source.id, label: source.name})), ...(draft.remote ? [{value: 'url', label: t('next.settingsSpec.server.live.groups.2.rows.2.control.options.2.label')}] : [])]}
            onValueChange={id => {setGuideAddress(id === 'url');controller.edit({guideSourceId: id === 'own' || id === 'url' ? '' : id, ...id === 'own' && draft.remote ? {remote: {...draft.remote, guideUrl: '', guideUrlRedacted: false}} : {}});}}/>
          {draft.remote && (guideAddress || draft.remote.guideUrl || draft.remote.guideUrlRedacted) && <TextField label={t('next.settingsSpec.server.live.groups.2.rows.2.children.0.label')} value={draft.remote.guideUrl} disabled={busy} onChange={event => controller.edit({remote: {...draft.remote!, guideUrl: event.currentTarget.value, guideUrlRedacted: false}})}/>}
          {draft.remote && <Select label={t('next.settingsSpec.server.live.groups.2.rows.3.label')} value={String(draft.remote.refreshMs)} disabled={busy} options={[3600000, 21600000, 43200000, 86400000].map((value, index) => ({value: String(value), label: t(`next.settingsSpec.server.live.groups.2.rows.3.control.options.${index}.label` as MessageId)})).concat([3600000, 21600000, 43200000, 86400000].includes(draft.remote.refreshMs) ? [] : [{value: String(draft.remote.refreshMs), label: t('next.settingsSpec.server.live.groups.1.rows.0.control.options.3.label')}])} onValueChange={value => controller.edit({remote: {...draft.remote!, refreshMs: Number(value)}})}/>}
          <Select label={t('next.settingsSpec.server.live.groups.3.rows.0.label')} value={included} disabled={busy}
            options={['all', 'groups', 'picked', 'filter', 'custom'].map((value, index) => ({value, disabled: value === 'custom', label: t(index === 4 ? 'next.settingsSpec.server.live.groups.1.rows.0.control.options.3.label' : `next.settingsSpec.server.live.groups.3.rows.0.control.options.${index}.label` as MessageId)}))} onValueChange={chooseChannels}/>
          {(included === 'groups' || included === 'custom') && <SourceChoicePicker controller={choices.groups} value={draft.enabledGroups} change={enabledGroups => controller.edit({enabledGroups})} disabled={busy}/>}
          {(included === 'picked' || included === 'custom') && <SourceChoicePicker controller={choices.channels} value={draft.enabledChannels} change={enabledChannels => controller.edit({enabledChannels})} disabled={busy}/>}
          {(included === 'filter' || included === 'custom') && <section>{(Object.keys(filters) as (keyof typeof filters)[]).map((key, index) => <div key={key} className={styles.filter}>
            <TextField label={t(included === 'custom' || draft.filters[key].values.length > 0 && draft.filters[key].mode !== (key === 'keywords' ? 'exclude' : 'include') ? filters[key] : `next.settingsSpec.server.live.groups.3.rows.0.children.${index + 2}.label` as MessageId)} value={draft.filters[key].values.join(', ')} disabled={busy} onChange={event => controller.edit({filters: {...draft.filters, [key]: {mode: included === 'custom' || draft.filters[key].values.length > 0 ? draft.filters[key].mode : key === 'keywords' ? 'exclude' : 'include', values: event.currentTarget.value.split(',').map(value => value.trim()).filter(Boolean)}}})}/>
            {(included === 'custom' || draft.filters[key].values.length > 0 && draft.filters[key].mode !== (key === 'keywords' ? 'exclude' : 'include')) && <Segmented aria-label={t('web.liveSource.filterMode', {filter: t(filters[key])})} value={draft.filters[key].mode} disabled={busy}
              items={[{value: 'include', label: t('web.liveSource.onlyThese')}, {value: 'exclude', label: t('web.liveSource.allButThese')}]}
              onValueChange={mode => controller.edit({filters: {...draft.filters, [key]: {...draft.filters[key], mode: mode as 'include' | 'exclude'}}})}/>}
          </div>)}</section>}
          <section><h3>{t('web.liveSource.channelNumbers')}</h3><Segmented aria-label={t('web.liveSource.numbering')} value={draft.channelNumbering.mode} disabled={busy}
            items={[{value: 'source', label: t('web.liveSource.asSent')}, {value: 'sequential', label: t('web.liveSource.inOrder')}]}
            onValueChange={mode => controller.edit({channelNumbering: {...draft.channelNumbering, mode: mode as 'source' | 'sequential'}})}/>
            {draft.channelNumbering.mode === 'sequential' && <div className={styles.pair}>
              {number('web.liveSource.startAt', draft.channelNumbering.startAt, value => {if (value && value >= 1) controller.edit({channelNumbering: {...draft.channelNumbering, startAt: value}});})}
              {number('web.liveSource.countBy', draft.channelNumbering.step, value => {if (value && value >= 1) controller.edit({channelNumbering: {...draft.channelNumbering, step: value}});})}
            </div>}</section>
          <section><Select label={t('next.settingsSpec.server.live.groups.2.rows.4.label')} value={draft.logoImport.followServer ? 'server' : !draft.logoImport.enabled ? 'none' : draft.logoImport.overwriteExisting ? 'replace' : 'import'} disabled={busy}
            options={['server', 'import', 'replace', 'none'].map((value, index) => ({value, label: t(`next.settingsSpec.server.live.groups.2.rows.4.control.options.${index}.label` as MessageId)}))}
            onValueChange={value => {if (value === 'server') controller.edit({logoImport: {...draft.logoImport, followServer: true}}); else if (value === 'import' || value === 'replace' || value === 'none') controller.edit({logoImport: {enabled: value !== 'none', overwriteExisting: value === 'replace', followServer: false}});}}/>
            <Button size="small" disabled={busy || dirty || !doc.effectiveLogoImport.enabled} loading={state.logoBusy} onClick={act(() => void controller.importLogos())}>{t('web.liveSource.importLogos')}</Button>
            {state.logoResult && <p role="status">{t('web.liveSource.logoResult', state.logoResult)}</p>}
          </section>
        </>}
        <SettingsAdvanced open={advanced} onOpenChange={setAdvanced} summary={[
          ...(draft.streamBufferMs !== null || draft.retryWindowMs !== null ? [t('next.settingsSpec.server.live.advanced.groups.2.rows.1.label')] : []),
          ...(draft.userAgent ? [t('next.settingsSpec.server.live.advanced.groups.2.rows.2.label')] : []),
        ]}>
          {draft.remote && <>
            {draft.remote.kind === 'xtream' && <>{(['username', 'password'] as const).map(key => <div key={key}><TextField label={t(key === 'username' ? 'web.liveSource.replaceUsername' : 'web.liveSource.replacePassword')} type="password" autoComplete="new-password" disabled={busy} value={draft.remote?.[key] ?? ''} onChange={event => controller.edit({remote: {...draft.remote!, [key]: event.currentTarget.value || undefined}})}/><Button size="small" disabled={busy} onClick={() => controller.edit({remote: {...draft.remote!, [key]: ''}})}>{t(key === 'username' ? 'web.liveSource.clearUsername' : 'web.liveSource.clearPassword')}</Button></div>)}</>}
            {lanConfirmationRoots(state.error) && <Button disabled={busy} onClick={() => controller.edit({remote: {...draft.remote!, confirmedLanRoots: lanConfirmationRoots(state.error)}})}>{t('web.liveSource.confirmGuideNetwork')}</Button>}
          </>}
          {draft.remote && !guideOnly && <Select label={t('next.settingsSpec.server.live.advanced.groups.2.rows.0.label')} value={String(draft.remote.ownerLimit || 'auto')} disabled={busy} options={['auto', 1, 2, 3, 4, 6, 8, ...(![0, 1, 2, 3, 4, 6, 8].includes(draft.remote.ownerLimit) ? [draft.remote.ownerLimit] : [])].map((value, index) => ({value: String(value), label: index === 0 ? t('next.settingsSpec.server.live.advanced.groups.2.rows.0.control.options.0.label') : String(value)}))} onValueChange={value => controller.edit({remote: {...draft.remote!, ownerLimit: value === 'auto' ? 0 : Number(value), useDiscoveredCapacity: value === 'auto' && draft.remote!.kind === 'hdhomerun'}})}/>}
          {!guideOnly && <>
            <Select label={t('next.settingsSpec.server.live.advanced.groups.2.rows.1.label')} value={draft.streamBufferMs === null && draft.retryWindowMs === null ? 'follow' : matchingPreset(bufferingPresets, {streamBufferMs: draft.streamBufferMs ?? -1, retryWindowMs: draft.retryWindowMs ?? -1})} disabled={busy}
              options={[
                ...['follow', 'smooth', 'balanced', 'live'].map((value, index) => ({value, label: t(`next.settingsSpec.server.live.advanced.groups.2.rows.1.control.options.${index}.label` as MessageId)})),
                {value: 'custom', label: t('next.settingsSpec.server.live.groups.1.rows.0.control.options.3.label'), disabled: true},
              ]}
              onValueChange={value => {if (value === 'follow') controller.edit({streamBufferMs: null, retryWindowMs: null});else if (value in bufferingPresets) controller.edit(bufferingPresets[value as keyof typeof bufferingPresets]);}}/>
            <div className={styles.pair}>
              {number('web.liveSource.streamBuffer', draft.streamBufferMs, streamBufferMs => controller.edit({streamBufferMs}), 1000, 120, t('web.liveSource.serverDefault', {value: doc.effective.streamBufferMs / 1000}))}
              {number('web.liveSource.retryFor', draft.retryWindowMs, retryWindowMs => controller.edit({retryWindowMs}), 1000, 600, t('web.liveSource.serverDefault', {value: doc.effective.retryWindowMs / 1000}))}
            </div>
          </>}
          <TextField label={t('next.settingsSpec.server.live.advanced.groups.2.rows.2.label')} value={draft.userAgent} maxLength={200} disabled={busy} placeholder={t('next.settingsSpec.server.live.advanced.groups.2.rows.2.control.placeholder')} onChange={event => controller.edit({userAgent: event.currentTarget.value})}/>
        </SettingsAdvanced>
      </>}
    </div>
  </Dialog>;
}
