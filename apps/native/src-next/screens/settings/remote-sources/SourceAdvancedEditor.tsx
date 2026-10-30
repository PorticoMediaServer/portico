import React, {useEffect, useMemo, useRef, useState} from 'react';
import {Text} from 'react-native';
import type {RemoteSource, RemoteSourceStatus, SourceAdvanced, SourceAdvancedSettings} from '../../../../../../packages/client-core/src/remote-sources';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Select} from '../../../kit/controls/Select';
import {Switch} from '../../../kit/controls/Switch';
import {useTheme} from '../../../kit/theme';
import type {NativeRemoteSourcesController} from './controller';
import {Page, Problem, remoteSourceError, useAttempt} from './RemoteSourceForms';
const GIB = 1024 * 1024 * 1024;

/** The canonical Advanced CAS, with only the controls the source can apply. */
export function SourceAdvancedEditor({controller, source, t, isCurrent, onCancel}: {
  controller: NativeRemoteSourcesController; source: RemoteSource; t: typeof defaultI18n.t; isCurrent: () => boolean; onCancel: () => void;
}) {
  const theme = useTheme(), current = useAttempt(isCurrent), flight = useRef(false), revision = useRef(0);
  const [settings, setSettings] = useState<SourceAdvancedSettings>(), [values, setValues] = useState<SourceAdvanced>();
  const [facts, setFacts] = useState<RemoteSourceStatus>();
  const [busy, setBusy] = useState(false), [error, setError] = useState<string>(), [saved, setSaved] = useState(false);
  const factsFlight = useRef(false);
  const draft = useMemo(() => Object.freeze({settings, values}), [settings, values]);
  const latestDraft = useRef(draft); latestDraft.current = draft;
  const renderedRevision = revision.current;
  const ownsDraft = () => current() && revision.current === renderedRevision && latestDraft.current === draft;
  const accept = (next: SourceAdvancedSettings) => {setSettings(next); setValues(next.values);};
  const load = async () => {
    if (flight.current || !current()) return;
    flight.current = true; revision.current++; setBusy(true); setError(undefined); setSaved(false);
    try {const next = await controller.readAdvanced(source, current); if (current()) accept(next);}
    catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {flight.current = false; if (current()) setBusy(false);}
  };
  const loadFacts = async () => {
    if (factsFlight.current || !current()) return;
    factsFlight.current = true;
    try {const next = await controller.sourceStatus(source, current); if (current()) setFacts(next);}
    catch {if (current()) setFacts(undefined);}
    finally {factsFlight.current = false;}
  };
  useEffect(() => {void load(); void loadFacts();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const save = async () => {
    if (flight.current || !ownsDraft() || !draft.settings || !draft.values) return;
    flight.current = true; setBusy(true); setError(undefined); setSaved(false);
    try {const next = await controller.saveAdvanced(draft.settings, draft.values, ownsDraft); if (ownsDraft()) {accept(next); setSaved(true);}}
    catch (problem) {if (current()) setError(remoteSourceError(t, problem));}
    finally {flight.current = false; if (current()) setBusy(false);}
  };
  const set = <K extends keyof SourceAdvanced>(key: K, value: SourceAdvanced[K]) => {if (values && !flight.current && ownsDraft()) {revision.current++; setValues({...values, [key]: value}); setSaved(false);}};
  const spec = 'next.settingsSpec.server.remote-sources.panels.source';
  const label = (suffix: string) => t(`${spec}.${suffix}` as never);
  const intervals = [900, 3600, 21600, 86400, 604800, 0], days = [7, 14, 30, 0], cache = [0, 2, 4, 8, 16, 32, 64], floors = [1, 2, 4, 8, 16];
  const switches = [
    ['watchForChanges', 'tabs.3.groups.0.rows.1.label'], ['scanOnStart', 'tabs.3.groups.0.rows.2.label'], ['doubleCheckFolders', 'tabs.3.groups.0.rows.4.label'],
    ['seekPreviews', 'tabs.3.groups.1.rows.2.control.items.0.label'], ['chapterImages', 'tabs.3.groups.1.rows.2.control.items.1.label'],
    ['waveforms', 'tabs.3.groups.1.rows.2.control.items.2.label'], ['loudness', 'tabs.3.groups.1.rows.2.control.items.3.label'],
    ['checksums', 'tabs.3.groups.1.rows.2.control.items.4.label'],
  ] as const;
  return <Page title={t('next.remoteSources.advanced')} t={t} busy={busy} onCancel={onCancel}
    footer={<Button disabled={busy || !settings || !values} onPress={() => {void save();}}>{t('next.remoteSources.save')}</Button>}>
    <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.advancedHelp')}</Text>
    {values && settings && <>
      {settings.scanIntervalObserved > 0 && !intervals.includes(settings.scanIntervalObserved) && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>
        {t('next.remoteSources.advObserved', {minutes: Math.round(settings.scanIntervalObserved / 60)})} {t('next.remoteSources.advObservedHelp')}
      </Text>}
      <Select disabled={busy} label={label('tabs.3.groups.0.rows.0.label')} value={String(values.scanIntervalSeconds)}
        options={intervals.map((value, index) => ({value: String(value), label: label(`tabs.3.groups.0.rows.0.control.options.${index}.label`)}))}
        onValueChange={value => {const next = Number(value); if (intervals.includes(next)) set('scanIntervalSeconds', next);}}/>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.advIntervalHint')}</Text>
      <Select disabled={busy} label={label('tabs.3.groups.0.rows.3.label')} value={String(values.fullRecheckDays)}
        options={days.map((value, index) => ({value: String(value), label: label(`tabs.3.groups.0.rows.3.control.options.${index}.label`)}))}
        onValueChange={value => {const next = Number(value); if (days.includes(next)) set('fullRecheckDays', next);}}/>
      {switches.map(([key, copy]) => <Switch key={key} checked={values[key]} disabled={busy} onCheckedChange={value => set(key, value)}>{label(copy)}</Switch>)}
      <Select disabled={busy} label={label('tabs.3.groups.1.rows.3.label')} value={values.introsMode}
        options={(['off', 'inherit', 'sound', 'picture'] as const).map((value, index) => ({value, label: label(`tabs.3.groups.1.rows.3.control.options.${index}.label`)}))}
        onValueChange={value => {if (value === 'off' || value === 'inherit' || value === 'sound' || value === 'picture') set('introsMode', value);}}/>
      {settings.cacheSupported && <>
        <Select disabled={busy} label={label('tabs.1.groups.0.rows.1.label')} value={String(values.cacheBytes / GIB)}
          options={cache.map((value, index) => ({value: String(value), label: label(`tabs.1.groups.0.rows.1.control.options.${index}.label`)}))}
          onValueChange={value => {const next = Number(value); if (cache.includes(next)) set('cacheBytes', next * GIB);}}/>
        <Select disabled={busy} label={label('tabs.3.groups.2.rows.0.label')} value={String(values.cacheFloorBytes / GIB)}
          options={floors.map((value, index) => ({value: String(value), label: label(`tabs.3.groups.2.rows.0.control.options.${index}.label`)}))}
          onValueChange={value => {const next = Number(value); if (floors.includes(next)) set('cacheFloorBytes', next * GIB);}}/>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.advCacheUsed', {used: defaultI18n.bytes(settings.cacheUsedBytes)})}</Text>
        {!settings.cacheApplied && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.advCachePending')}</Text>}
        {settings.cacheError && <Problem message={t(settings.cacheError === 'source_changed' ? 'next.remoteSources.advCacheChanged'
          : settings.cacheError === 'mount_unavailable' ? 'next.remoteSources.advCacheUnavailable' : 'next.remoteSources.advCacheFailed')}/>}
      </>}
      {(!!facts?.remote || !!facts?.backgroundSpeed) && <>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.remoteSources.connection')}</Text>
        {!!facts?.remote && <>
          <Text style={[theme.type.body, {color: theme.colors.text}]}>{t('next.remoteSources.remote')}</Text>
          <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{facts.remote}</Text>
        </>}
        {!!facts?.backgroundSpeed && <>
          <Text style={[theme.type.body, {color: theme.colors.text}]}>{t('next.remoteSources.backgroundSpeed')}</Text>
          <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{facts.backgroundSpeed}</Text>
          <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.backgroundSpeedHelp')}</Text>
        </>}
      </>}
    </>}
    {saved && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.saved')}</Text>}
    <Problem message={error}/>
    <Button disabled={busy} variant="text" onPress={() => {void load(); void loadFacts();}}>{t('next.remoteSources.refresh')}</Button>
  </Page>;
}
