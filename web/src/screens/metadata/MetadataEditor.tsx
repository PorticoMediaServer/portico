import {UnlockIcon} from '../../kit/UnlockIcon';
import type {ReactNode} from 'react';
import {useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {attentionInProgress} from '@core/server-admin/attention-words.ts';
// The field label and pre-Save check are client-core's, shared with native's reader owner sheet.
import {metadataFieldError as fieldError, metadataFieldLabel as fieldLabel} from '@core/presentation/metadata-field.ts';
import {MetadataRepairService, type RepairCaller, type RepairCommand, type RepairData, type RepairFieldSpec, type RepairTarget} from '@core/metadata-repair.ts';
import {EditorHeader} from '../../kit/shell';
import {Icon} from '../../kit/Icon';
import {Button, TextField, TabsParts} from '../../kit/controls';
import {Dialog} from '../../kit/overlays/Dialog';
import {providerName} from '@core/server-admin/attention-words.ts';
import {MetadataError} from './MetadataError';
import styles from './metadata.module.css';
import {Artwork as Picture} from '../../kit/artwork';
import type {ShellArtwork} from '../../shell/artwork';
import {SongLyricsEditor} from './SongLyricsEditor';
import {metadataDraftValue, patchMetadataDraft, type MetadataFieldDraft} from './draft-value';
import {compactMetadataField, generalMetadataFields, metadataFieldMeter, metadataFieldSource, metadataTabCount, wideMetadataField, type MetadataTab as Tab} from './editor-presentation';
import {COMMON_LANGUAGES, languageName} from '@core/presentation/language-list.ts';

const t = defaultI18n.t;
type Api = Readonly<{request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>; requestMetadataUpload?: <T>(path: string, body: Blob, signal?: AbortSignal) => Promise<T>;
  requestRaw?: (path: string, method: string, options: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>) => Promise<{status: number; body?: unknown}>}>;
type Draft = Readonly<Record<string, MetadataFieldDraft>>;

const MATCHED_KINDS = new Set(['movie', 'show', 'album', 'artist']);
/** The editor's words for the rebuilt server's kinds of title. */
const LEDE_KINDS: Readonly<Record<string, string>> = {movie: 'film', episode: 'episode', show: 'show', season: 'season', album: 'album', artist: 'artist', track: 'song', audiobook: 'book'};
/** Only the tabs this type really has: no list fields means no Tags tab, no roles means no Artwork. */
function tabsFor(data: RepairData | null | undefined): readonly Tab[] {
  const tabs: Tab[] = ['general'];
  if (!data || data.artworkRoles.length) tabs.push('artwork');
  if (!data || data.schema.some(spec => spec.type === 'list')) tabs.push('tags');
  // Only a type the server actually matches online has a Matching tab (frame 10); a local-only
  // book has none because there is nothing to match against (frame 13).
  // Frame 9: credits exist only where the type carries them; an empty list is not a tab.
  if (data && (data.snapshot.relationships.some(row => row.kind === 'credit') || Object.hasOwn(data.snapshot.relationshipLocks, 'credit'))) tabs.push('people');
  // Only a title the server matches by itself has a Matching tab: an episode follows its show, a track its album.
  if (data && MATCHED_KINDS.has(data.itemKind ?? '')) tabs.push('matching');
  if (data?.itemKind === 'track') tabs.push('lyrics');
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
    // The service's own answer is already shown above; only a failure it didn't publish is shown here (no second notice).
    if (!saved && !service.getSnapshot().error) setError(new Error(t('web.metadata.saveChanges')));
    return saved;
  };
  const makeThumbnail = useCallback(async (expectedData: RepairData) => {
    try {await service.command({action: 'make_episode_thumbnail'}, expectedData);} catch (value) {setError(value);}
  }, [service]);
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
  const kind = !data ? 'item' : localArtworkOnly ? 'reading' : LEDE_KINDS[data.itemKind ?? ''] ?? 'other';
  return <TabsParts.Root asChild value={shown} onValueChange={value => {if (!openingCurrent() || !offered.includes(value as Tab)) return; if (value !== shown) retireOpening(); setTab(value as Tab);}}><aside className={styles.editor} aria-label={t('web.metadata.sections')}>
    <EditorHeader audience={{authority: 'owner'}} title={heading ? t('web.metadata.editTitle', {title: heading}) : t('web.metadata.editFallbackTitle')} description={t('web.metadata.editorLede', {kind})} onClose={close} className={styles.editorHead}/>
    <TabsParts.List asChild><nav ref={tabStrip} className={styles.editorTabs} aria-label={t('web.metadata.sections')}>
      {tabsFor(data).map(id => <TabsParts.Trigger key={id} value={id} asChild><button type="button" aria-current={shown === id ? 'page' : undefined}>
        {t(id === 'general' ? 'web.metadata.tabGeneral' : id === 'artwork' ? 'web.metadata.tabArtwork' : id === 'tags' ? data && !data.schema.some(field => field.field === 'genres' || field.field === 'genre') ? 'web.metadata.field.tags' : 'web.metadata.tabTags' : id === 'people' ? 'web.metadata.tabPeople' : id === 'matching' ? 'web.metadata.tabMatching' : id === 'lyrics' ? 'web.metadata.tabLyrics' : 'web.metadata.tabHistory')}
        {metadataTabCount(id, data, draft) !== undefined && <small>{metadataTabCount(id, data, draft)}</small>}</button></TabsParts.Trigger>)}
    </nav></TabsParts.List>
    <TabsParts.Content value={shown} className={styles.editorBody}>
    {state.phase === 'loading' ? <p className={styles.working}>{t('web.metadata.loading')}</p> : null}
    {state.error ? <MetadataError error={state.error} /> : null}
    {state.phase === 'uncertain' || state.phase === 'conflict' || state.phase === 'error' ? <Button size="small" onClick={() => {setError(undefined); void service.load(target);}}>{t('next.states.tryAgain')}</Button> : null}
    {error ? <MetadataError error={error} /> : null}
    {state.phase === 'saving' ? <p className={styles.working}>{t('web.metadata.applying')}</p> : null}
    {data && state.phase !== 'loading' ? <>
      {shown === 'general' ? <General data={data} draft={draft} set={set} requiredTitle={target.kind !== 'season'} /> : null}
      {shown === 'tags' ? <Tags data={data} draft={draft} set={set} busy={state.phase !== 'ready'} /> : null}
      {shown === 'people' ? <People data={data} target={target} busy={state.phase !== 'ready'} opening={openingLease} openingSignal={openingLease?.controller.signal} openingCurrent={openingCurrent}
        onCommit={(rows, caller) => run({action: 'edit_relationships', role: 'credit', confirm: true, relationships: rows, ...(data.snapshot.canEditProviderCredits === true ? {effectiveCredits: true as const} : {})}, caller)}
        onLock={(locked, caller) => run({action: 'relationship_lock', role: 'credit', locked, confirm: true}, caller)} /> : null}
      {shown === 'matching' ? <Matching data={data} busy={state.phase !== 'ready'} onSearch={query => void run({action: 'search', query})}
        onRetry={() => void run({action: 'retry'})} onLock={locked => void run({action: 'relationship_lock', role: 'identity', locked, confirm: true})}
        onKeep={() => void run({action: 'keep_as_is', confirm: true})}
        onUse={(provider, candidateId, order, episodeOffset) => void run({action: 'identify', provider, candidateId, confirm: true, ...(order ? {order} : {}), ...(episodeOffset ? {episodeOffset} : {})})} /> : null}
      {shown === 'artwork' && data.artworkRoles.includes('cover') && api.requestMetadataUpload ? renderCoverPages?.(file => service.uploadArtwork('cover', file), state.phase !== 'ready') : null}
      {shown === 'artwork' ? <Artwork artwork={artwork} portraitCover={localArtworkOnly} data={data} busy={state.phase !== 'ready'} onUse={(role, candidateId) => void run({action: 'select_artwork', role, candidateId, confirm: true})}
        onRemove={removeUpload} isDataCurrent={currentRepairData} onMakeFromVideo={data.canMakeEpisodeThumbnail ? makeThumbnail : undefined} localOnly={target.kind === 'book'}
        onUpload={api.requestMetadataUpload ? (role, file) => service.uploadArtwork(role, file) : undefined}
        onLock={(role, subject, locked) => void run({action: 'artwork_lock', role, subject, locked})}
        onRefetch={role => void run({action: 'refetch_artwork', role})}
        onFindMore={localArtworkOnly || target.kind === 'book' || data.onlineLookups === false ? undefined : role => void run({action: 'discover_artwork', role})} /> : null}
      {shown === 'lyrics' ? <SongLyricsEditor api={api} songId={target.id}/> : null}
      {shown === 'history' ? <History data={data} busy={state.phase !== 'ready'} onUndo={historyId => void run({action: 'undo', historyId, confirm: true})}
        onAll={locked => void run({action: locked ? 'lock_all' : 'unlock_all', confirm: true})} /> : null}
    </> : null}
    </TabsParts.Content>
    {/* Frame 6: Save stays pinned under the scrolling fields. */}
    {data && state.phase !== 'loading' && (shown === 'general' || shown === 'tags') ? <footer className={styles.editorFooter}>
      <span className={styles.pending} data-changed={!!changed.length || undefined}>{t(changed.length ? 'web.metadata.pendingCount' : 'web.metadata.noChanges', {count: changed.length})}</span>
      <div className={styles.editorActions}><Button onClick={onClose}>{t('action.cancel')}</Button>
        <Button variant="primary" loading={state.phase === 'saving'} disabled={!changed.length || invalid || state.phase !== 'ready'} onClick={save}>
          {t('web.metadata.saveChanges')}</Button></div>
    </footer> : null}
  </aside></TabsParts.Root>;
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
  // Frame 6: the language is chosen by name. The server takes any valid language tag,
  // so a value set elsewhere stays one of the choices.
  if (spec.field === 'metadataLanguage') return <select {...accessibility} value={value} data-empty={!value || undefined} onChange={event => set(event.currentTarget.value)}>
    <option value="">{t('web.metadata.fromLibrary')}</option>
    {[...new Set([...(value ? [value] : []), ...COMMON_LANGUAGES])].map(code => <option key={code} value={code}>{languageName(code, key => defaultI18n.has(key) ? t(key) : undefined, defaultI18n.locale)}</option>)}</select>;
  if (spec.type === 'multiline') return <textarea {...accessibility} rows={4} value={value} placeholder={t('web.metadata.notSet')} onChange={event => set(event.currentTarget.value)} />;
  return <input {...accessibility} type={spec.type === 'date' ? 'date' : 'text'} inputMode={spec.type === 'integer' || spec.type === 'number' ? 'numeric' : undefined} value={value} placeholder={t('web.metadata.notSet')} onChange={event => set(event.currentTarget.value)} />;
}


function sourceWords(source: string | undefined): string {
  const origin = metadataFieldSource(source);
  return !origin ? '' : origin.kind === 'edited' ? t('web.metadata.editedHere') : origin.kind === 'library' ? t('web.metadata.fromLibrary') : t('web.metadata.fromSource', {source: providerName(origin.provider)});
}

function Field({spec, field, draft, set, requiredTitle = false}: {requiredTitle?: boolean; spec: RepairFieldSpec; field?: {value: string; automaticValue: string; locked: boolean; source: string; values?: readonly string[]}; draft?: {value: string; locked: boolean}; set: (patch: Partial<MetadataFieldDraft>) => void}) {
  const id = useId();
  const value = draft?.value ?? field?.value ?? '', locked = draft?.locked ?? field?.locked ?? false, error = fieldError(spec, value, [], requiredTitle);
  const changed = draft !== undefined && (draft.value !== (field?.value ?? '') || draft.locked !== (field?.locked ?? false));
  const meter = metadataFieldMeter(spec);
  const hint = meter === 'range' ? spec.min !== undefined && spec.max !== undefined ? t('web.metadata.numericBounds', {min: spec.min, max: spec.max}) : spec.min !== undefined ? t('web.metadata.numericMinimum', {min: spec.min}) : t('web.metadata.numericMaximum', {max: spec.max!}) : meter === 'count' ? t('web.metadata.characterCount', {count: Array.from(value).length, max: spec.maxLength!}) : '';
  const origin = sourceWords(field?.source), label = fieldLabel(spec), from = metadataFieldSource(field?.source), provider = from?.kind === 'provider' ? from.provider : undefined;
  // An empty field with no origin says what belongs there instead, where that needs saying.
  const said = origin || (spec.field === 'edition' && !value ? t('web.metadata.editionHint') : '');
  return <label className={styles.field} data-wide={wideMetadataField(spec) || undefined} data-short={compactMetadataField(spec) || undefined} data-changed={changed || undefined}>
    <span id={id + '-label'} className={styles.fieldLabel}>{label}
      {changed ? <em>{t('web.metadata.editorChanged')}</em> : null}
    </span>
    <span className={styles.fieldControl} data-invalid={!!error || undefined}>{input(spec, value, next => set({value: next, ...(spec.field === 'metadataLanguage' ? {locked: false} : {})}), {'aria-labelledby': id + '-label', 'aria-describedby': id + '-hint' + (error ? ' ' + id + '-error' : ''), 'aria-invalid': Boolean(error)})}
      {spec.field !== 'metadataLanguage' ? <button type="button" className={styles.lock} aria-pressed={locked} title={t(locked ? 'web.metadata.lockedTip' : 'web.metadata.unlockedTip')} aria-label={t(locked ? 'web.metadata.unlockField' : 'web.metadata.lockField', {label})} onClick={() => set({locked: !locked})}>{locked ? <Icon name="i-lock" size={16}/> : <UnlockIcon size={16}/>}</button> : null}
    </span>
    {error ? <small id={id + '-error'} className={styles.fieldError} role="alert"><Icon name="i-warn" size={16}/>{error}</small> : null}
    <small id={id + '-hint'}>{said}{hint ? <span className={styles.fieldBounds}>{hint}</span> : null}</small>
    {field && field.automaticValue && field.automaticValue !== value ? <small className={styles.automatic}>{provider ? t('web.metadata.automaticValueFrom', {source: providerName(provider), value: field.automaticValue}) : t('web.metadata.automaticValue', {value: field.automaticValue})}<button type="button" onClick={() => set({value: field.automaticValue, locked: false})}>{t('web.metadata.useIt')}</button></small> : null}
  </label>;
}

function General({data, draft, set, requiredTitle}: {data: RepairData; draft: Draft; requiredTitle: boolean; set: (field: string, patch: Partial<MetadataFieldDraft>) => void}) {
  // Frame 6: the lock line carries the lock and names Unlock in bold.
  const [before, after = ''] = t('web.metadata.editorLockHelp', {unlock: '\u0000'}).split('\u0000');
  return <div className={styles.generalFields}><p className={styles.lockHelp}><Icon name="i-lock" size={16}/><span>{before}<b>{t('web.metadata.unlockWord')}</b>{after}</span></p>{generalMetadataFields(data.schema).map(spec =>
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
  const label = fieldLabel(spec), genre = spec.field === 'genres' || spec.field === 'genre';
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
  const fromProvider = chosen && candidates.some(candidate => candidate.id === chosen.candidateId && candidate.provider === 'provider');
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
        {onRefetch && fromProvider ? <Button size="small" onClick={() => onRefetch(role)} disabled={busy}>{t('web.metadata.refetchChosen')}</Button> : null}
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
    <Dialog footerDismisses open={Boolean(removing && currentRemoval(removing))} title={t('web.metadata.removeUploadTitle')} description={t('web.metadata.removeUploadBody')} onOpenChange={open => {if (!open) retireRemoval(removing);}}
      dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy}
      footer={<><Button size="small" disabled={busy} onClick={() => retireRemoval(removing)}>{t('action.cancel')}</Button><Button size="small" disabled={busy} onClick={() => void confirmRemoval(removing)}>{t('web.metadata.removeUpload')}</Button></>}>{removeFailed ? <p role="alert">{t('web.metadata.removeUploadFailed')}</p> : null}</Dialog>
  </div>;
}


function History({data, busy, onUndo, onAll}: {data: RepairData; busy: boolean; onUndo: (historyId: number | string) => void; onAll: (locked: boolean) => void}) {
  return <div className={styles.fields}>
    <div className={styles.editorFooter}><Button size="small" disabled={busy} onClick={() => onAll(true)}>{t('web.metadata.lockEverything')}</Button>
      <Button size="small" disabled={busy} onClick={() => onAll(false)}>{t('web.metadata.unlockEverything')}</Button></div>
    <h3>{t('web.metadata.recentChanges')}</h3>
    {data.history.length ? data.history.map(row => <div key={row.id} className={styles.historyRow}>
      <div><b>{t('web.metadata.history', {trigger: row.trigger})}</b>{row.observedAt ? <small>{defaultI18n.dateTime(row.observedAt)}</small> : null}</div>
      <Button size="small" disabled={busy} onClick={() => onUndo(row.id)}>{t('web.metadata.undoThisChange')}</Button>
    </div>) : <p className={styles.working}>{t('web.metadata.noHistory')}</p>}
  </div>;
}


const ORDERS = ['aired', 'dvd', 'absolute'] as const;
/** The rebuilt server's cast and crew roles. */
const CREDIT_ROLES = ['actor', 'director', 'writer', 'producer', 'composer', 'author', 'narrator', 'artist', 'guest', 'crew'] as const;
/** Frame 10 and rules 13a to 17: what the title is matched to, its lock, a search or an ID, Look it up again, and Keep
 * as is, which takes a wrong match off. A show's match carries the episode order and number offset its files use. */
function Matching({data, busy, onSearch, onUse, onRetry, onLock, onKeep}: {
  data: RepairData; busy: boolean; onSearch: (query: string) => void; onRetry: () => void; onLock: (locked: boolean) => void; onKeep: () => void;
  onUse: (provider: string, candidateId: string, order?: string, offset?: number) => void;
}) {
  const identity = data.snapshot.identity;
  const [query, setQuery] = useState(() => data.snapshot.fields.title?.value ?? '');
  const [order, setOrder] = useState(identity?.order || 'aired'), [offset, setOffset] = useState(String(identity?.offset ?? 0));
  const [keeping, setKeeping] = useState(false);
  const show = data.itemKind === 'show';
  const matched = !!identity?.provider && !!identity.id;
  const state = identity?.status ?? 'unmatched';
  const status = t(state === 'matched' || state === 'chosen' ? 'web.metadata.matchStatus.matched' : state === 'kept' ? 'web.metadata.matchStatus.kept' : 'web.metadata.matchStatus.unmatched');
  const validOffset = /^-?\d+$/.test(offset.trim()) && Math.abs(Number(offset)) <= 9999;
  return <div className={styles.fields}>
    <section className={styles.candidate}>
      <div><b>{t('web.metadata.currentMatch')}</b>
        <small>{matched ? [providerName(identity!.provider), identity!.id, status].join(' · ') : status}</small></div>
      <button type="button" className={styles.lock} disabled={busy} aria-pressed={identity?.locked === true}
        onClick={() => onLock(identity?.locked !== true)}>{t(identity?.locked ? 'web.metadata.identityLocked' : 'web.metadata.identityUnlocked')}</button>
    </section>
    {matched ? <div className={styles.keepMatch}><p className={styles.lockHelp}>{t('web.metadata.keepAsIsHelp')}</p>
      <Button size="small" disabled={busy} onClick={() => setKeeping(true)}>{t('metadata.attention.keepAsIs')}</Button></div> : null}
    {show ? <div className={styles.matchOrder}>
      <label className={styles.order}>{t('metadata.attention.episodeOrder')}
        <select value={order} disabled={busy} onChange={event => setOrder(event.currentTarget.value)}>{ORDERS.map(item => <option key={item} value={item}>{t(`web.metadata.episodeOrderName.${item}`)}</option>)}</select></label>
      <TextField label={t('web.metadata.numberOffset')} description={t('web.metadata.numberOffsetHelp')} inputMode="numeric" value={offset} disabled={busy}
        onChange={event => setOffset(event.currentTarget.value)} error={validOffset ? undefined : t('web.metadata.numberRange', {label: t('web.metadata.numberOffset'), min: -9999, max: 9999})}/>
    </div> : null}
    <form className={styles.matchSearch} onSubmit={event => {event.preventDefault(); if (query.trim() && !busy) onSearch(query.trim());}}>
      <TextField label={t('web.metadata.searchProvider')} description={t('web.metadata.searchProviderHelp')} disabled={busy} value={query} onChange={event => setQuery(event.currentTarget.value)} />
      <Button size="small" type="submit" disabled={busy || !query.trim()}>{t('web.search.title')}</Button>
      <Button size="small" type="button" disabled={busy} onClick={onRetry}>{t('server.attention.lookUpAgain')}</Button>
    </form>
    {data.candidates.map(candidate => {
      const current = matched && identity!.provider === candidate.provider && identity!.id === candidate.id;
      return <div key={`${candidate.provider}:${candidate.id}`} className={`${styles.candidate} ${styles.matchCandidate}`}>
        <div className={styles.matchDetails}><b>{candidate.title}{candidate.year ? ` (${candidate.year})` : ''}</b><small>{providerName(candidate.provider)}{candidate.subtitle ? ` · ${candidate.subtitle}` : ''}</small></div>
        {current ? <span className={styles.matchCurrent}>{t('web.metadata.currentMatch')}</span>
          : <Button size="small" disabled={busy || show && !validOffset} onClick={() => onUse(candidate.provider, candidate.id, show ? order : undefined, show ? Number(offset) : undefined)}>{t('metadata.attention.useMatch')}</Button>}
      </div>;
    })}
    <p className={styles.lockHelp}>{t('web.metadata.chooseMatchHelp')}</p>
    <Dialog footerDismisses width={480} open={keeping} onOpenChange={open => {if (!open) setKeeping(false);}} title={t('web.metadata.keepAsIsTitle')} description={t('web.metadata.keepAsIsBody')}
      footer={<><Button size="small" onClick={() => setKeeping(false)}>{t('action.cancel')}</Button>
        <Button size="small" variant="primary" disabled={busy} onClick={() => {setKeeping(false); onKeep();}}>{t('web.metadata.keepAsIsConfirm')}</Button></>}>{null}</Dialog>
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
    setDraft([...rows, {kind: 'credit', provider: 'manual', targetKind: 'person', targetId: '', label: '', role: '', department: 'actor',
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
      <label className={styles.picker}><span>{t('web.metadata.creditRole')}</span>
        <select value={row.department ?? ''} disabled={busy || !editable(row)} onChange={event => change(index, {department: event.currentTarget.value})}>
          {!CREDIT_ROLES.includes(row.department as typeof CREDIT_ROLES[number]) ? <option value={row.department ?? ''}>{row.department ?? ''}</option> : null}
          {CREDIT_ROLES.map(role => <option key={role} value={role}>{t(`web.metadata.creditRoleName.${role}`)}</option>)}</select></label>
      <TextField label={t(row.department === 'actor' || row.department === 'guest' ? 'web.metadata.creditCharacter' : 'web.metadata.field.role')} value={row.role ?? ''} disabled={busy || !editable(row)} maxLength={500} onChange={event => change(index, {role: event.currentTarget.value})}/>
      <Button size="small" variant="icon" icon="i-trash" iconOnly aria-label={t('web.metadata.removeLabel', {label: row.label})} title={t('web.metadata.removeLabel', {label: row.label})} disabled={busy || !editable(row)} onClick={() => remove(index)}/>
      {row.sourceStatus === 'missing' || row.sourceStatus === 'replaced' ? <small className={styles.creditStatus}>{t(row.sourceStatus === 'missing' ? 'web.metadata.creditSourceMissing' : 'web.metadata.creditSourceReplaced')}</small> : null}
    </div>)}
    <footer className={styles.editorFooter}>
      <Button size="small" disabled={busy || draft === null} onClick={() => setDraft(null)}>{t('action.discard')}</Button>
      <Button size="small" variant="primary" disabled={busy || draft === null || !valid} onClick={() => void save()}>{t('web.metadata.saveCredits')}</Button>
    </footer>
  </div>;
}
