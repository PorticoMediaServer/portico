import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent} from 'react';
import {defaultI18n} from '@i18n';
import {metadataAttentionService, type AttentionCommand, type AttentionEntry, type AttentionPage} from '@core/metadata-attention.ts';
import {attentionOffers, attentionReason, attentionInProgress, candidateFacts, candidateLine, providerName, typedProviderId} from '@core/server-admin/attention-words.ts';
import {Button, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {Icon, type IconName} from '../../kit/Icon';
import {HostedError} from '../../kit/accounts/ServicesAuth';
import styles from './metadata.module.css';
import {Artwork, GeneratedCover} from '../../kit/artwork';
import {generatedCoverTint} from '@design';
import type {ShellArtwork} from '../../shell/artwork';

const t = defaultI18n.t, has = (id: string) => defaultI18n.has(id);
type Api = Readonly<{request: <T>(path: string, method?: string, body?: unknown) => Promise<T>}>;
type Admission = {service: ReturnType<typeof metadataAttentionService>; alive: boolean; generation: number; reading: boolean; failed?: string; failedGeneration?: number};
type ReadView = {admission: Admission; page?: AttentionPage; cursor: string; cursors: readonly string[]; loading: boolean; error?: unknown; failed?: string; failedGeneration?: number};
const PROVIDERS = ['tmdb', 'tvdb', 'anilist'] as const;

/**
 * owner-metadata frames 2-4: the titles Portico could not match with confidence, each with
 * why, up to three closest matches, and only the actions the server names for it. The list is
 * the library's own; the server stays the authority on what each row offers.
 */
export function NeedsAttention({libraryId, libraryName, api, onOpen, onEdit, artwork}: {artwork?: ShellArtwork; libraryId: string; libraryName?: string; api: Api; onOpen?: (entry: AttentionEntry) => void; onEdit?: (entry: AttentionEntry, anchor: HTMLElement, matching?: boolean) => void}) {
  const service = useMemo(() => metadataAttentionService(<T,>(path: string, method?: string, body?: unknown) => api.request<T>(path, method, body), libraryId), [api, libraryId]);
  const [admission, setAdmission] = useState<Admission | null>(null);
  const liveAdmission = useRef<Admission | null>(null);
  const [readView, setReadView] = useState<ReadView | null>(null);
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState<string>(), [notice, setNotice] = useState<string>();
  const [searching, setSearching] = useState<AttentionEntry>(), [identifying, setIdentifying] = useState<AttentionEntry>();
  const [query, setQuery] = useState(''), [typed, setTyped] = useState(''), [provider, setProvider] = useState<(typeof PROVIDERS)[number]>('tmdb');
  const searchInput = useRef<HTMLInputElement>(null), idInput = useRef<HTMLInputElement>(null);
  const load = useCallback(async (held: Admission, next: string) => {
    if (liveAdmission.current !== held || !held.alive || held.reading) return;
    const current = ++held.generation;
    held.reading = true;
    held.failed = undefined; held.failedGeneration = undefined;
    setReadView(value => value?.admission === held ? {...value, loading: true, error: undefined, failed: undefined} : value);
    try {
      const loaded = await held.service.list(next, 25, true);
      if (liveAdmission.current !== held || !held.alive || current !== held.generation) return;
      setReadView(value => value?.admission === held ? {...value, page: loaded, cursor: next, loading: false} : value);
    } catch (value) {
      // The failed attempt stays retryable at its own cursor; accepted rows and pager stay put.
      if (liveAdmission.current !== held || !held.alive || current !== held.generation) return;
      held.failed = next; held.failedGeneration = current;
      setReadView(state => state?.admission === held ? {...state, loading: false, error: value, failed: next, failedGeneration: current} : state);
    }
    finally {
      if (current === held.generation) {
        held.reading = false;
        if (liveAdmission.current === held) setReadView(value => value?.admission === held ? {...value, loading: false} : value);
      }
    }
  }, []);
  // Admission is layout-committed. A replaced service/API/library retires its predecessor before
  // paint, and retained callbacks capture their own admission and refuse before any request/state.
  useLayoutEffect(() => {
    const previous = liveAdmission.current;
    if (previous?.service === service) return;
    if (previous) {previous.alive = false; previous.generation++;}
    const admitted: Admission = {service, alive: true, generation: 0, reading: false};
    liveAdmission.current = admitted;
    setAdmission(admitted);
    setReadView({admission: admitted, cursor: '', cursors: [], loading: true});
    void load(admitted, '');
  }, [service, load]);
  useLayoutEffect(() => () => { const held = liveAdmission.current; liveAdmission.current = null; if (held) {held.alive = false; held.generation++;} }, []);
  // Pure render match: the admission must belong to this render's exact service, not just be the
  // state value, so a replacement render before layout cannot draw the predecessor's rows.
  const shown = readView && readView.admission === admission && admission?.service === service ? readView : undefined;
  const page = shown?.page, loading = shown?.loading ?? true, cursors = shown?.cursors ?? [], readError = shown?.error;
  const retryRead = () => {
    const held = admission;
    if (!held || liveAdmission.current !== held || !shown || shown.failed === undefined || held.reading) return;
    // Only the same still-failed attempt is retryable; a recovered or newer failure is not.
    if (held.failed !== shown.failed || held.failedGeneration !== shown.failedGeneration) return;
    void load(held, shown.failed);
  };
  // A search or a match settles on the server; a failed read suspends the poll until Retry.
  const settling = !!page?.entries.some(entry => attentionInProgress(entry.reason));
  useEffect(() => {
    if (!settling || !shown || shown.failed !== undefined || shown.loading) return;
    const held = shown.admission, next = shown.cursor, generation = held.generation;
    const timer = setInterval(() => {
      // A queued pre-cleanup tick belongs to its own read generation; a failure or a newer read
      // invalidates it synchronously instead of reloading the accepted cursor.
      if (liveAdmission.current !== held || !held.alive || held.reading) return;
      if (held.generation !== generation || held.failed !== undefined) return;
      void load(held, next);
    }, 2000);
    return () => clearInterval(timer);
  }, [settling, shown, load]);
  const send = async (entry: AttentionEntry, command: Omit<AttentionCommand, 'kind' | 'entityId' | 'expectedRevision'>, done: string) => {
    if (busy) return false;
    setBusy(entry.entityId); setNotice(undefined); setError(undefined);
    try {
      await service.act({kind: entry.kind, entityId: entry.entityId, expectedRevision: entry.revision, ...command});
      setNotice(done); setSearching(undefined); setIdentifying(undefined);
      if (admission) await load(admission, shown?.cursor ?? '');
      return true;
    } catch (value) {setError(value); return false;}
    finally {setBusy(undefined);}
  };
  const older = () => {
    const held = admission;
    if (!held || liveAdmission.current !== held || held.reading || !shown?.page?.nextCursor) return;
    setReadView(value => value?.admission === held ? {...value, cursors: [...value.cursors, value.cursor]} : value);
    void load(held, shown.page.nextCursor);
  };
  const newer = () => {
    const held = admission;
    if (!held || liveAdmission.current !== held || held.reading || !shown?.cursors.length) return;
    const next = shown.cursors.at(-1)!;
    setReadView(value => value?.admission === held ? {...value, cursors: value.cursors.slice(0, -1)} : value);
    void load(held, next);
  };
  const id = typedProviderId(typed), bare = !!id && /^\d+$/.test(id);
  return <section className={styles.attention}>
    {error ? <HostedError error={error} /> : null}
    {readError ? <div role="group">
      <HostedError error={readError} />
      <Button size="small" icon="i-retry" disabled={loading} loading={loading} onClick={retryRead}>{t('next.browse.retry')}</Button>
    </div> : null}
    {notice ? <p className={styles.notice} role="status">{notice}</p> : null}
    <div className={styles.listBox}>{page?.entries.map(entry => <Row key={entry.entityId} entry={entry} artwork={artwork} busy={busy === entry.entityId}
      onUse={(candidateKey, order) => void send(entry, {action: 'accept', candidateKey, ...(order ? {order} : {})}, t('server.attention.matched', {title: entry.candidates.find(value => value.key === candidateKey)?.title ?? entry.title}))}
      onSearch={() => {setQuery(entry.title); setSearching(entry);}}
      onIdentify={() => {setTyped(''); setProvider('tmdb'); setIdentifying(entry);}}
      onRetry={() => void send(entry, {action: 'retry'}, t('server.attention.lookingUp'))}
      onKeep={() => void send(entry, {action: 'dismiss'}, t('metadata.attention.kept'))}
      onOpen={onOpen ? () => onOpen(entry) : undefined} onEdit={onEdit && (['show', 'album', 'book', 'song'].includes(entry.kind) || entry.kind === 'item' && ['movie', 'episode', 'song', 'audiobook_file'].includes(entry.itemKind)) ? (anchor, matching) => onEdit(entry, anchor, matching) : undefined} />)}</div>
    {!loading && !error && page && !page.entries.length ? <div className={styles.empty}>
      <span className={styles.emptyIcon}><Icon name="i-check" size={28}/></span><b>{t('metadata.attention.empty')}</b><p>{libraryName ? t('metadata.attention.emptyLibraryBody', {library: libraryName}) : t('metadata.attention.emptyBody')}</p>
    </div> : null}
    {!loading && !error && page?.entries.length ? <p className={styles.listSummary}>{t('metadata.attention.showing', {count: page.entries.length, total: page.total})}</p> : null}
    {(cursors.length > 0 || page?.nextCursor) ? <div className={styles.pager}>
      <Button size="small" disabled={loading || !cursors.length} onClick={newer}>{t('web.hostedAccount.newer')}</Button>
      <Button size="small" disabled={loading || !page?.nextCursor} onClick={older}>{t('web.hostedAccount.older')}</Button>
    </div> : null}
    <Dialog width={520} initialFocusRef={searchInput} bodyClassName={styles.dialogBody} open={Boolean(searching)} onOpenChange={open => {if (!open && !busy) setSearching(undefined);}} title={t('server.attention.searchTitle')} description={t('server.attention.searchBody')}
      footer={<><Button size="small" disabled={Boolean(busy)} onClick={() => setSearching(undefined)}>{t('action.cancel')}</Button>
        <Button size="small" variant="primary" loading={Boolean(busy)} disabled={query.trim().length < 2} onClick={() => {if (searching) void send(searching, {action: 'search', query: query.trim()}, t('server.attention.searchStarted'));}}><Icon name="i-search" size={18} />{t('web.search.title')}</Button></>}>
      <form onSubmit={(event: FormEvent) => {event.preventDefault(); if (searching && query.trim().length >= 2 && !busy) void send(searching, {action: 'search', query: query.trim()}, t('server.attention.searchStarted'));}}>
        <TextField ref={searchInput} label={t('server.attention.searchFor')} value={query} onChange={event => setQuery(event.currentTarget.value)} maxLength={200} />
      </form>
    </Dialog>
    <Dialog width={520} initialFocusRef={idInput} bodyClassName={styles.dialogBody} open={Boolean(identifying)} onOpenChange={open => {if (!open && !busy) setIdentifying(undefined);}} title={t('metadata.attention.enterProviderId')} description={identifying?.title}
      footer={<><Button size="small" disabled={Boolean(busy)} onClick={() => setIdentifying(undefined)}>{t('action.cancel')}</Button>
        <Button size="small" variant="primary" loading={Boolean(busy)} disabled={!id} onClick={() => {if (identifying && id) void send(identifying, {action: 'id', providerId: id, ...(bare ? {provider} : {})}, t('server.attention.idSaved'));}}>{t('server.attention.useId')}</Button></>}>
      <TextField ref={idInput} label={t('server.attention.idLabel')} description={t('server.attention.idHelp')} value={typed} onChange={event => setTyped(event.currentTarget.value)} maxLength={2048} />
      {bare ? <label className={styles.picker}>
        <span>{t('server.attention.idProvider')}</span>
        <select value={provider} onChange={event => setProvider(event.currentTarget.value as (typeof PROVIDERS)[number])}>
          {PROVIDERS.map(item => <option key={item} value={item}>{providerName(item)}</option>)}
        </select>
      </label> : null}
    </Dialog>
  </section>;
}

function Row({entry, busy, onUse, onSearch, onIdentify, onRetry, onKeep, onOpen, onEdit, artwork}: {
  artwork?: ShellArtwork; entry: AttentionEntry; busy: boolean; onUse: (candidateKey: string, order?: string) => void;
  onSearch: () => void; onIdentify: () => void; onRetry: () => void; onKeep: () => void; onOpen?: () => void; onEdit?: (anchor: HTMLElement, matching?: boolean) => void;
}) {
  const [orders, setOrders] = useState<Readonly<Record<string, string>>>({});
  const music = entry.kind === 'album' || entry.kind === 'song' || entry.kind === 'book';
  // Frame 5: a release is chosen on the album's own Matching tab, never from this list.
  const candidates = attentionOffers(entry, 'accept') && !music ? entry.candidates : [];
  const subtitle = entry.parentTitle ?? entry.parentArtist;
  const kind = entry.kind === 'item' ? (entry.itemKind === 'episode' ? 'episode' : 'film') : entry.kind;
  const kindIcon: IconName = kind === 'show' ? 'i-tv' : kind === 'book' ? 'i-book' : kind === 'album' || kind === 'song' ? 'i-music' : 'i-film';
  const working = busy || attentionInProgress(entry.reason);
  const hasCover = entry.kind === 'album' || entry.kind === 'song';
  const coverTitle = entry.kind === 'song' ? entry.parentTitle ?? entry.title : entry.title;
  const fallbackCover = <GeneratedCover title={coverTitle} author={entry.parentArtist} glyph={Array.from(coverTitle.trim())[0] ?? ''} tint={generatedCoverTint(coverTitle)}/>;
  return <article className={styles.row} data-busy={working || undefined}>
    <header className={styles.rowHead}>
      <span className={styles.kindTile} aria-hidden="true" data-cover={hasCover || undefined}>{hasCover ? <Artwork source={artwork?.foreground(entry.artworkUrl, 120, 1)} alt="" fallback={fallbackCover}/> : <Icon name={kindIcon} size={24} />}</span>
      <div><h3>{onEdit ? <button type="button" disabled={busy} onClick={event => onEdit(event.currentTarget)}>{entry.title || t('server.attention.untitled')}</button> : onOpen ? <button type="button" onClick={onOpen}>{entry.title || t('server.attention.untitled')}</button> : entry.title || t('server.attention.untitled')}<span className={styles.kindChip}>{t(`metadata.attention.kind.${kind}`)}</span></h3>
      {subtitle ? <small>{subtitle}</small> : null}
      <p>{attentionReason(t, has, entry.reason, entry.kind)}</p>
      {music && entry.candidates.length ? <ul className={styles.candidatesOnly}>{entry.candidates.map(candidate => <li key={candidate.key}>{candidateLine(candidate)}</li>)}</ul> : null}
      </div>
      {!working && attentionOffers(entry, 'dismiss') ? <button className={styles.textAction} type="button" disabled={busy} onClick={onKeep}>{t('metadata.attention.keepAsIs')}</button> : null}
    </header>
    {working ? <p className={styles.working}>{t(entry.reason.includes('search') ? 'metadata.attention.rowSearching' : 'metadata.attention.rowMatching')}</p> : <>
      {candidates.map(candidate => {
        const facts = candidate.reasons.flatMap(reason => candidateFacts(t, has, {reasons: [reason]}).map(words => ({reason, words})));
        const selected = candidate.orders.find(item => item.id === orders[candidate.key])?.id ?? candidate.orders[0]?.id;
        return <div key={candidate.key} className={styles.candidate}>
          <span className={styles.candidatePoster} aria-hidden="true">{candidate.posterUrl ? <img src={candidate.posterUrl} alt="" loading="lazy"/> : <i>{Array.from(candidate.title.trim())[0] ?? ''}</i>}</span>
          <div><div className={styles.candidateTitle}><b>{candidate.title}</b>{candidate.year > 0 ? <span className={styles.candidateYear}>{candidate.year}</span> : null}<span className={styles.provider}>{providerName(candidate.identity.provider)}</span></div>
            {facts.length ? <div className={styles.facts}>{facts.map(({reason, words}) => <span key={reason} className={styles.fact} data-positive={reason === 'exact_title_or_alias' || reason === 'release_year_agrees' || undefined}>{words}</span>)}</div> : null}
            {candidate.orders.length > 1 ? <label className={styles.order}>{t('metadata.attention.episodeOrder')}
              <select value={selected} onChange={event => {const value = event.currentTarget.value; setOrders(current => ({...current, [candidate.key]: value}));}}>
                {candidate.orders.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
              </select></label> : null}</div>
          <Button size="small" disabled={busy} onClick={() => onUse(candidate.key, candidate.orders.length ? selected : undefined)}>{t('metadata.attention.useMatch')}</Button>
        </div>;
      })}
      <div className={styles.actions}>
        {!music && (attentionOffers(entry, 'search') || attentionOffers(entry, 'id')) ? <span>{t(candidates.length ? 'metadata.attention.notThese' : 'metadata.attention.lookYourself')}</span> : null}
        {attentionOffers(entry, 'search') ? <button className={styles.textAction} type="button" disabled={busy} onClick={onSearch}><Icon name="i-search" size={16} />{t('metadata.attention.searchByHand')}</button> : null}
        {attentionOffers(entry, 'id') ? <button className={styles.textAction} type="button" disabled={busy} onClick={onIdentify}><Icon name="i-link" size={16} />{t('metadata.attention.enterProviderId')}</button> : null}
        {entry.kind === 'album' && entry.candidates.length > 0 && onEdit ? <button className={styles.textAction} type="button" disabled={busy} onClick={event => onEdit(event.currentTarget, true)}><Icon name="i-music" size={16} />{t('metadata.attention.chooseRelease')}</button> : null}
        {onOpen && (music || attentionOffers(entry, 'parent')) ? <button className={styles.textAction} type="button" disabled={busy} onClick={event => {if (entry.kind === 'song' && onEdit) onEdit(event.currentTarget, true); else onOpen();}}><Icon name={kindIcon} size={16} />{t(music ? entry.kind === 'book' ? 'metadata.attention.openBook' : 'metadata.attention.openAlbum' : 'metadata.attention.openShow')}</button> : null}
        {!candidates.length && attentionOffers(entry, 'retry') ? <button className={styles.textAction} type="button" disabled={busy} onClick={onRetry}><Icon name="i-refresh" size={16} />{t('server.attention.lookUpAgain')}</button> : null}
      </div>
    </>}
  </article>;
}
