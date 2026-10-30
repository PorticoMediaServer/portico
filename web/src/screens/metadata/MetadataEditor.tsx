import {UnlockIcon} from '../../kit/UnlockIcon';
import type {ReactNode} from 'react';
import {useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {orderTakesOffset, readTmdbOrder, tmdbAbsoluteOrder} from '@core/screen-metadata.ts';
import {attentionInProgress} from '@core/server-admin/attention-words.ts';
import {MetadataRepairService, type RepairCaller, type RepairCommand, type RepairData, type RepairFieldSpec, type RepairTarget} from '@core/metadata-repair.ts';
import {EditorHeader} from '../../kit/shell';
import {Icon} from '../../kit/Icon';
import {Button, TextField, TabsParts} from '../../kit/controls';
import {Dialog} from '../../kit/overlays/Dialog';
import {providerName} from '@core/server-admin/attention-words.ts';
import {HostedError} from '../../kit/accounts/ServicesAuth';
import styles from './metadata.module.css';
import {Artwork as Picture} from '../../kit/artwork';
import type {ShellArtwork} from '../../shell/artwork';
import {SongLyricsEditor} from './SongLyricsEditor';
import {metadataDraftValue, patchMetadataDraft, type MetadataFieldDraft} from './draft-value';
import {compactMetadataField, metadataTabCount, type MetadataTab as Tab} from './editor-presentation';

const t = defaultI18n.t;
type Api = Readonly<{request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>; requestMetadataUpload?: <T>(path: string, body: FormData, signal?: AbortSignal) => Promise<T>}>;
type Draft = Readonly<Record<string, MetadataFieldDraft>>;

/** Only the tabs this type really has: no list fields means no Tags tab, no roles means no Artwork. */
function tabsFor(data: RepairData | null | undefined): readonly Tab[] {
  const tabs: Tab[] = ['general'];
  if (!data || data.artworkRoles.length) tabs.push('artwork');
  if (!data || data.schema.some(spec => spec.type === 'list')) tabs.push('tags');
  // Only a type the server actually matches online has a Matching tab (frame 10); a local-only
  // book has none because there is nothing to match against (frame 13).
  // Frame 9: credits exist only where the type carries them; an empty list is not a tab.
  if (data && (data.snapshot.relationships.some(row => row.kind === 'credit') || Object.hasOwn(data.snapshot.relationshipLocks, 'credit'))) tabs.push('people');
  if (data && (data.snapshot.identity || data.candidates.length)) tabs.push('matching');
  if (data?.schema.some(field => field.field === 'trackNumber')) tabs.push('lyrics');
  tabs.push('history');
  return tabs;
}

/** owner-metadata frames 6-8: one editor for a title, an episode, a show, an album or a book.
 * Every field and every choice comes from the server's schema and current snapshot; a change is
 * exactly one edit command carrying only the fields the owner touched. */
export function MetadataEditor({api, scope, target, onClose, initialTab = 'general', localArtworkOnly = false, renderCoverPages, artwork}: {
  api: Api; scope: Readonly<{serverId: string; viewerId: string}>; target: RepairTarget; onClose: () => void;
  artwork?: ShellArtwork; localArtworkOnly?: boolean; renderCoverPages?: (upload: (file: Blob) => Promise<boolean>, busy: boolean) => ReactNode; initialTab?: 'general' | 'matching';
}) {
  const service = useMemo(() => new MetadataRepairService({api, scope}), [api, scope]);
  const state = useSyncExternalStore(service.subscribe, service.getSnapshot);
  const tabStrip = useRef<HTMLElement>(null);
  const [tab, setTab] = useState<Tab>(initialTab), [draft, setDraft] = useState<Draft>({}), [error, setError] = useState<unknown>();
  useLayoutEffect(() => {void service.load(target); return () => service.cancel();}, [service, target]);
  const data = state.data;
  const offered = tabsFor(data);
  const shown: Tab = offered.includes(tab) ? tab : 'general';
  const opening = useMemo(() => ({service, target, shown}), [service, target, shown]);
  const renderedOpening = useRef(opening); renderedOpening.current = opening;
  const [openingLease, setOpeningLease] = useState<{opening: typeof opening; live: boolean; controller: AbortController} | null>(null);
  useLayoutEffect(() => {
    const lease = {opening, live: true, controller: new AbortController()}; setOpeningLease(lease);
    return () => {lease.live = false; lease.controller.abort();};
  }, [opening]);
  const retireOpening = () => {if (openingLease?.opening === opening) {openingLease.live = false; openingLease.controller.abort();}};
  const openingCurrent = () => openingLease !== null && openingLease.live && openingLease.opening === opening && renderedOpening.current === opening;
  const close = () => {if (!openingCurrent()) return; retireOpening(); onClose();};
  useEffect(() => {tabStrip.current?.querySelector<HTMLElement>('[aria-current="page"]')?.scrollIntoView({block: 'nearest', inline: 'nearest'});}, [shown]);
  const invalid = data ? Object.entries(draft).some(([name, value]) => {const spec = data.schema.find(row => row.field === name); return spec ? Boolean(fieldError(spec, value.value, value.values, target.kind !== 'season')) : false;}) : false;
  const changed = useMemo(() => {
    if (!data) return [];
    return Object.entries(draft).filter(([field, value]) => {
      const original = data.snapshot.fields[field];
      const list = data.schema.find(spec => spec.field === field)?.type === 'list';
      return value.locked !== (original?.locked ?? false) || (list ? JSON.stringify(value.values ?? original?.values ?? []) !== JSON.stringify(original?.values ?? []) : value.value !== metadataDraftValue(data, field));
    }).map(([field, value]) => ({field, ...value}));
  }, [data, draft]);
  useEffect(() => {
    if (shown !== 'matching' || state.phase !== 'ready' || !data || changed.length || !attentionInProgress(data.snapshot.identity?.status ?? '')) return;
    const timer = setTimeout(() => {void service.refresh(data);}, 2000);
    return () => clearTimeout(timer);
  }, [service, shown, state.phase, data, changed.length]);
  const edit = async (fields: NonNullable<RepairCommand['fields']>) => {
    if (!data || service.getSnapshot().data !== data || service.getSnapshot().phase !== 'ready') return;
    setError(undefined);
    await service.command({action: 'edit', fields}, data);
  };
  const run = async (command: RepairCommand, caller?: RepairCaller) => {
    if (caller && (caller.signal.aborted || !caller.current())) return false;
    if (!data || service.getSnapshot().data !== data || service.getSnapshot().phase !== 'ready') return false;
    setError(undefined);
    const saved = await service.command(command, data, caller);
    if (caller && (caller.signal.aborted || !caller.current())) return false;
    if (!saved) setError(service.getSnapshot().error ?? new Error(t('web.metadata.saveChanges')));
    return saved;
  };
  const makeThumbnail = useCallback(async (expectedData: RepairData) => {
    try {await service.command({action: 'make_episode_thumbnail'}, expectedData);} catch (value) {setError(value);}
  }, [service]);
  const currentRepairGrant = useCallback((expected: RepairData) => service.getSnapshot().phase === 'ready' && service.getSnapshot().data === expected, [service]);
  const currentRepairData = useCallback((expected: RepairData) => service.getSnapshot().data === expected, [service]);
  const removeUpload = useCallback(async (role: string, candidateId: string, expectedData: RepairData) => {
    try {return await service.removeUploadedArtwork(role, candidateId, expectedData);} catch (value) {setError(value); return false;}
  }, [service]);
  const set = (field: string, patch: Partial<MetadataFieldDraft>) => {
    setDraft(current => ({...current, [field]: patchMetadataDraft(data, field, current[field], patch)}));
  };
  const save = () => {
    if (!changed.length) return;
    const fields: NonNullable<RepairCommand['fields']> = {};
    for (const item of changed) {
      const spec = data?.schema.find(row => row.field === item.field);
      fields[item.field] = {...(spec?.type === 'list' ? {values: item.values ?? data?.snapshot.fields[item.field]?.values ?? []} : {value: item.value}), locked: item.locked};
    }
    void edit(fields);
  };
  const heading = data ? (data.snapshot.fields.title?.value || target.id) : '';
  const kind = !data ? 'item' : localArtworkOnly ? 'reading' : target.kind === 'item' ? data.schema.some(field => field.field === 'episodeNumber') ? 'episode' : data.schema.some(field => field.field === 'trackNumber') ? 'song' : data.schema.some(field => field.field === 'partNumber') ? 'audiobook_file' : 'film' : target.kind;
  return <TabsParts.Root asChild value={shown} onValueChange={value => {if (!openingCurrent() || !offered.includes(value as Tab)) return; if (value !== shown) retireOpening(); setTab(value as Tab);}}><aside className={styles.editor} aria-label={t('web.metadata.sections')}>
    <EditorHeader audience={{authority: 'owner'}} title={heading ? t('web.metadata.editTitle', {title: heading}) : t('web.metadata.editFallbackTitle')} description={t('web.metadata.editorLede', {kind})} onClose={close} className={styles.editorHead}/>
    <TabsParts.List asChild><nav ref={tabStrip} className={styles.editorTabs} aria-label={t('web.metadata.sections')}>
      {tabsFor(data).map(id => <TabsParts.Trigger key={id} value={id} asChild><button type="button" aria-current={shown === id ? 'page' : undefined}>
        {t(id === 'general' ? 'web.metadata.tabGeneral' : id === 'artwork' ? 'web.metadata.tabArtwork' : id === 'tags' ? data && !data.schema.some(field => field.field === 'genres' || field.field === 'genre') ? 'web.metadata.field.tags' : 'web.metadata.tabTags' : id === 'people' ? 'web.metadata.tabPeople' : id === 'matching' ? 'web.metadata.tabMatching' : id === 'lyrics' ? 'web.metadata.tabLyrics' : 'web.metadata.tabHistory')}
        {metadataTabCount(id, data, draft) !== undefined && <small>{metadataTabCount(id, data, draft)}</small>}</button></TabsParts.Trigger>)}
    </nav></TabsParts.List>
    <TabsParts.Content value={shown} className={styles.editorBody}>
    {state.phase === 'loading' ? <p className={styles.working}>{t('web.metadata.loading')}</p> : null}
    {state.error ? <HostedError error={state.error} /> : null}
    {state.phase === 'uncertain' || state.phase === 'conflict' || state.phase === 'error' ? <Button size="small" onClick={() => {setError(undefined); void service.load(target);}}>{t('next.states.tryAgain')}</Button> : null}
    {error ? <HostedError error={error} /> : null}
    {state.phase === 'saving' ? <p className={styles.working}>{t('web.metadata.applying')}</p> : null}
    {data && state.phase !== 'loading' ? <>
      {shown === 'general' ? <General data={data} draft={draft} set={set} requiredTitle={target.kind !== 'season'} /> : null}
      {shown === 'tags' ? <Tags data={data} draft={draft} set={set} busy={state.phase !== 'ready'} /> : null}
      {shown === 'people' ? <People data={data} target={target} busy={state.phase !== 'ready'} opening={openingLease} openingSignal={openingLease?.controller.signal} openingCurrent={openingCurrent}
        onCommit={(rows, caller) => run({action: 'edit_relationships', role: 'credit', confirm: true, relationships: rows, ...(data.snapshot.canEditProviderCredits === true ? {effectiveCredits: true as const} : {})}, caller)}
        onLock={(locked, caller) => run({action: 'relationship_lock', role: 'credit', locked, confirm: true}, caller)} /> : null}
      {shown === 'matching' ? <Matching data={data} target={target} busy={state.phase !== 'ready'} onSearch={query => void run({action: 'search', query})}
        onRetry={() => void run({action: 'retry'})} onLock={locked => void run({action: 'relationship_lock', role: 'identity', locked, confirm: true})}
        onUse={(provider, candidateId, order) => void run({action: 'identify', provider, candidateId, confirm: true, ...(order ? {order} : {})})} /> : null}
      {shown === 'artwork' && data.artworkRoles.includes('cover') && api.requestMetadataUpload ? renderCoverPages?.(file => service.uploadArtwork('cover', file), state.phase !== 'ready') : null}
      {shown === 'artwork' ? <Artwork artwork={artwork} portraitCover={localArtworkOnly} data={data} busy={state.phase !== 'ready'} onUse={(role, candidateId) => void run({action: 'select_artwork', role, candidateId, confirm: true})}
        onRemove={removeUpload} isDataCurrent={currentRepairData} onMakeFromVideo={data.canMakeEpisodeThumbnail ? makeThumbnail : undefined} localOnly={target.kind === 'book'}
        onUpload={api.requestMetadataUpload ? (role, file) => service.uploadArtwork(role, file) : undefined}
        onLock={(role, subject, locked) => void run({action: 'artwork_lock', role, subject, locked})}
        onFindMore={localArtworkOnly || target.kind === 'book' || data.onlineLookups === false ? undefined : role => void run({action: 'discover_artwork', role})} onRefetch={localArtworkOnly || target.kind === 'book' ? undefined : role => void run({action: 'repair_assets', role})} /> : null}
      {shown === 'lyrics' ? <SongLyricsEditor api={api} scope={scope} target={target} repairData={data} isCurrent={currentRepairGrant}/> : null}
      {shown === 'history' ? <History data={data} busy={state.phase !== 'ready'} onUndo={historyId => void run({action: 'undo', historyId, confirm: true})}
        onAll={locked => void run({action: locked ? 'lock_all' : 'unlock_all', confirm: true})} /> : null}
      {shown === 'general' || shown === 'tags' ? <footer className={styles.editorFooter}>
        <span className={styles.pending} data-changed={!!changed.length || undefined}>{t(changed.length ? 'web.metadata.pendingCount' : 'web.metadata.noChanges', {count: changed.length})}</span>
        <div className={styles.editorActions}><Button onClick={onClose}>{t('action.cancel')}</Button>
          <Button variant="primary" loading={state.phase === 'saving'} disabled={!changed.length || invalid || state.phase !== 'ready'} onClick={save}>
            {t('web.metadata.saveChanges')}</Button></div>
      </footer> : null}
    </> : null}
    </TabsParts.Content>
  </aside></TabsParts.Root>;
}


/** The server's own bounds, checked beside the field as the frame draws them. */
function fieldError(spec: RepairFieldSpec, value: string, values: readonly string[] = [], requiredTitle = false): string {
  const label = t(spec.label as Parameters<typeof t>[0]);
  if (requiredTitle && spec.field === 'title' && value.trim() === '') return t('web.metadata.titleRequired');
  if (spec.type === 'list') {
    if (spec.max !== undefined && values.length > spec.max) return t('web.metadata.tooManyEntries', {label, max: spec.max});
    if (spec.maxLength !== undefined && values.some(entry => Array.from(entry).length > spec.maxLength!)) return t('web.metadata.listEntryTooLong', {label, max: spec.maxLength});
    return '';
  }
  if (spec.type === 'integer' || spec.type === 'number') {
    if (value.trim() === '') return '';
    const number = Number(value);
    if (!Number.isSafeInteger(number) || number < 0 || String(number) !== value) return t('web.metadata.wholeNumber', {label});
    if (spec.min !== undefined || spec.max !== undefined) {
      if ((spec.min !== undefined && number < spec.min) || (spec.max !== undefined && number > spec.max)) return t('web.metadata.numberRange', {label, min: spec.min ?? '', max: spec.max ?? ''});
    }
    return '';
  }
  if (spec.type === 'date' && value) {
    const parsed = new Date(value + 'T00:00:00Z');
    if (!/^\d{4}-\d{2}-\d{2}$/.test(value) || Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== value) return t('web.metadata.dateError', {label});
  }
  if (spec.type === 'enum' && value && spec.allowed && !spec.allowed.includes(value)) return t('web.metadata.enumError', {label});
  if (spec.maxLength !== undefined && Array.from(value).length > spec.maxLength) return t('web.metadata.textTooLong', {label, max: spec.maxLength});
  return '';
}

function enumChoiceLabel(spec: RepairFieldSpec, value: string): string {
  if (spec.field !== 'publicationType') return value;
  switch (value) {
    case '': return t('web.metadata.publicationType.automatic');
    case 'book': return t('web.metadata.publicationType.book');
    case 'magazine': return t('web.metadata.publicationType.magazine');
    case 'newspaper': return t('web.metadata.publicationType.newspaper');
    default: return value;
  }
}

function input(spec: RepairFieldSpec, value: string, set: (value: string) => void, accessibility: Readonly<{'aria-labelledby': string; 'aria-describedby': string; 'aria-invalid': boolean}>) {
  if (spec.type === 'enum' && spec.allowed) return <select {...accessibility} value={value} onChange={event => set(event.currentTarget.value)}>
    {spec.allowed.map(choice => <option key={choice} value={choice}>{enumChoiceLabel(spec, choice)}</option>)}</select>;
  if (spec.type === 'multiline') return <textarea {...accessibility} rows={4} value={value} onChange={event => set(event.currentTarget.value)} />;
  return <input {...accessibility} type={spec.type === 'date' ? 'date' : 'text'} inputMode={spec.type === 'integer' || spec.type === 'number' ? 'numeric' : undefined} value={value} onChange={event => set(event.currentTarget.value)} />;
}

function Field({spec, field, draft, set, requiredTitle = false}: {requiredTitle?: boolean; spec: RepairFieldSpec; field?: {value: string; automaticValue: string; locked: boolean; source: string; values?: readonly string[]}; draft?: {value: string; locked: boolean}; set: (patch: Partial<MetadataFieldDraft>) => void}) {
  const id = useId();
  const value = draft?.value ?? field?.value ?? '', locked = draft?.locked ?? field?.locked ?? false, error = fieldError(spec, value, [], requiredTitle);
  const changed = draft !== undefined && (draft.value !== (field?.value ?? '') || draft.locked !== (field?.locked ?? false));
  const numeric = spec.type === 'integer' || spec.type === 'number';
  const hint = numeric ? spec.min !== undefined && spec.max !== undefined ? t('web.metadata.numericBounds', {min: spec.min, max: spec.max}) : spec.min !== undefined ? t('web.metadata.numericMinimum', {min: spec.min}) : spec.max !== undefined ? t('web.metadata.numericMaximum', {max: spec.max}) : '' : spec.maxLength !== undefined ? t('web.metadata.characterCount', {count: Array.from(value).length, max: spec.maxLength}) : '';
  const source = field?.source ? ['tmdb', 'tvdb', 'anilist', 'musicbrainz'].includes(field.source) ? providerName(field.source) : field.source : '';
  return <label className={styles.field} data-wide={spec.type === 'multiline' || spec.field === 'tagline' || undefined} data-short={compactMetadataField(spec) || undefined} data-changed={changed || undefined}>
    <span id={id + '-label'} className={styles.fieldLabel}>{t(spec.label as Parameters<typeof t>[0])}
      {changed ? <em>{t('web.metadata.editorChanged')}</em> : null}
    </span>
    <span className={styles.fieldControl} data-invalid={!!error || undefined}>{input(spec, value, next => set({value: next}), {'aria-labelledby': id + '-label', 'aria-describedby': id + '-hint' + (error ? ' ' + id + '-error' : ''), 'aria-invalid': Boolean(error)})}
      <button type="button" className={styles.lock} aria-pressed={locked} title={t(locked ? 'web.metadata.lockedTip' : 'web.metadata.unlockedTip')} aria-label={t(locked ? 'web.metadata.unlockField' : 'web.metadata.lockField', {label: t(spec.label as Parameters<typeof t>[0])})} onClick={() => set({locked: !locked})}>{locked ? <Icon name="i-lock" size={16}/> : <UnlockIcon size={16}/>}</button>
    </span>
    {error ? <small id={id + '-error'} className={styles.fieldError} role="alert"><Icon name="i-warn" size={16}/>{error}</small> : null}
    <small id={id + '-hint'}>{field?.source === 'manual' ? t('web.metadata.editedHere') : source ? t('web.metadata.fromSource', {source}) : ''}{hint ? <span className={styles.fieldBounds}>{hint}</span> : null}</small>
    {field && field.automaticValue !== value ? <small>{field.source !== 'manual' && source ? t('web.metadata.automaticValueFrom', {source, value: field.automaticValue}) : t('web.metadata.automaticValue', {value: field.automaticValue})}<button type="button" onClick={() => set({value: field.automaticValue})}>{t('web.metadata.useIt')}</button></small> : null}
  </label>;
}

function General({data, draft, set, requiredTitle}: {data: RepairData; draft: Draft; requiredTitle: boolean; set: (field: string, patch: Partial<MetadataFieldDraft>) => void}) {
  return <div className={styles.generalFields}><p className={styles.lockHelp}>{t('web.metadata.editorLockHelp')}</p>{data.schema.filter(spec => spec.type !== 'list' && spec.group !== 'identity').map(spec =>
    <Field requiredTitle={requiredTitle} key={spec.field} spec={spec} field={data.snapshot.fields[spec.field]} draft={draft[spec.field]} set={patch => set(spec.field, patch)} />)}</div>;
}

function Tags({data, draft, set, busy}: {data: RepairData; draft: Draft; busy: boolean; set: (field: string, patch: Partial<MetadataFieldDraft>) => void}) {
  return <div className={styles.fields}>{data.schema.filter(spec => spec.type === 'list').map(spec => {
    const original = data.snapshot.fields[spec.field], current = draft[spec.field];
    return <TagList key={spec.field} spec={spec} busy={busy} values={current?.values ?? original?.values ?? []} locked={current?.locked ?? original?.locked ?? false}
      onValues={values => set(spec.field, {values})} onLock={locked => set(spec.field, {locked})}/>;
  })}</div>;
}

function TagList({spec, values, locked, onValues, onLock, busy}: {spec: RepairFieldSpec; values: readonly string[]; locked: boolean; busy: boolean;
  onValues: (values: readonly string[]) => void; onLock: (locked: boolean) => void;
}) {
  const [text, setText] = useState(''), [error, setError] = useState(''), errorId = useId();
  const label = t(spec.label as Parameters<typeof t>[0]), genre = spec.field === 'genres' || spec.field === 'genre';
  const placeholder = genre ? t('web.metadata.addGenreHelp') : spec.field === 'tags' ? t('web.metadata.addTagChip') : spec.field === 'labels' ? t(values.length ? 'web.metadata.addLabelChip' : 'web.metadata.emptyLabelsChip') : t('web.metadata.addListChip', {label});
  const add = () => {
    if (busy) return;
    const value = text.trim();
    if (!value || values.includes(value)) {setText(''); return;}
    const next = [...values, value], problem = fieldError(spec, '', next);
    if (problem) {setError(problem); return;}
    onValues(next); setText(''); setError('');
  };
  return <section className={styles.tagGroup} aria-label={label}>
    <header><h3>{label}</h3>{genre ? <small>{t(locked ? 'web.metadata.genresLocked' : 'web.metadata.genresUnlocked')}</small> : spec.field === 'tags' ? <small>{t('web.metadata.tagsFiling')}</small> : null}
      <button type="button" className={styles.lock} disabled={busy} aria-pressed={locked} onClick={() => onLock(!locked)}>{t(locked ? 'web.metadata.locked' : 'web.metadata.unlocked')}</button></header>
    <div className={styles.tagChips} data-invalid={!!error || undefined}>{values.map((value, index) => <span className={styles.tagChip} key={index}>
      {value}<button type="button" disabled={busy} aria-label={t('web.metadata.removeLabel', {label: value})} onClick={() => onValues(values.filter((_, at) => at !== index))}><Icon name="i-x" size={12}/></button>
    </span>)}<input disabled={busy} value={text} aria-label={placeholder} aria-invalid={!!error} aria-describedby={error ? errorId : undefined} placeholder={placeholder} onChange={event => {setText(event.currentTarget.value); setError('');}} onKeyDown={event => {if (event.key === 'Enter' && !event.nativeEvent.isComposing) {event.preventDefault(); add();}}} onBlur={add}/></div>
    {error ? <small id={errorId} className={styles.fieldError} role="alert"><Icon name="i-warn" size={16}/>{error}</small> : null}
  </section>;
}

function Artwork({data, busy, portraitCover, onUse, onLock, onFindMore, onRefetch, onUpload, onRemove, isDataCurrent, onMakeFromVideo, localOnly, artwork}: {
  artwork?: ShellArtwork; localOnly: boolean;
  isDataCurrent: (data: RepairData) => boolean;
  onMakeFromVideo?: (expectedData: RepairData) => Promise<void>;
  onRemove: (role: string, candidateId: string, expectedData: RepairData) => Promise<boolean>;
  onUpload?: (role: string, file: File) => Promise<boolean>;
  data: RepairData; busy: boolean; portraitCover?: boolean; onUse: (role: string, candidateId: string) => void;
  onLock: (role: string, subject: string, locked: boolean) => void; onFindMore?: (role: string) => void; onRefetch?: (role: string) => void;
}) {
  const rolesId = useId();
  const [selected, setSelected] = useState(''), [uploadError, setUploadError] = useState('');
  type Removal = {data: RepairData; candidate: RepairData['artwork']['candidates'][number]; handler: typeof onRemove; live: boolean; started: boolean};
  const removal = useRef<Removal | null>(null);
  const [removing, setRemoving] = useState<Removal>(), [removeFailed, setRemoveFailed] = useState(false);
  // A replaced document/handler retires the confirmation before its next event can run.
  if (removal.current && (removal.current.data !== data || removal.current.handler !== onRemove || !isDataCurrent(removal.current.data))) {removal.current.live = false; removal.current = null;}
  const retireRemoval = (attempt: Removal | undefined) => {if (!attempt || removal.current !== attempt) return; attempt.live = false; removal.current = null; setRemoving(shown => shown === attempt ? undefined : shown);};
  useLayoutEffect(() => () => {if (removal.current) removal.current.live = false; removal.current = null;}, []);
  const currentRemoval = (attempt: Removal) => removal.current === attempt && attempt.live && attempt.data === data && attempt.handler === onRemove && isDataCurrent(attempt.data) && data.artwork.candidates.includes(attempt.candidate);
  const confirmRemoval = async (attempt: Removal | undefined) => {
    if (!attempt || attempt.started || busy || !currentRemoval(attempt)) return;
    attempt.started = true;
    const saved = await attempt.handler(attempt.candidate.role, attempt.candidate.id, attempt.data);
    if (!currentRemoval(attempt)) return;
    if (saved) retireRemoval(attempt); else setRemoveFailed(true);
  };
  const upload = async (file: File | undefined) => {
    if (!file || busy || !role || !onUpload) return;
    if (!['image/jpeg', 'image/png'].includes(file.type) || file.size < 1 || file.size > 10 * 1024 * 1024) {setUploadError(t('web.metadata.uploadImageLimit')); return;}
    setUploadError('');
    try {await onUpload(role, file);} catch {setUploadError(t('web.metadata.uploadImageLimit'));}
  };
  const role = data.artworkRoles.includes(selected) ? selected : data.artworkRoles[0];
  const candidates = data.artwork.candidates.filter(candidate => candidate.role === role);
  const chosen = data.snapshot.artwork.find(choice => choice.role === role);
  const uploadTile = onUpload ? <div className={styles.uploadTile} onDragOver={event => {if (!busy) event.preventDefault();}} onDrop={event => {event.preventDefault(); if (!busy) void upload(event.dataTransfer.files[0]);}}>
        <b>{t('web.metadata.uploadImage')}</b><p>{t('web.metadata.uploadImageHelp')}</p>
        <label>{t('web.metadata.chooseImageFile')}<input type="file" accept="image/jpeg,image/png" disabled={busy} onChange={event => {const file = event.currentTarget.files?.[0]; event.currentTarget.value = ''; void upload(file);}}/></label>
        {uploadError ? <p role="alert">{uploadError}</p> : null}
      </div> : null;
  return <div className={styles.artwork}>
    {localOnly ? <p className={styles.lockHelp}>{t('web.metadata.localArtworkHelp')}</p> : null}
    <div className={styles.artRoles} role="tablist" aria-label={t('web.metadata.tabArtwork')}>{data.artworkRoles.map((value, index) =>
      <button key={value} id={rolesId + '-' + value} type="button" role="tab" tabIndex={role === value ? 0 : -1} aria-selected={role === value} aria-controls={rolesId + '-panel'} onClick={() => setSelected(value)}
        onKeyDown={event => {
          let next = index;
          if (event.key === 'ArrowRight') next = (index + 1) % data.artworkRoles.length;
          else if (event.key === 'ArrowLeft') next = (index + data.artworkRoles.length - 1) % data.artworkRoles.length;
          else if (event.key === 'Home') next = 0;
          else if (event.key === 'End') next = data.artworkRoles.length - 1;
          else return;
          event.preventDefault(); setSelected(data.artworkRoles[next]);
          event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
        }}>{t('web.metadata.artRole', {role: value})}</button>)}</div>
    {role ? <section className={styles.role} id={rolesId + '-panel'} role="tabpanel" aria-labelledby={rolesId + '-' + role}>
      <header><b>{t('web.metadata.candidateCount', {count: candidates.length})}</b>
        {chosen ? <Button size="small" disabled={busy} aria-pressed={chosen.locked} onClick={() => onLock(role, chosen.subject, !chosen.locked)}>{t(chosen.locked ? 'web.metadata.unlock' : 'web.metadata.lock')}</Button> : null}
        {onFindMore && <Button size="small" onClick={() => onFindMore(role)} disabled={busy}>{t('web.metadata.findMore')}</Button>}
        {onRefetch && <Button size="small" onClick={() => onRefetch(role)} disabled={busy || !chosen}>{t('web.metadata.refetchChosen')}</Button>}
      </header>
      {data.artworkFallbackUrl && (role === 'still' || role === 'thumbnail') ? <figure className={styles.backdropFallback}><Picture source={artwork?.foreground(data.artworkFallbackUrl, 800, 16 / 9)} alt=""/><figcaption>{t('web.metadata.showBackdropFallback')}</figcaption></figure> : null}
      {!candidates.length ? <p className={styles.working}>{t('web.metadata.noCandidates')}</p> : null}
      <div className={styles.choices} data-shape={portraitCover && role === 'cover' ? 'poster' : role === 'cover' || role === 'square' ? 'square' : role === 'backdrop' || role === 'still' || role === 'thumbnail' || role === 'logo' ? 'landscape' : role === 'banner' ? 'banner' : 'poster'}>{candidates.map(candidate => <figure key={candidate.id} data-current={candidate.current || undefined}>
        {candidate.previewUrl ? <img src={candidate.previewUrl} alt={t('web.metadata.artAlt', {role: t('web.metadata.artRole', {role}), provider: providerName(candidate.provider)})} loading="lazy" /> : <div className={styles.placeholder} />}
        <figcaption><small>{providerName(candidate.provider)}{candidate.locale ? ` · ${candidate.locale}` : ''}{candidate.votes != null ? ` · ${t('web.metadata.votes', {count: candidate.votes})}` : ''}</small></figcaption>
        {candidate.current ? <span className={styles.badge}>{t('web.metadata.inUse')}</span>
          : <Button size="small" disabled={busy} onClick={() => onUse(role, candidate.id)}>{t('web.liveSource.useThis')}</Button>}
        {candidate.provider === 'upload' ? <Button size="small" disabled={busy} onClick={() => {if (removal.current) retireRemoval(removal.current); const attempt: Removal = {data, candidate, handler: onRemove, live: true, started: false}; removal.current = attempt; setRemoveFailed(false); setRemoving(attempt);}}>{t('web.metadata.removeUpload')}</Button> : null}
      </figure>)}{uploadTile}</div>
      {onMakeFromVideo && (role === 'still' || role === 'thumbnail') ? <div className={styles.uploadTile}><b>{t('web.metadata.makeFromVideo')}</b><p>{t('web.metadata.makeFromVideoHelp')}</p><Button size="small" disabled={busy} onClick={() => {if (isDataCurrent(data)) void onMakeFromVideo(data);}}>{t('web.metadata.makeFromVideo')}</Button></div> : null}

      {data.artwork.jobs.filter(job => job.role === role).map(job => <p key={job.id} role="status">{t('web.metadata.artJob', {role: t('web.metadata.artRole', {role}), state: job.status})}</p>)}
    </section> : null}
    <Dialog open={Boolean(removing && currentRemoval(removing))} title={t('web.metadata.removeUploadTitle')} description={t('web.metadata.removeUploadBody')} onOpenChange={open => {if (!open) retireRemoval(removing);}}
      dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy}
      footer={<><Button size="small" disabled={busy} onClick={() => retireRemoval(removing)}>{t('action.cancel')}</Button><Button size="small" disabled={busy} onClick={() => void confirmRemoval(removing)}>{t('web.metadata.removeUpload')}</Button></>}>{removeFailed ? <p role="alert">{t('web.metadata.removeUploadFailed')}</p> : null}</Dialog>
  </div>;
}


function History({data, busy, onUndo, onAll}: {data: RepairData; busy: boolean; onUndo: (historyId: number) => void; onAll: (locked: boolean) => void}) {
  return <div className={styles.fields}>
    <div className={styles.editorFooter}><Button size="small" disabled={busy} onClick={() => onAll(true)}>{t('web.metadata.lockEverything')}</Button>
      <Button size="small" disabled={busy} onClick={() => onAll(false)}>{t('web.metadata.unlockEverything')}</Button></div>
    <h3>{t('web.metadata.recentChanges')}</h3>
    {data.history.length ? data.history.map(row => <div key={row.id} className={styles.historyRow}>
      <div><b>{t('web.metadata.history', {trigger: row.trigger})}</b>{row.observedAt ? <small>{defaultI18n.dateTime(row.observedAt)}</small> : null}</div>
      <Button size="small" disabled={busy} onClick={() => onUndo(row.id)}>{t('web.metadata.undoToHere')}</Button>
    </div>) : <p className={styles.working}>{t('web.metadata.noHistory')}</p>}
  </div>;
}


/** Frame 10: current identity, its lock, and the server's own matching commands. */
function Matching({data, target, busy, onSearch, onUse, onRetry, onLock}: {
  data: RepairData; target: RepairTarget; busy: boolean; onSearch: (query: string) => void;
  onUse: (provider: string, candidateId: string, order?: string) => void; onRetry: () => void; onLock: (locked: boolean) => void;
}) {
  const [query, setQuery] = useState(''), [orders, setOrders] = useState<Readonly<Record<string, Readonly<{order: string; offset: string}>>>>({});
  const identity = data.snapshot.identity;
  const statusKey = `web.metadata.matchStatus.${identity?.status.replace(/_([a-z])/g, (_, char: string) => char.toUpperCase()) ?? 'unmatched'}`;
  const status = identity?.status === 'needs_selection' ? t('web.metadata.moreThanOneMatch') : defaultI18n.has(statusKey) ? t(statusKey) : t('web.metadata.matchStatus.unknown');
  const typeKey = `metadata.attention.kind.${identity?.type === 'movie' ? 'film' : identity?.type ?? ''}`;
  const type = defaultI18n.has(typeKey) ? t(typeKey) : '';
  const scannedMatch = target.kind === 'album';
  const lookupBusy = busy || data.onlineLookups === false;
  return <div className={styles.fields}>
    <section className={styles.candidate}>
      <div><b>{t('web.metadata.currentMatch')}</b>
        <small>{identity ? [providerName(identity.provider), type, identity.id, status].filter(Boolean).join(' · ') : t('web.metadata.matchStatus.unmatched')}</small></div>
      <button type="button" className={styles.lock} disabled={busy} aria-pressed={identity?.locked === true}
        onClick={() => onLock(identity?.locked !== true)}>{t(identity?.locked ? 'web.metadata.identityLocked' : 'web.metadata.identityUnlocked')}</button>
    </section>
    {data.onlineLookups === false ? <p className={styles.lockHelp}>{t('web.metadata.onlineLookupOff')}</p> : null}
    <form className={styles.matchSearch} onSubmit={event => {event.preventDefault(); if (!scannedMatch && query.trim() && !lookupBusy) onSearch(query.trim());}}>
      {scannedMatch ? <p>{t('web.metadata.searchProviderHelpAuto')}</p> : <>
        <TextField label={t('web.metadata.searchProvider')} description={t('web.metadata.searchProviderHelp')} disabled={lookupBusy} value={query} onChange={event => setQuery(event.currentTarget.value)} />
        <Button size="small" type="submit" disabled={lookupBusy || !query.trim()}>{t('web.search.title')}</Button>
      </>}
      <Button size="small" type="button" disabled={lookupBusy} onClick={onRetry}>{t('web.chooser.tryAgain')}</Button>
    </form>
    {data.candidates.map(candidate => {
      const key = `${candidate.provider}:${candidate.id}`;
      const candidateIdentity = candidate.identityId ?? (identity?.engine === 'screen' ? undefined : candidate.id);
      const currentIdentity = identity?.provider === candidate.provider && identity.id === candidateIdentity
        && (identity.engine !== 'screen' || candidate.identityType !== undefined && identity.type === candidate.identityType);
      const stored = readTmdbOrder(identity?.order ?? ''), offered = candidate.orders ?? [];
      const fallback = {order: currentIdentity && offered.some(item => item.id === stored.base) ? stored.base : offered[0]?.id ?? '', offset: String(currentIdentity ? stored.offset : 0)};
      const draftOrder = orders[key];
      const chosen = draftOrder && offered.some(item => item.id === draftOrder.order) ? draftOrder : fallback;
      const takesOffset = target.kind === 'show' && candidate.provider === 'tmdb' && orderTakesOffset(chosen.order);
      const validOffset = !takesOffset || /^-?\d+$/.test(chosen.offset) && Number.isSafeInteger(Number(chosen.offset)) && Math.abs(Number(chosen.offset)) <= 1000000;
      const order = takesOffset && validOffset ? tmdbAbsoluteOrder(chosen.order, Number(chosen.offset)) : chosen.order;
      const current = currentIdentity && validOffset && (!offered.length || order === identity?.order);
      const setOrder = (patch: Partial<{order: string; offset: string}>) => setOrders(previous => ({...previous, [key]: {...chosen, ...patch}}));
      return <div key={key} className={`${styles.candidate} ${styles.matchCandidate}`}>
        {candidate.previewUrl ? <span className={styles.candidatePoster}><img src={candidate.previewUrl} alt="" loading="lazy"/></span> : null}
        <div className={styles.matchDetails}><b>{candidate.title}{candidate.year ? ` (${candidate.year})` : ''}</b><small>{providerName(candidate.provider)}{candidate.subtitle ? ` · ${candidate.subtitle}` : ''}</small>
          {candidate.orders && candidate.orders.length > 1 ? <label className={styles.order}>{t('metadata.attention.episodeOrder')}
            <select value={chosen.order} disabled={busy} onChange={event => setOrder({order: event.currentTarget.value})}>{candidate.orders.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label> : null}
          {takesOffset ? <TextField label={t('web.metadata.numberOffset')} description={t('web.metadata.numberOffsetHelp')} type="number" inputMode="numeric" min={-1000000} max={1000000} step={1} value={chosen.offset} disabled={busy} onChange={event => setOrder({offset: event.currentTarget.value})} error={!validOffset ? t('web.metadata.numberRange', {label: t('web.metadata.numberOffset'), min: -1000000, max: 1000000}) : undefined}/> : null}</div>
        {current ? <span className={styles.matchCurrent}>{t('web.metadata.currentMatch')}</span> : <Button size="small" disabled={lookupBusy || !validOffset || order.length > 160} onClick={() => onUse(candidate.provider, candidate.id, offered.length ? order : undefined)}>{t('web.liveSource.useThis')}</Button>}
      </div>;
    })}
    <p className={styles.lockHelp}>{t('web.metadata.chooseMatchHelp')}</p>
  </div>;
}


/** Frame 9: provider presentation edits retain exact captured source references. */
function People({data, target, busy, opening, openingSignal, openingCurrent, onCommit, onLock}: {
  data: RepairData; target: RepairTarget; busy: boolean;
  opening: unknown; openingSignal?: AbortSignal; openingCurrent: () => boolean;
  onCommit: (rows: RepairData['snapshot']['relationships'], caller: RepairCaller) => Promise<boolean>; onLock: (locked: boolean, caller: RepairCaller) => Promise<boolean>;
}) {
  const credits = data.snapshot.relationships.filter(row => row.kind === 'credit');
  const [ownedDraft, setOwnedDraft] = useState<{owner: RepairData; rows: RepairData['snapshot']['relationships']} | null>(null);
  const draft = ownedDraft?.owner === data ? ownedDraft.rows : null;
  const privateDraft = useRef(ownedDraft);
  const draftGeneration = useRef(0); const renderedGeneration = draftGeneration.current;
  const [lease, setLease] = useState<{live: boolean; claimed: boolean; controller: AbortController} | null>(null);
  const renderedDocument = useRef({data, opening}); renderedDocument.current = {data, opening};
  useLayoutEffect(() => {
    const attempt = {live: true, claimed: false, controller: new AbortController()}; setLease(attempt);
    const retire = () => {attempt.live = false; attempt.controller.abort();};
    openingSignal?.addEventListener('abort', retire, {once: true});
    if (openingSignal?.aborted) retire();
    return () => {retire(); openingSignal?.removeEventListener('abort', retire);};
  }, [data, target, opening, openingSignal]);
  const currentDraft = () => !!lease && lease.live && !lease.controller.signal.aborted && openingCurrent() &&
    renderedDocument.current.data === data && renderedDocument.current.opening === opening && privateDraft.current === ownedDraft && draftGeneration.current === renderedGeneration;
  const setDraft = (next: RepairData['snapshot']['relationships'] | null) => {
    if (!currentDraft() || lease?.claimed) return;
    // Retire the rendered draft before notifying React; retained callbacks cannot claim it.
    const replacement = next === null ? null : {owner: data, rows: next};
    draftGeneration.current++;
    privateDraft.current = replacement;
    setOwnedDraft(replacement);
  };
  const claim = (): RepairCaller | null => {
    if (!currentDraft() || !lease || lease.claimed) return null;
    lease.claimed = true;
    return {signal: lease.controller.signal, current: currentDraft};
  };
  const rows = draft ?? credits;
  const editable = (row: RepairData['snapshot']['relationships'][number]) => data.snapshot.canEditProviderCredits === true || row.provider === 'manual' && (!row.recordId || !row.locked);
  const clean = (value: string) => !/[\u0000-\u001f\u007f]/.test(value);
  const valid = rows.length <= 512 && rows.every(row => row.label.trim() !== '' && row.label.length <= 300 &&
    (row.role?.length ?? 0) <= 500 && (row.department?.length ?? 0) <= 200 && clean(row.label) && clean(row.role ?? '') && clean(row.department ?? '')) && new TextEncoder().encode(JSON.stringify(rows)).length <= (1 << 20) - 1024;
  const change = (index: number, patch: Partial<Pick<RepairData['snapshot']['relationships'][number], 'label' | 'role' | 'department'>>) => {
    if (busy || !editable(rows[index])) return;
    setDraft(rows.map((row, at) => at === index ? {...row, ...patch} : row));
  };
  const remove = (index: number) => {
    if (busy || !editable(rows[index])) return;
    setDraft(rows.filter((_, at) => at !== index));
  };
  const add = () => {
    if (busy || target.kind !== 'item' || rows.length >= 512) return;
    setDraft([...rows, {kind: 'credit', provider: 'manual', targetKind: 'scoped_credit', targetId: '', label: '', role: '', department: '',
      ordinal: rows.reduce((top, row) => Math.max(top, row.ordinal), 0) + 1, source: 'manual', locked: true}]);
  };
  const save = async () => {
    if (!currentDraft() || busy || !draft || !valid) return;
    const caller = claim(); if (!caller) return;
    const saved = await onCommit(rows.map(row => editable(row) && !credits.includes(row) ? {...row, label: row.label.trim(), role: row.role?.trim(), department: row.department?.trim()} : row), caller);
    if (!caller.current() || caller.signal.aborted || !lease) return;
    lease.claimed = false;
    if (saved) setDraft(null);
  };
  return <div className={styles.fields}>
    <div className={styles.creditHeader}><h3>{t('web.metadata.tabPeople')}</h3><small>{t('web.metadata.creditCount', {count: rows.length})}</small>
      <button type="button" className={styles.lock} disabled={busy || draft !== null} aria-pressed={data.snapshot.relationshipLocks.credit === true}
        onClick={() => {if (busy || draft !== null) return; const caller = claim(); if (caller) void onLock(data.snapshot.relationshipLocks.credit !== true, caller).finally(() => {if (caller.current() && lease) lease.claimed = false;});}}>{t(data.snapshot.relationshipLocks.credit ? 'web.metadata.locked' : 'web.metadata.unlocked')}</button>
      {target.kind === 'item' ? <Button size="small" disabled={busy || rows.length >= 512} onClick={add}>{t('web.metadata.addPerson')}</Button> : null}
    </div>
    {rows.map((row, index) => <div key={`${row.recordId ?? 'new'}:${row.targetId}:${index}`} className={styles.creditRow} data-new={!row.recordId || undefined}>
      <TextField label={t('web.metadata.field.name')} value={row.label} disabled={busy || !editable(row)} maxLength={300} onChange={event => change(index, {label: event.currentTarget.value})}/>
      <TextField label={t('web.metadata.field.department')} value={row.department ?? ''} disabled={busy || !editable(row)} maxLength={200} onChange={event => change(index, {department: event.currentTarget.value})}/>
      <TextField label={t(row.department?.toLowerCase() === 'acting' ? 'web.metadata.creditCharacter' : 'web.metadata.creditRole')} value={row.role ?? ''} disabled={busy || !editable(row)} maxLength={500} onChange={event => change(index, {role: event.currentTarget.value})}/>
      <Button size="small" disabled={busy || !editable(row)} onClick={() => remove(index)}>{t('web.metadata.removeLabel', {label: row.label})}</Button>
      {row.sourceStatus === 'missing' || row.sourceStatus === 'replaced' ? <small className={styles.creditStatus}>{t(row.sourceStatus === 'missing' ? 'web.metadata.creditSourceMissing' : 'web.metadata.creditSourceReplaced')}</small> : null}
    </div>)}
    <footer className={styles.editorFooter}>
      <Button size="small" disabled={busy || draft === null} onClick={() => setDraft(null)}>{t('action.discard')}</Button>
      <Button size="small" variant="primary" disabled={busy || draft === null || !valid} onClick={() => void save()}>{t('web.metadata.saveCredits')}</Button>
    </footer>
  </div>;
}
