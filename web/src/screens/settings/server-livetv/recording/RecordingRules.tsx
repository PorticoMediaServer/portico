import {MetadataFacts} from '../../../../kit/MetadataFacts';
import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {recordingAllowed, recordingOwnerKey, recordingOwnerLabel} from '@core/server-admin/recording-policy.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {GeneratedArt} from '../../../../kit/artwork';
import {Button, Select, Switch, TextField} from '../../../../kit/controls';
import {Dialog, Menu, useRetained} from '../../../../kit/overlays';
import {SettingsRow} from '../../../../kit/shell/SettingsSurfaces';
import {needsRuleOwner, type RecordingRulesController, type OwnerRule} from './rules-controller';
import type {RecordingPermissionsController} from './permissions-controller';
import styles from '../../../live/owner/owner.module.css';
import fields from '../source-settings.module.css';
import ruleStyles from './recording-rules.module.css';
const t = defaultI18n.t;
export function RecordingRules({controller, permissions, sources}: {controller: RecordingRulesController; permissions: RecordingPermissionsController; sources: readonly {id: string; name: string}[]}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const policy = useSyncExternalStore(permissions.subscribe, permissions.getSnapshot, permissions.getSnapshot);
  const [remove, setRemove] = useState<OwnerRule>();
  const [menu, setMenu] = useState<string>();
  const editor = useRef<HTMLDivElement>(null);
  useEffect(() => {void controller.load();}, [controller]);
  const draft = state.draft;
  const shownDraft = useRetained(draft), shownRemove = useRetained(remove);
  const newDraft = !!draft && !draft.id;
  useEffect(() => {if (newDraft) {const now = controller.getSnapshot();void controller.guide(now.day, now.sourceId);}}, [controller, newDraft]);
  if (!controller.isActive()) return null;
  const owners = policy.owners.filter(row => recordingAllowed(row.owner, policy.grants));
  const chosen = state.series.find(row => row.anchor.programmeId === draft?.anchor?.programmeId && row.anchor.generation === draft.anchor.generation && row.anchor.channelId === draft.anchor.channelId && row.anchor.sourceId === draft.anchor.sourceId);
  const busy = state.saving || !!state.uncertain;
  const allowed = !!draft && owners.some(row => recordingOwnerKey(row.owner) === recordingOwnerKey(draft.owner));
  const valid = !!draft && allowed && !!draft.name.trim() && !!draft.match && !!draft.sourceId && (!!draft.id || !!chosen) && !state.guideLoading;
  const editOptions = (patch: Partial<NonNullable<typeof draft>['options']>) => {if (draft) controller.edit(draft, {options: {...draft.options, ...patch}});};
  return <section className={styles.section}>
    <div className={styles.heading}><h3>{t('web.recordingRules.title')}</h3><Button size="small" disabled={state.loading || busy || !!draft} onClick={() => controller.open()}>{t('web.recordingRules.newRule')}</Button></div>
    <p className={styles.lede}>{t('web.recordingRules.lede')}</p>
    {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error)}</p>}
    {state.loading && <p role="status">{t('status.loading')}</p>}
    {!state.loading && !state.rules.length && <p>{t('web.recordingRules.noRules')}</p>}
    {state.rules.map(rule => <SettingsRow key={rule.id} label={<span className={ruleStyles.label}>
      <span className={ruleStyles.art}><GeneratedArt glyph={Array.from(rule.name.trim())[0]?.toLocaleUpperCase() ?? ''}/></span>
      <span>{rule.name}</span><span className={styles.badge} data-tone={rule.enabled ? 'healthy' : undefined} data-paused={!rule.enabled || undefined}>{t(rule.enabled ? 'web.recordingRules.onRule' : 'web.recordingRules.pausedRule')}</span>
    </span>} help={<MetadataFacts parts={[
      needsRuleOwner(rule) ? t('web.recordingRules.chooseOwner') : undefined,
      !needsRuleOwner(rule) ? t(rule.kind === 'title' ? 'web.recordingRules.titleMatch' : 'web.recordingRules.seriesKind') : undefined,
      sources.find(source => source.id === rule.sourceId)?.name,
      ...policy.owners.filter(row => recordingOwnerKey(row.owner) === recordingOwnerKey(rule.owner)).map(recordingOwnerLabel),
      rule.options.newEpisodesOnly ? t('guide.newOnly') : undefined,
    ]} />}>
<Button size="small" disabled={busy || !!draft} onClick={() => controller.open(rule)}>{t(needsRuleOwner(rule) ? 'web.recordingRules.replaceOwned' : 'web.recordingRules.editRule')}</Button>
      <Menu open={menu === rule.id} onOpenChange={open => setMenu(open ? rule.id : undefined)} title={t('server.row.actions', {name: rule.name})}
        trigger={<Button size="small" variant="icon" icon="i-more" iconOnly disabled={busy || !!draft} aria-label={t('server.row.actions', {name: rule.name})}/>}
        items={[
          {id: 'edit', label: t(needsRuleOwner(rule) ? 'web.recordingRules.replaceOwned' : 'web.recordingRules.editRule'), onSelect: () => controller.open(rule)},
          ...(!needsRuleOwner(rule) ? [{id: 'toggle', label: t(rule.enabled ? 'web.recordingRules.pauseRule' : 'web.recordingRules.resumeRule'), onSelect: () => {void controller.toggle(rule);}}] : []),
          {id: 'remove', label: t('web.recordingRules.remove'), destructive: true, onSelect: () => {if (controller.isActive()) setRemove(rule);}},
        ]}/>
    </SettingsRow>)}
    {state.cursor && <Button size="small" disabled={busy || !!draft} loading={state.loading} onClick={() => void controller.load(true)}>{t('action.showMore')}</Button>}
    <Button size="small" variant="text" disabled={state.loading || busy || !!draft} onClick={() => void controller.load()}>{t('action.refresh')}</Button>
    {shownDraft && <Dialog open={!!draft} title={t(shownDraft.id ? 'web.recordingRules.editTitle' : 'web.recordingRules.newTitle')} width={520} onOpenChange={open => {if (!open) controller.close(shownDraft);}}
      footer={<><Button size="small" variant="text" disabled={busy} onClick={() => controller.close(shownDraft)}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={state.saving} disabled={!valid || state.saving} onClick={() => {if (editor.current && [...editor.current.querySelectorAll<HTMLInputElement>('input')].every(input => input.reportValidity())) void controller.save(shownDraft);}}>{t(state.uncertain ? 'action.tryAgain' : 'action.save')}</Button></>}>
      <div className={fields.fields} ref={editor}>
        {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error)}</p>}
        <TextField label={t('web.recordingRules.ruleName')} maxLength={160} value={shownDraft.name} disabled={busy} onChange={event => controller.edit(shownDraft, {name: event.currentTarget.value})}/>
        <Select label={t('web.recordingRules.recordFor')} value={shownDraft.owner.accountId ? recordingOwnerKey(shownDraft.owner) : ''} disabled={busy || policy.loading || !!shownDraft.id} options={[{value: '', label: t('web.recordingRules.chooseProfile')}, ...owners.map(row => ({value: recordingOwnerKey(row.owner), label: recordingOwnerLabel(row)}))]} onValueChange={key => {const row = owners.find(row => recordingOwnerKey(row.owner) === key);if (row) controller.edit(shownDraft, {owner: row.owner});}}/>
        {policy.loading && <p role="status">{t('status.loading')}</p>}
        {!!policy.error && <p role="alert" className={styles.error}>{errorMessage(policy.error)}</p>}
        {!shownDraft.id && policy.cursor && <Button size="small" variant="text" disabled={busy || policy.loading || policy.saving || !!policy.uncertain || !!Object.keys(policy.edits).length} onClick={() => void permissions.load(true)}>{t('action.showMore')}</Button>}
        {!policy.loading && !policy.error && !policy.cursor && !owners.length && <p>{t('web.recordingRules.allowFirst')}</p>}
        {!shownDraft.id ? <>
          <Select label={t('live.guideSource')} value={state.sourceId} disabled={busy} options={[{value: '', label: t('web.live.allSources')}, ...sources.map(row => ({value: row.id, label: row.name}))]} onValueChange={value => {if (controller.getSnapshot().draft === shownDraft) void controller.guide(state.day, value);}}/>
          <TextField type="date" label={t('web.recordingRules.guideDate')} value={state.day} disabled={busy} onChange={event => {if (controller.getSnapshot().draft === shownDraft) void controller.guide(event.currentTarget.value, state.sourceId);}}/>
          {state.guideLoading && <p role="status">{t('web.recordingRules.readingGuide')}</p>}
          <Select label={t('web.recordingRules.seriesFromGuide')} value={chosen?.key ?? ''} disabled={busy || state.guideLoading} options={[{value: '', label: t('web.recordingRules.chooseProgram')}, ...state.series.map(row => ({value: row.key, label: t('web.recordingRules.seriesOption', {title: row.title, channel: row.channelName, time: defaultI18n.formatDate(row.startsAt, {hour: 'numeric', minute: '2-digit'}), available: row.recordAvailable ? 'yes' : 'no'})}))]} onValueChange={key => {const row = state.series.find(row => row.key === key);if (row) controller.choose(shownDraft, row);}}/>
          {!state.guideLoading && !state.error && !state.series.length && <p>{t('web.recordingRules.noSeries')}</p>}
          <div className={fields.pair}><Button size="small" variant="text" disabled={busy || state.guideLoading} onClick={() => {if (controller.getSnapshot().draft === shownDraft) void controller.guide(state.day, state.sourceId);}}>{t('web.recordingRules.firstChannels')}</Button>{state.guideCursor && <Button size="small" variant="text" disabled={busy || state.guideLoading} onClick={() => {if (controller.getSnapshot().draft === shownDraft) void controller.guide(state.day, state.sourceId, state.guideCursor);}}>{t('web.live.empty.moreTitle')}</Button>}</div>
          {chosen && !chosen.recordAvailable && <p>{t('web.recordingRules.captureUnavailable')}</p>}
        </> : <p>{t('web.recordingRules.fixedNote')}</p>}
        {shownDraft.kind === 'title' && <p>{t('web.recordingRules.titleMatch')}</p>}
        <SettingsRow label={t('web.recordingRules.pausedRule')}><Switch checked={!shownDraft.enabled} disabled={busy || !!chosen && !chosen.recordAvailable} aria-label={t('web.recordingRules.pausedRule')} onCheckedChange={paused => controller.edit(shownDraft, {enabled: !paused})}/></SettingsRow>
        <SettingsRow label={t('guide.newOnly')}><Switch checked={shownDraft.options.newEpisodesOnly} disabled={busy || !shownDraft.options.newEpisodesOnly && (!chosen || !['new', 'repeat'].includes(chosen.newEvidence))} aria-label={t('guide.newOnly')} onCheckedChange={newEpisodesOnly => editOptions({newEpisodesOnly})}/></SettingsRow>
        <div className={fields.pair}>{(['prePaddingMs', 'postPaddingMs'] as const).map(key => <TextField key={key} label={t(key === 'prePaddingMs' ? 'web.recordingRules.startEarlySeconds' : 'web.recordingRules.endLateSeconds')} type="number" min={0} max={key === 'prePaddingMs' ? 1800 : 7200} step={1} placeholder={t('web.recordingRules.default')} value={shownDraft.options[key] === null ? '' : shownDraft.options[key] / 1000} disabled={busy} onChange={event => {const raw = event.currentTarget.value, value = Number(raw);if (!raw || Number.isInteger(value) && value >= 0 && value <= (key === 'prePaddingMs' ? 1800 : 7200)) editOptions({[key]: raw ? value * 1000 : null});}}/>)}</div>
        <TextField label={t('web.recordingRules.priority')} description={t('web.recordingRules.priorityHelp')} type="number" min={-100} max={100} value={shownDraft.options.priority} disabled={busy} onChange={event => {const value = Number(event.currentTarget.value);if (Number.isInteger(value) && value >= -100 && value <= 100) editOptions({priority: value});}}/>
        <Select label={t('web.dvr.keepLabel')} value={shownDraft.options.keepPolicy?.mode ?? 'default'} disabled={busy} options={[{value: 'default', label: t('web.recordingRules.useDefaults')}, {value: 'keep-all', label: t('web.logs.everything')}, {value: 'keep-count', label: t('web.recordingRules.latestFew')}, {value: 'keep-days', label: t('web.dvrSettings.keepDays')}]} onValueChange={value => {if (value === 'default') editOptions({keepPolicy: null});else if (value === 'keep-all' || value === 'keep-count' || value === 'keep-days') editOptions({keepPolicy: {mode: value, keepCount: value === 'keep-count' ? shownDraft.options.keepPolicy?.keepCount || 5 : 0, keepDays: value === 'keep-days' ? shownDraft.options.keepPolicy?.keepDays || 30 : 0, keepUntilWatched: false}});}}/>
        {shownDraft.options.keepPolicy && shownDraft.options.keepPolicy.mode !== 'keep-all' && <TextField label={t(shownDraft.options.keepPolicy.mode === 'keep-count' ? 'web.dvrSettings.howMany' : 'web.maintenance.days')} type="number" min={1} max={shownDraft.options.keepPolicy.mode === 'keep-count' ? 1000 : 3650} value={shownDraft.options.keepPolicy.mode === 'keep-count' ? shownDraft.options.keepPolicy.keepCount : shownDraft.options.keepPolicy.keepDays} disabled={busy} onChange={event => {const keep = shownDraft.options.keepPolicy, value = Number(event.currentTarget.value);if (keep && Number.isInteger(value) && value >= 1 && value <= (keep.mode === 'keep-count' ? 1000 : 3650)) editOptions({keepPolicy: {...keep, [keep.mode === 'keep-count' ? 'keepCount' : 'keepDays']: value}});}}/>}
      </div>
    </Dialog>}
    {shownRemove && <Dialog open={!!remove} title={t('web.recordingRules.removeTitle', {name: shownRemove.name})} description={t('web.recordingRules.removeBody')} onOpenChange={open => {if (!open && !busy) setRemove(current => current === shownRemove ? undefined : current);}}
      footer={<><Button size="small" variant="text" disabled={busy} onClick={() => setRemove(current => current === shownRemove ? undefined : current)}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={state.saving} onClick={() => {if (controller.isActive()) void controller.remove(shownRemove).then(ok => {if (ok && controller.isActive()) setRemove(current => current === shownRemove ? undefined : current);});}}>{t(state.uncertain ? 'action.tryAgain' : 'web.recordingRules.remove')}</Button></>}>
      {!!state.error && <p role="alert" className={styles.error}>{errorMessage(state.error)}</p>}
    </Dialog>}
  </section>;
}
