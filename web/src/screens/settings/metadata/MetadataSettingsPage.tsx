import {useEffect, useRef, useState, type ReactNode} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {artworkLimitBytes, artworkLimitText, metadataSettingsService, openSubtitlesSummary, type ArtworkMaxEdge, type ArtworkPreference, type ArtworkLanguage, type MetadataAdvanced, type MetadataSettings, type MetadataSettingsChange, type MetadataLibraryChange, type MetadataLibrarySettings} from '@core/metadata-settings.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {presentError} from '@core/presentation/errors.ts';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import {SettingsAdvanced, SettingsGroup} from '../../../kit/settings';
import {Dialog} from '../../../kit/overlays';
import {Skeleton} from '../../../kit/states';
import {MetadataLibraries} from './MetadataLibraries';
import styles from './metadata-settings.module.css';
import {SettingsRow} from '../../../kit/shell/SettingsSurfaces';

const t = defaultI18n.t;
type SecretDraft = Readonly<{value: string; remove: boolean}>;
const emptySecret = (): SecretDraft => ({value: '', remove: false});
type Draft = {artworkLanguage?: ArtworkLanguage;artworkPreference?: ArtworkPreference;onlineLookups?: boolean;defaultLanguage?: string; defaultRegion?: string; libraries: Record<string, MetadataLibraryChange>; limit: string; adult: boolean; edge: ArtworkMaxEdge; tmdb: SecretDraft; tvdb: SecretDraft; subtitleKey: SecretDraft; subtitleUser: string; subtitlePassword: string; advanced?: Pick<MetadataAdvanced, 'refreshPolicy' | 'fallbackLanguage' | 'animeTitles' | 'castPhotos'>; acoustid: SecretDraft};
const draftFor = (document: MetadataSettings): Draft => ({artworkLanguage: document.artworkLanguage, artworkPreference: document.artworkPreference, onlineLookups: document.onlineLookups, defaultLanguage: document.defaultLanguage, defaultRegion: document.defaultRegion, libraries: {}, limit: artworkLimitText(document.localArtworkLimitBytes), adult: document.includeAdult, edge: document.artworkMaxEdge,
  tmdb: emptySecret(), tvdb: emptySecret(), subtitleKey: emptySecret(), subtitleUser: document.openSubtitlesUsername, subtitlePassword: '', acoustid: emptySecret(),
  advanced: document.advanced && {refreshPolicy: document.advanced.refreshPolicy, fallbackLanguage: document.advanced.fallbackLanguage, animeTitles: document.advanced.animeTitles, castPhotos: document.advanced.castPhotos}});
const advancedKeys = ['refreshPolicy', 'fallbackLanguage', 'animeTitles', 'castPhotos'] as const;
const secretValue = (draft: SecretDraft): string | undefined => draft.remove ? '' : draft.value.trim() || undefined;
const terminal = (error: unknown): boolean => {
  if (!error || typeof error !== 'object') return false;
  const value = error as {status?: number; statusCode?: number; code?: string};
  return value.status === 401 || value.status === 403 || value.statusCode === 401 || value.statusCode === 403 || ['unauthorized', 'forbidden', 'not_permitted'].includes(value.code ?? '');
};
/** The Advanced summary names only what differs from the spec defaults. */
function advancedSummary(advanced: MetadataAdvanced): string[] {
  const parts: string[] = [];
  const named = (id: string, value: string, fallback: string) => {
    if (value === fallback) return;
    const option = settingsRowIndex.get(id)?.row.control.options?.find(candidate => String(candidate.value) === value);
    if (option) parts.push(t(option.label));
  };
  named('metadata.refresh', advanced.refreshPolicy, 'regular');
  named('metadata.fallbackLanguage', advanced.fallbackLanguage, 'en');
  named('metadata.animeTitles', advanced.animeTitles, 'language');
  if (!advanced.castPhotos) parts.push(t('server.metadata.castPhotosOff'));
  if (advanced.acoustidKeyConfigured) parts.push(t('next.settingsSpec.server.metadata.advanced.groups.3.title'));
  return parts;
}
const retired = () => Object.assign(new Error('metadata_scope_ended'), {name: 'AbortError'});
type Owned = {service: ReturnType<typeof metadataSettingsService>; current: () => boolean};

/** Only fields with real metadata-settings producers are offered. Credentials are local,
 * write-only drafts; neither a failed read nor a revision conflict silently resends them. */
export function MetadataSettingsPage({api, isCurrent, onLibraries}: {api: LibraryContentApi; isCurrent: () => boolean; onLibraries: () => void}) {
  const active = useRef<{api: LibraryContentApi; isCurrent: () => boolean}>({api, isCurrent}); active.current = {api, isCurrent};
  const [owned, setOwned] = useState<Owned>(), [document, setDocument] = useState<MetadataSettings>(), [draft, setDraft] = useState<Draft>();
  const [busy, setBusy] = useState(false), [error, setError] = useState<string>(), [denied, setDenied] = useState(false), [reload, setReload] = useState(0);
  const flight = useRef(false), refreshTrigger = useRef<HTMLButtonElement>(null);
  const [advancedOpen, setAdvancedOpen] = useState(false), [confirmRefresh, setConfirmRefresh] = useState(false);
  useEffect(() => {
    const lifetime = new AbortController(); let live = true;
    const current = () => live && !lifetime.signal.aborted && active.current.api === api && active.current.isCurrent === isCurrent && isCurrent();
    const clear = () => {live = false; lifetime.abort(); setDenied(true); setDocument(undefined); setDraft(undefined);};
    const service = metadataSettingsService(async <T,>(path: string, method?: string, body?: unknown): Promise<T> => {
      if (!current()) throw retired();
      try {
        const result = await api.request<T>(path, method, body, lifetime.signal);
        if (!current()) throw retired();
        return result;
      } catch (problem) {if (terminal(problem) && current()) clear(); throw problem;}
    });
    const scope = {service, current}; setOwned(scope); setDenied(false); setDocument(undefined); setDraft(undefined); setError(undefined); setBusy(true); flight.current = true;
    void service.read().then(next => {if (current()) {setDocument(next); setDraft(draftFor(next));}}, problem => {
      if (current()) setError(presentError(problem, 'server-console').body);
    }).finally(() => {if (current()) {flight.current = false; setBusy(false);}});
    return () => {live = false; lifetime.abort(); flight.current = false;};
  }, [api, isCurrent, reload]);
  const current = () => !denied && !!owned?.current();
  if (!isCurrent() || denied) return <p role="alert">{t('next.remoteSources.error.forbidden')}</p>;
  const problem = error && <p role="alert">{error}</p>;
  if (!document || !draft || !owned || !owned.current()) return busy ? <div aria-busy="true"><Skeleton quiet height={72}/><Skeleton quiet height={96}/><Skeleton quiet height={96}/></div>
    : <>{problem}<Button size="small" onClick={() => setReload(value => value + 1)}>{t('action.tryAgain')}</Button></>;
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => {if (!flight.current && current()) {setDraft({...draft, [key]: value}); setError(undefined);}};
  const setLibrary = (row: MetadataLibrarySettings, patch: Partial<Omit<MetadataLibraryChange, 'id' | 'expectedRevision'>>) => {
    if (flight.current || !current()) return;
    setDraft(previous => {
      if (!previous) return previous;
      const value: {-readonly [K in keyof MetadataLibraryChange]: MetadataLibraryChange[K]} = {...previous.libraries[row.id], id: row.id, expectedRevision: row.revision, ...patch};
      if (value.agent === 'local') delete value.lyricsSource;
      for (const key of ['agent', 'lyricsSource', 'episodeThumbnailMode', 'scrubPreviews'] as const) if (value[key] === row[key]) delete value[key];
      const libraries = {...previous.libraries};
      if (Object.keys(value).length > 2) libraries[row.id] = value; else delete libraries[row.id];
      return {...previous, libraries};
    });
    setError(undefined);
  };
  const bytes = artworkLimitBytes(draft.limit);
  const subtitleChanges = {
    ...(secretValue(draft.subtitleKey) !== undefined ? {openSubtitlesApiKey: secretValue(draft.subtitleKey)} : {}),
    ...(draft.subtitleUser !== document.openSubtitlesUsername ? {openSubtitlesUsername: draft.subtitleUser.trim()} : {}),
    ...(draft.subtitlePassword ? {openSubtitlesPassword: draft.subtitlePassword} : {}),
  };
  const advancedChanges: Partial<MetadataSettingsChange> = {
    ...Object.fromEntries(advancedKeys.filter(key => draft.advanced && document.advanced && draft.advanced[key] !== document.advanced[key]).map(key => [key, draft.advanced![key]])),
    ...(secretValue(draft.acoustid) !== undefined ? {acoustidKey: secretValue(draft.acoustid)} : {}),
  };
  const advancedCount = Object.keys(advancedChanges).length;
  const dirty = advancedCount > 0 || draft.artworkLanguage !== document.artworkLanguage || draft.artworkPreference !== document.artworkPreference || draft.onlineLookups !== document.onlineLookups || draft.defaultLanguage !== document.defaultLanguage || draft.defaultRegion !== document.defaultRegion || bytes !== document.localArtworkLimitBytes || draft.adult !== document.includeAdult || draft.edge !== document.artworkMaxEdge || secretValue(draft.tmdb) !== undefined || secretValue(draft.tvdb) !== undefined || Object.keys(subtitleChanges).length > 0 || Object.keys(draft.libraries).length > 0;
  const pendingCount = Number(draft.artworkLanguage !== document.artworkLanguage) + Number(draft.artworkPreference !== document.artworkPreference) + Number(draft.onlineLookups !== document.onlineLookups) + Number(draft.defaultLanguage !== document.defaultLanguage) + Number(draft.defaultRegion !== document.defaultRegion) + Number(bytes !== document.localArtworkLimitBytes) + Number(draft.adult !== document.includeAdult) + Number(draft.edge !== document.artworkMaxEdge)
    + Number(secretValue(draft.tmdb) !== undefined) + Number(secretValue(draft.tvdb) !== undefined) + Object.keys(subtitleChanges).length
    + Object.values(draft.libraries).reduce((count, change) => count + Object.keys(change).length - 2, 0) + advancedCount;
  const save = async () => {
    if (flight.current || !current() || bytes === undefined || !dirty) return;
    flight.current = true; setBusy(true); setError(undefined);
    const change: MetadataSettingsChange = {...(draft.artworkLanguage !== document.artworkLanguage ? {artworkLanguage: draft.artworkLanguage} : {}), ...(draft.artworkPreference !== document.artworkPreference ? {artworkPreference: draft.artworkPreference} : {}), ...(draft.onlineLookups !== document.onlineLookups ? {onlineLookups: draft.onlineLookups} : {}), ...(draft.defaultLanguage !== document.defaultLanguage ? {defaultLanguage: draft.defaultLanguage} : {}), ...(draft.defaultRegion !== document.defaultRegion ? {defaultRegion: draft.defaultRegion} : {}), libraries: Object.values(draft.libraries), expectedRevision: document.revision, localArtworkLimitBytes: bytes, includeAdult: draft.adult, artworkMaxEdge: draft.edge,
      ...(secretValue(draft.tmdb) !== undefined ? {tmdbReadToken: secretValue(draft.tmdb)} : {}), ...(secretValue(draft.tvdb) !== undefined ? {tvdbProjectKey: secretValue(draft.tvdb)} : {}), ...subtitleChanges, ...advancedChanges};
    // The explicit request owns these values from here. No retry draft retains a secret.
    setDraft({...draft, tmdb: emptySecret(), tvdb: emptySecret(), subtitleKey: emptySecret(), subtitlePassword: '', acoustid: emptySecret()});
    try {const next = await owned.service.save(change); if (current()) {setDocument(next); setDraft(draftFor(next));}}
    catch (reason) {if (current()) setError(presentError(reason, 'server-console').body);}
    finally {if (current()) {flight.current = false; setBusy(false);}}
  };
  const refreshAll = async () => {
    if (flight.current || !current()) return;
    flight.current = true; setBusy(true); setError(undefined); setConfirmRefresh(false);
    try {const next = await owned.service.refreshAll(); if (current()) setDocument(next);}
    catch (reason) {if (current()) setError(presentError(reason, 'server-console').body);}
    finally {if (current()) {flight.current = false; setBusy(false);}}
  };
  const check = async () => {
    if (flight.current || !current() || Object.keys(subtitleChanges).length) return;
    flight.current = true; setBusy(true); setError(undefined);
    try {const next = await owned.service.checkOpenSubtitles(); if (current()) setDocument({...document, openSubtitles: next});}
    catch (reason) {if (current()) setError(presentError(reason, 'server-console').body);}
    finally {if (current()) {flight.current = false; setBusy(false);}}
  };
  const secret = (key: 'tmdb' | 'tvdb' | 'subtitleKey' | 'acoustid', label: MessageId, configured: boolean): ReactNode => {
    const value = draft[key];
    return <SettingsRow label={t(label)} help={key === 'subtitleKey' ? t('next.settingsSpec.server.metadata.groups.4.rows.1.help') : key === 'acoustid' ? t('next.settingsSpec.server.metadata.advanced.groups.3.rows.0.help') : t('server.metadata.keyHelp')} stacked>
      <div><p>{t(value.remove ? 'server.metadata.keyRemoving' : key === 'acoustid' ? (configured ? 'server.metadata.keySet' : document.advanced?.acoustidKeySource === 'environment' ? 'server.metadata.acoustidServer' : 'server.metadata.acoustidNone') : key === 'subtitleKey' ? document.openSubtitles.keySource === 'owner' ? 'server.metadata.openSubtitlesKeyOwn' : document.openSubtitles.keySource === 'environment' ? 'server.metadata.openSubtitlesKeyEnvironment' : 'server.metadata.openSubtitlesKeyNone' : configured ? 'server.metadata.keySet' : 'server.metadata.keyOurs')}</p>
        <TextField type="password" aria-label={t(label)} value={value.value} autoComplete="off" spellCheck={false} maxLength={2048} disabled={busy || value.remove}
          placeholder={t(configured ? 'server.metadata.keyReplace' : 'server.metadata.keyPaste')} onChange={event => set(key, {value: event.currentTarget.value, remove: false})}/>
        {configured && <Button size="small" variant="text" disabled={busy} onClick={() => set(key, {value: '', remove: !value.remove})}>{t(value.remove ? 'server.metadata.keyKeep' : key === 'subtitleKey' ? 'server.metadata.openSubtitlesRemoveKey' : 'server.metadata.keyRemove')}</Button>}
      </div>
    </SettingsRow>;
  };
  const summary = openSubtitlesSummary(document.openSubtitles, defaultI18n);
  const specSelect = (id: 'metadata.refresh' | 'metadata.fallbackLanguage' | 'metadata.animeTitles', key: 'refreshPolicy' | 'fallbackLanguage' | 'animeTitles') => {
    const row = settingsRowIndex.get(id)?.row;
    if (!row?.control.options || !draft.advanced) return null;
    const options = row.control.options.map(option => ({value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined}));
    return <SettingsRow label={t(row.label)} help={row.help ? t(row.help) : undefined}>
      <Select aria-label={t(row.label)} value={draft.advanced[key]} disabled={busy} options={options} onValueChange={value => {if (options.some(option => option.value === value)) set('advanced', {...draft.advanced!, [key]: value});}}/>
    </SettingsRow>;
  };
  return <div data-testid="server-metadata-settings">
    {problem}
    {document.librariesComplete && !!document.libraries?.length && <MetadataLibraries libraries={document.libraries} changes={draft.libraries} busy={busy} onChange={setLibrary}/>}
    <SettingsGroup title={t('server.metadata.lookups')}>
      {(!document.librariesComplete || !document.libraries?.length) && <SettingsRow label={t('server.metadata.perLibrary')} help={t('server.metadata.perLibraryHelp')}><Button size="small" onClick={() => {if (current()) onLibraries();}}>{t('settings.server.libraries')}</Button></SettingsRow>}
      {typeof document.onlineLookups === 'boolean' ? <SettingsRow label={t('next.settingsSpec.server.metadata.groups.0.rows.0.label')} help={t('next.settingsSpec.server.metadata.groups.0.rows.0.help')}><Switch aria-label={t('next.settingsSpec.server.metadata.groups.0.rows.0.label')} checked={draft.onlineLookups === true} disabled={busy} onCheckedChange={value => set('onlineLookups', value)}/></SettingsRow> : null}
      <SettingsRow label={t('metadata.settings.includeAdult')} help={t('server.metadata.adultHelp')}><Switch aria-label={t('metadata.settings.includeAdult')} checked={draft.adult} disabled={busy} onCheckedChange={value => set('adult', value)}/></SettingsRow>
    </SettingsGroup>
    {(document.defaultLanguage && document.defaultRegion) ? <SettingsGroup title={t('next.settingsSpec.server.metadata.groups.1.title')}>
      {(['metadata.language', 'metadata.region'] as const).map(id => {
        const row = settingsRowIndex.get(id)?.row;
        if (!row?.control.options) return null;
        const field = id === 'metadata.language' ? 'defaultLanguage' : 'defaultRegion';
        return <SettingsRow key={id} label={t(row.label)} help={row.help ? t(row.help) : undefined}>
          <Select aria-label={t(row.label)} value={draft[field] ?? ''} disabled={busy} options={row.control.options.filter(option => typeof option.value === 'string').map(option => ({value: String(option.value), label: t(option.label)}))} onValueChange={value => set(field, value)}/>
        </SettingsRow>;
      })}
    </SettingsGroup> : null}
    <SettingsGroup title={t('next.settingsSpec.server.metadata.groups.2.title')}>
      {secret('tmdb', 'metadata.settings.tmdbToken', document.tmdbCredentialConfigured)}
      {secret('tvdb', 'metadata.settings.tvdbKey', document.tvdbCredentialConfigured)}
      <SettingsRow label={t('next.settingsSpec.server.metadata.groups.2.rows.2.label')} help={t('next.settingsSpec.server.metadata.groups.2.rows.2.help')}>{null}</SettingsRow>
    </SettingsGroup>
    <SettingsGroup title={t('server.metadata.artwork')}>
      <SettingsRow label={t('metadata.settings.artworkLimit')} help={t('server.metadata.artworkUsed', {used: defaultI18n.bytes(document.localArtworkUsedBytes), limit: defaultI18n.bytes(document.localArtworkLimitBytes)})}>
        <TextField aria-label={t('metadata.settings.artworkLimit')} inputMode="decimal" value={draft.limit} disabled={busy} onChange={event => set('limit', event.currentTarget.value)} trailing={t('settings.unit.gb')} error={bytes === undefined ? t('server.metadata.limitProblem') : undefined}/>
      </SettingsRow>
      <progress className={styles.usage} max={document.localArtworkLimitBytes} value={Math.min(document.localArtworkUsedBytes, document.localArtworkLimitBytes)} aria-label={t('metadata.settings.artworkUsage')} aria-valuetext={t('server.metadata.artworkUsed', {used: defaultI18n.bytes(document.localArtworkUsedBytes), limit: defaultI18n.bytes(document.localArtworkLimitBytes)})}/>
      <SettingsRow label={t('next.settingsSpec.server.metadata.advanced.groups.2.rows.1.label')}><Select aria-label={t('next.settingsSpec.server.metadata.advanced.groups.2.rows.1.label')} value={String(draft.edge)} disabled={busy}
        options={[{value: '1280', label: t('next.settingsSpec.server.metadata.advanced.groups.2.rows.1.control.options.0.label')}, {value: '1920', label: t('next.settingsSpec.server.metadata.advanced.groups.2.rows.1.control.options.1.label')}, {value: '2560', label: t('next.settingsSpec.server.metadata.advanced.groups.2.rows.1.control.options.2.label')}]} onValueChange={value => {if (value === '1280' || value === '1920' || value === '2560') set('edge', Number(value) as ArtworkMaxEdge);}}/></SettingsRow>
    </SettingsGroup>
    {(document.artworkPreference || document.artworkLanguage) ? <SettingsGroup title={t('next.settingsSpec.server.metadata.groups.3.title')}>
      {document.artworkPreference ? <SettingsRow label={t('next.settingsSpec.server.metadata.groups.3.rows.0.label')} help={t('next.settingsSpec.server.metadata.groups.3.rows.0.help')}>
        <Select aria-label={t('next.settingsSpec.server.metadata.groups.3.rows.0.label')} value={draft.artworkPreference ?? ''} disabled={busy} options={[{value: 'folders', label: t('next.settingsSpec.server.metadata.groups.3.rows.0.control.options.0.label')}, {value: 'online', label: t('next.settingsSpec.server.metadata.groups.3.rows.0.control.options.1.label')}]} onValueChange={value => {if (value === 'folders' || value === 'online') set('artworkPreference', value);}}/>
      </SettingsRow> : null}
      {document.artworkLanguage ? <SettingsRow label={t('next.settingsSpec.server.metadata.groups.3.rows.1.label')}>
        <Select aria-label={t('next.settingsSpec.server.metadata.groups.3.rows.1.label')} value={draft.artworkLanguage ?? ''} disabled={busy} options={[{value: 'same', label: t('next.settingsSpec.server.metadata.groups.3.rows.1.control.options.0.label')}, {value: 'en', label: t('next.settingsSpec.server.metadata.groups.3.rows.1.control.options.1.label')}, {value: 'textless', label: t('next.settingsSpec.server.metadata.groups.3.rows.1.control.options.2.label')}]} onValueChange={value => {if (value === 'same' || value === 'en' || value === 'textless') set('artworkLanguage', value);}}/>
      </SettingsRow> : null}
    </SettingsGroup> : null}
    <SettingsGroup title={t('next.settingsSpec.server.metadata.groups.4.title')}>
      <SettingsRow label={t('next.settingsSpec.server.metadata.groups.4.rows.0.label')} help={t('metadata.settings.returnFromKeyHelp')}><a href="https://www.opensubtitles.com/en/consumers" target="_blank" rel="noopener noreferrer">{t('server.metadata.openSubtitlesGetKey')}</a></SettingsRow>
      {secret('subtitleKey', 'server.metadata.openSubtitlesKey', document.openSubtitlesKeyConfigured)}
      <SettingsRow label={t('server.metadata.openSubtitlesUser')} help={t('server.metadata.openSubtitlesAccountHelp')}><TextField aria-label={t('server.metadata.openSubtitlesUser')} placeholder={t('next.settingsSpec.server.metadata.groups.4.rows.2.control.placeholder')} autoComplete="off" spellCheck={false} maxLength={512} value={draft.subtitleUser} disabled={busy} onChange={event => {if (!flight.current && current()) {const subtitleUser = event.currentTarget.value; setDraft({...draft, subtitleUser, subtitlePassword: ''}); setError(undefined);}}}/></SettingsRow>
      {!!draft.subtitleUser.trim() && <SettingsRow label={t('server.metadata.openSubtitlesPassword')} help={document.openSubtitlesPasswordConfigured ? t('server.metadata.openSubtitlesPasswordSaved') : t('next.settingsSpec.server.metadata.groups.4.rows.3.help')}>
        <TextField aria-label={t('server.metadata.openSubtitlesPassword')} placeholder={t('next.settingsSpec.server.metadata.groups.4.rows.3.control.placeholder')} type="password" autoComplete="new-password" maxLength={512} value={draft.subtitlePassword} disabled={busy} onChange={event => set('subtitlePassword', event.currentTarget.value)}/>
      </SettingsRow>}
      <SettingsRow label={t('server.metadata.openSubtitlesConnection')} help={Object.keys(subtitleChanges).length ? t('server.metadata.openSubtitlesTestUnsaved') : summary.downloads}>
        <div><p>{summary.text}</p><Button size="small" disabled={busy || !!Object.keys(subtitleChanges).length || document.openSubtitles.keySource === 'none'} onClick={() => {void check();}}>{t(busy ? 'server.metadata.openSubtitlesTesting' : 'server.metadata.openSubtitlesTest')}</Button></div>
      </SettingsRow>
    </SettingsGroup>
    {document.advanced && draft.advanced ? <SettingsAdvanced open={advancedOpen} onOpenChange={setAdvancedOpen} summary={advancedSummary(document.advanced)}>
      <SettingsGroup title={t('next.settingsSpec.server.metadata.advanced.groups.0.title')} advanced>
        {specSelect('metadata.refresh', 'refreshPolicy')}
        <SettingsRow label={t('next.settingsSpec.server.metadata.advanced.groups.0.rows.1.label')} help={document.advanced.refreshAll.running ? t('server.metadata.refreshAllRunning') : t('next.settingsSpec.server.metadata.advanced.groups.0.rows.1.help')}>
          <Button ref={refreshTrigger} size="small" variant="glass" disabled={busy || dirty} onClick={() => {if (current()) setConfirmRefresh(true);}}>{t('next.settingsSpec.server.metadata.advanced.groups.0.rows.1.control.label')}</Button>
        </SettingsRow>
      </SettingsGroup>
      <SettingsGroup title={t('next.settingsSpec.server.metadata.advanced.groups.1.title')} advanced>
        {specSelect('metadata.fallbackLanguage', 'fallbackLanguage')}
        {specSelect('metadata.animeTitles', 'animeTitles')}
      </SettingsGroup>
      <SettingsGroup title={t('next.settingsSpec.server.metadata.advanced.groups.2.title')} advanced>
        <SettingsRow label={t('next.settingsSpec.server.metadata.advanced.groups.2.rows.2.label')} help={t('next.settingsSpec.server.metadata.advanced.groups.2.rows.2.help')}>
          <Switch aria-label={t('next.settingsSpec.server.metadata.advanced.groups.2.rows.2.label')} checked={draft.advanced.castPhotos} disabled={busy} onCheckedChange={value => set('advanced', {...draft.advanced!, castPhotos: value})}/>
        </SettingsRow>
      </SettingsGroup>
      <SettingsGroup title={t('next.settingsSpec.server.metadata.advanced.groups.3.title')} advanced>
        {secret('acoustid', 'next.settingsSpec.server.metadata.advanced.groups.3.rows.0.label', document.advanced.acoustidKeyConfigured)}
      </SettingsGroup>
    </SettingsAdvanced> : null}
    <Dialog open={confirmRefresh} size="sm" role="alertdialog" returnFocusRef={refreshTrigger} onOpenChange={open => {if (!open) setConfirmRefresh(false);}} title={t('next.settingsSpec.server.metadata.advanced.groups.0.rows.1.label')}
      description={t('next.settingsSpec.server.metadata.advanced.groups.0.rows.1.control.confirm')}
      footer={<><Button size="small" variant="text" disabled={busy} onClick={() => setConfirmRefresh(false)}>{t('action.cancel')}</Button><Button size="small" variant="primary" disabled={busy} onClick={() => {void refreshAll();}}>{t('next.settingsSpec.server.metadata.advanced.groups.0.rows.1.control.label')}</Button></>}>{null}</Dialog>
    {dirty ? <div className={styles.saveBar}><span className={styles.pending}>{t('web.metadata.pendingCount', {count: pendingCount})}</span><Button size="small" variant="text" disabled={busy} onClick={() => {if (current()) {setDraft(draftFor(document)); setError(undefined);}}}>{t('metadata.settings.discard')}</Button>
      <Button size="small" variant="primary" disabled={busy || bytes === undefined} onClick={() => {void save();}}>{t('web.metadata.saveChanges')}</Button></div> : null}
    {error ? <Button size="small" variant="text" disabled={busy} onClick={() => {if (current()) setReload(value => value + 1);}}>{t('action.refresh')}</Button> : null}
  </div>;
}
