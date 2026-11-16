import {useCallback, useEffect, useRef, useState, type FormEvent} from 'react';
import {defaultI18n} from '@i18n';
import {EPISODE_ORDERS, chooseMatch, findMatches, keepAsIs, lookUpAgain, providerRef, readUnmatched, type EpisodeOrder, type MatchCandidate, type UnmatchedEntry, type UnmatchedEntryPage} from '@core/fix-match.ts';
import {attentionReason, providerName} from '@core/server-admin/attention-words.ts';
import type {ServerRequester} from '@core/query/server-api.ts';
import {Button, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {Icon, type IconName} from '../../kit/Icon';
import {MetadataError} from './MetadataError';
import styles from './metadata.module.css';

const t = defaultI18n.t, has = (id: string) => defaultI18n.has(id);
const PROVIDERS = ['tmdb', 'tvdb', 'anilist'] as const;
/** The server's reasons, in the words the list already has for them. */
const REASONS: Readonly<Record<string, string>> = {no_close_match: 'unmatched', several_titles: 'needs_selection'};
/** The server's item kinds, as the list names them. */
const KINDS: Readonly<Record<string, 'film' | 'show' | 'episode' | 'album' | 'song' | 'book'>> = {movie: 'film', show: 'show', episode: 'episode', album: 'album', track: 'song', audiobook: 'book'};
const ICONS: Readonly<Record<string, IconName>> = {show: 'i-tv', episode: 'i-tv', album: 'i-music', track: 'i-music', artist: 'i-music', audiobook: 'i-book', book: 'i-book'};
type Finder = Readonly<{entry: UnmatchedEntry; mode: 'search' | 'id'}>;

/**
 * owner-metadata frames 2-4, on the rebuilt server (area 4 rules 12 to 17): the titles Portico
 * could not match with confidence, each with why and up to three closest matches. The owner can
 * use one, search by hand, enter an ID, look it up again, or keep it as it is.
 */
export function NeedsAttention({libraryId, libraryName, api, onOpen, onEdit}: {libraryId: string; libraryName?: string; api: ServerRequester;
  onOpen?: (entry: UnmatchedEntry) => void; onEdit?: (entry: UnmatchedEntry, anchor: HTMLElement) => void}) {
  const [page, setPage] = useState<UnmatchedEntryPage>(), [cursor, setCursor] = useState(''), [cursors, setCursors] = useState<readonly string[]>([]);
  const [loading, setLoading] = useState(true), [readError, setReadError] = useState<unknown>();
  const [busy, setBusy] = useState<string>(), [notice, setNotice] = useState<string>(), [error, setError] = useState<unknown>();
  const [finder, setFinder] = useState<Finder>(), [typed, setTyped] = useState(''), [provider, setProvider] = useState<(typeof PROVIDERS)[number]>('tmdb');
  const [found, setFound] = useState<readonly MatchCandidate[]>(), [finding, setFinding] = useState(false), [findError, setFindError] = useState<unknown>();
  const finderInput = useRef<HTMLInputElement>(null);
  const finderRef = useRef(finder); finderRef.current = finder;
  // Only the newest read of the list on screen may land: a read for another library or api, or a
  // Retry kept from before one, is dropped; a second click joins the read already under way.
  const generation = useRef(0), reading = useRef(false), active = useRef<unknown>(undefined);
  const load = useCallback(async (next: string) => {
    if (active.current !== load || reading.current) return;
    const mine = ++generation.current;
    reading.current = true; setLoading(true); setReadError(undefined); setCursor(next);
    try {
      const loaded = await readUnmatched(api, libraryId, next);
      if (mine === generation.current) setPage(loaded);
    } catch (value) {if (mine === generation.current) setReadError(value);}
    finally {if (mine === generation.current) {reading.current = false; setLoading(false);}}
  }, [api, libraryId]);
  useEffect(() => {
    active.current = load; reading.current = false; generation.current++;
    setPage(undefined); setCursors([]); setNotice(undefined); setError(undefined);
    void load('');
    return () => {active.current = undefined; generation.current++;};
  }, [load]);
  const act = async (entry: UnmatchedEntry, run: () => Promise<string>) => {
    if (busy) return;
    setBusy(entry.itemId); setNotice(undefined); setError(undefined);
    try {
      const done = await run();
      setNotice(done); setFinder(undefined);
      await load(cursor);
    } catch (value) {setError(value);}
    finally {setBusy(undefined);}
  };
  const use = (entry: UnmatchedEntry, candidate: MatchCandidate, order?: EpisodeOrder) => void act(entry, async () => {
    const result = await chooseMatch(api, entry.itemId, candidate, order);
    return [t('server.attention.matched', {title: candidate.title}), result.notice].filter(Boolean).join(' ');
  });
  const open = (entry: UnmatchedEntry, mode: Finder['mode']) => {
    setTyped(mode === 'search' ? entry.title : ''); setProvider('tmdb'); setFound(undefined); setFindError(undefined); setFinder({entry, mode});
  };
  const ref = finder?.mode === 'id' ? providerRef(typed, provider) : undefined;
  const ready = finder?.mode === 'id' ? !!ref : typed.trim().length >= 2;
  const find = async (event?: FormEvent) => {
    event?.preventDefault();
    if (!finder || !ready || finding) return;
    const asked = finder;
    setFinding(true); setFindError(undefined); setFound(undefined);
    try {
      const answer = await findMatches(api, asked.entry.itemId, asked.mode === 'id' ? {ref} : {query: typed});
      if (finderRef.current === asked) setFound(answer);
    } catch (value) {if (finderRef.current === asked) setFindError(value);}
    finally {setFinding(false);}
  };
  const older = () => {if (!loading && page?.nextCursor) {setCursors(list => [...list, cursor]); void load(page.nextCursor);}};
  const newer = () => {if (!loading && cursors.length) {const previous = cursors.at(-1)!; setCursors(list => list.slice(0, -1)); void load(previous);}};
  const entries = page?.items ?? [];
  const bare = finder?.mode === 'id' && /^\d+$/.test(typed.trim());
  return <section className={styles.attention}>
    {error ? <MetadataError error={error} /> : null}
    {readError ? <div role="group">
      <MetadataError error={readError} />
      <Button size="small" icon="i-retry" disabled={loading} loading={loading} onClick={() => void load(cursor)}>{t('next.browse.retry')}</Button>
    </div> : null}
    {notice ? <p className={styles.notice} role="status">{notice}</p> : null}
    <div className={styles.listBox}>{entries.map(entry => <Row key={entry.itemId} entry={entry} busy={busy === entry.itemId} disabled={!!busy}
      onUse={(candidate, order) => use(entry, candidate, order)}
      onFind={mode => open(entry, mode)}
      onRetry={() => void act(entry, async () => t(await lookUpAgain(api, entry.itemId) ? 'metadata.attention.lookedUpMatched' : 'metadata.attention.lookedUpUnmatched'))}
      onKeep={() => void act(entry, async () => {await keepAsIs(api, entry.itemId); return t('metadata.attention.kept');})}
      onOpen={onOpen ? () => onOpen(entry) : undefined} onEdit={onEdit ? anchor => onEdit(entry, anchor) : undefined} />)}</div>
    {!loading && !readError && page && !entries.length && !cursors.length ? <div className={styles.empty}>
      <span className={styles.emptyIcon}><Icon name="i-check" size={28}/></span><b>{t('metadata.attention.empty')}</b><p>{libraryName ? t('metadata.attention.emptyLibraryBody', {library: libraryName}) : t('metadata.attention.emptyBody')}</p>
    </div> : null}
    {(cursors.length > 0 || page?.nextCursor) ? <div className={styles.pager}>
      <Button size="small" disabled={loading || !cursors.length} onClick={newer}>{t('web.hostedAccount.newer')}</Button>
      <Button size="small" disabled={loading || !page?.nextCursor} onClick={older}>{t('web.hostedAccount.older')}</Button>
    </div> : null}
    <Dialog footerDismisses width={560} initialFocusRef={finderInput} bodyClassName={styles.dialogBody} open={Boolean(finder)} onOpenChange={value => {if (!value && !busy) setFinder(undefined);}}
      title={t(finder?.mode === 'id' ? 'metadata.attention.enterProviderId' : 'server.attention.searchTitle')} description={finder?.mode === 'id' ? finder.entry.title : t('server.attention.searchBody')}
      footer={<><Button size="small" disabled={Boolean(busy)} onClick={() => setFinder(undefined)}>{t('action.cancel')}</Button>
        <Button size="small" variant="primary" loading={finding} disabled={!ready || Boolean(busy)} onClick={() => void find()}><Icon name="i-search" size={18} />{t('metadata.attention.find')}</Button></>}>
      <form onSubmit={event => void find(event)}>
        {finder?.mode === 'id'
          ? <TextField ref={finderInput} label={t('server.attention.idLabel')} description={t('server.attention.idHelp')} value={typed} onChange={event => setTyped(event.currentTarget.value)} maxLength={2048} />
          : <TextField ref={finderInput} label={t('server.attention.searchFor')} value={typed} onChange={event => setTyped(event.currentTarget.value)} maxLength={200} />}
      </form>
      {bare ? <label className={styles.picker}>
        <span>{t('server.attention.idProvider')}</span>
        <select value={provider} onChange={event => setProvider(event.currentTarget.value as (typeof PROVIDERS)[number])}>
          {PROVIDERS.map(item => <option key={item} value={item}>{providerName(item)}</option>)}
        </select>
      </label> : null}
      {findError ? <MetadataError error={findError} /> : null}
      {found && !found.length ? <p className={styles.working}>{t('metadata.attention.noMatchesFound')}</p> : null}
      {finder && found?.length ? <div className={styles.matchResults}>{found.map(candidate =>
        <Candidate key={candidate.provider + ':' + candidate.id} candidate={candidate} show={finder.entry.kind === 'show'} disabled={!!busy} onUse={order => use(finder.entry, candidate, order)} />)}</div> : null}
    </Dialog>
  </section>;
}

function Row({entry, busy, disabled, onUse, onFind, onRetry, onKeep, onOpen, onEdit}: {
  entry: UnmatchedEntry; busy: boolean; disabled: boolean; onUse: (candidate: MatchCandidate, order?: EpisodeOrder) => void;
  onFind: (mode: Finder['mode']) => void; onRetry: () => void; onKeep: () => void; onOpen?: () => void; onEdit?: (anchor: HTMLElement) => void;
}) {
  const kind = KINDS[entry.kind];
  const title = entry.title || t('server.attention.untitled');
  const matches = entry.matches ?? [];
  return <article className={styles.row} data-busy={busy || undefined}>
    <header className={styles.rowHead}>
      <span className={styles.kindTile} aria-hidden="true"><Icon name={ICONS[entry.kind] ?? 'i-film'} size={24} /></span>
      <div><h3>{onEdit ? <button type="button" disabled={disabled} onClick={event => onEdit(event.currentTarget)}>{title}</button> : onOpen ? <button type="button" onClick={onOpen}>{title}</button> : title}
        {kind ? <span className={styles.kindChip}>{t(`metadata.attention.kind.${kind}`)}</span> : null}</h3>
      {entry.year ? <small>{entry.year}</small> : null}
      <p>{attentionReason(t, has, REASONS[entry.reason] ?? entry.reason)}</p>
      </div>
      <button className={styles.textAction} type="button" disabled={disabled} onClick={onKeep}>{t('metadata.attention.keepAsIs')}</button>
    </header>
    {busy ? <p className={styles.working}>{t('metadata.attention.rowMatching')}</p> : <>
      {matches.map(candidate => <Candidate key={candidate.provider + ':' + candidate.id} candidate={candidate} show={entry.kind === 'show'} disabled={disabled} onUse={order => onUse(candidate, order)} />)}
      <div className={styles.actions}>
        <span>{t(matches.length ? 'metadata.attention.notThese' : 'metadata.attention.lookYourself')}</span>
        <button className={styles.textAction} type="button" disabled={disabled} onClick={() => onFind('search')}><Icon name="i-search" size={16} />{t('metadata.attention.searchByHand')}</button>
        <button className={styles.textAction} type="button" disabled={disabled} onClick={() => onFind('id')}><Icon name="i-link" size={16} />{t('metadata.attention.enterProviderId')}</button>
        {!matches.length ? <button className={styles.textAction} type="button" disabled={disabled} onClick={onRetry}><Icon name="i-refresh" size={16} />{t('server.attention.lookUpAgain')}</button> : null}
      </div>
    </>}
  </article>;
}

/** One match: its title, year and provider; a show's carries the episode order its files are numbered in (rule 15). */
function Candidate({candidate, show, disabled, onUse}: {candidate: MatchCandidate; show: boolean; disabled: boolean; onUse: (order?: EpisodeOrder) => void}) {
  const [order, setOrder] = useState<EpisodeOrder>('aired');
  return <div className={styles.candidate}>
    <span className={styles.candidatePoster} aria-hidden="true"><i>{Array.from(candidate.title.trim())[0] ?? ''}</i></span>
    <div><div className={styles.candidateTitle}><b>{candidate.title}</b>{candidate.year ? <span className={styles.candidateYear}>{candidate.year}</span> : null}<span className={styles.provider}>{providerName(candidate.provider)}</span></div>
      {candidate.originalTitle && candidate.originalTitle !== candidate.title ? <small>{candidate.originalTitle}</small> : null}
      {show ? <label className={styles.order}>{t('metadata.attention.episodeOrder')}
        <select value={order} disabled={disabled} onChange={event => setOrder(event.currentTarget.value as EpisodeOrder)}>
          {EPISODE_ORDERS.map(item => <option key={item} value={item}>{t(`metadata.attention.order.${item}`)}</option>)}
        </select></label> : null}</div>
    <Button size="small" disabled={disabled} onClick={() => onUse(show ? order : undefined)}>{t('metadata.attention.useMatch')}</Button>
  </div>;
}
