import {useScreenIdentity} from '../detail/context';
import {generatedCoverTint} from '@design';
import {useCardInteractionAdmission} from '../../kit/cards/use-interaction-admission';
import type {SearchSelection} from './selection';
import {SelectionCircle} from '../../kit/selection';
import {t, locale} from '../detail/text';
import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type MouseEvent as ReactMouseEvent} from 'react';
import {preferenceValue} from '@core/preferences.ts';
import {searchQueryProblem, type SearchEntry, type SearchGroup, type SearchGroupResult, type SearchHistoryPage} from '@core/search.ts';
import {SEARCH_CHIPS, searchGroupLabel, topSearchResult} from '@core/presentation/search-model.ts';
import {Button, Chip, TextField} from '../../kit/controls';
import {useSelectionRangePreview, PosterCard, SquareCard, CircleCard, EpisodeTile, GenreTile, LiveCard, CoverCard} from '../../kit/cards';
import {Artwork} from '../../kit/artwork';
import {WindowedGrid} from '../../kit/layout';
import {Skeleton} from '../../kit/states';
import {Banner} from '../../kit/overlays';
import {SearchResultStatus} from './SearchResultStatus';
import {searchResultCount} from './result-status';
import {SearchEmpty} from '../../kit/states/SearchEmpty';
import {entryCard} from '../detail/model';
import {dependsOn, titlesWithPersonalState} from '@core/freshness-hub.ts';
import {useFreshDocument} from '../../platform/freshness';
import {Icon} from '../../kit/Icon';
import type {SearchBindings} from './bindings';
import type {SearchOnNowCard} from './on-now';
import {appendedWindow} from './window';
import styles from './search.module.css';
import {TopResult} from './TopResult';
import {artistAlbumArt} from './artist-art';
import {runScreenCommand} from '../detail/context';
import {useLoadingClock} from '../detail/Loading';
import {ScreenProblem} from '../detail/ScreenProblem';
import {useShellWidth} from '../../shell/viewport';
import {RecentSearches} from './RecentSearches';
import {nowNext} from '@core/guide/index.ts';
import {channelTint, type FactPart} from '@portico/client-core/kit-model';
import {clockText, durationText} from '@core/presentation/kit-model/text.ts';
import {defaultI18n} from '@i18n';
import {formatSignalText, musicFormatBadges} from '@core/music-signal.ts';
import {AllResultsRow} from './AllResultsRow';

type ChipId = SearchGroup | 'all';
type HistoryLease = {retired: boolean; pending: boolean};
type HistoryView = {lease: HistoryLease; page?: SearchHistoryPage};
const keyOf = (entry: SearchEntry) => JSON.stringify(['libraryId' in entry ? entry.libraryId : '', entry.kind, entry.id]);
const groupOrder = new Map<string, number>(SEARCH_CHIPS.map((chip, index) => [chip.id, index]));
const searchTimezone = () => defaultI18n.region.timeZone ?? Intl.DateTimeFormat().resolvedOptions().timeZone;
const onNowTime = (at: number) => new Intl.DateTimeFormat(locale(), {hour: 'numeric', minute: '2-digit', timeZone: searchTimezone()}).format(new Date(at));
function onNowProgramme(card: SearchOnNowCard, now: number) {
  const current = nowNext(card.programmes, now), programme = current.now;
  if (!programme) return {title: t('guide.noInformation'), facts: [] as readonly FactPart[]};
  const facts: FactPart[] = [];
  if (programme.subtitle) facts.push({id: 'programme-subtitle', role: 'context', text: programme.subtitle});
  facts.push({id: 'ends', role: 'context', text: t('guide.ends', {time: onNowTime(programme.end)})});
  return {title: programme.title, facts, progress: current.progress, live: programme.flags?.live === true};
}
export type SearchScreenProps = {binding: SearchBindings; selection?: SearchSelection; linkedQuery?: string; onConsumeLinkedQuery?: () => void; onOpenLive?: (sourceId?: string) => void};

export function SearchScreen({binding, selection, linkedQuery, onConsumeLinkedQuery, onOpenLive}: SearchScreenProps) {
  useSyncExternalStore(selection?.subscribe ?? noSelectionSubscription, selection?.getSnapshot ?? noSelectionSnapshot, selection?.getSnapshot ?? noSelectionSnapshot);
  const selectionEpoch = selection?.epoch();
  const compact = useShellWidth() < 600;
  const {service, context} = binding, initial = binding.session();
  useScreenIdentity(context);
  const snapshot = useSyncExternalStore(binding.results.subscribe, binding.results.getSnapshot, binding.results.getSnapshot);
  useFreshDocument([dependsOn.libraries, ...titlesWithPersonalState(), dependsOn.settings('preferences'), dependsOn.event('stream.resync')], refresh => service.revalidate(refresh), () => service.getSnapshot().phase === 'ready');
  const preferences = useSyncExternalStore(context.preferences.subscribe, context.preferences.getSnapshot, context.preferences.getSnapshot);
  const [text, setText] = useState(linkedQuery ?? initial.text), [chip, setChip] = useState<ChipId>(initial.group ?? 'all');
  const [historyView, setHistoryView] = useState<HistoryView>(), [clearFailed, setClearFailed] = useState(false);
  // The history row the viewer is looking at belongs to this exact mount (or replay) and
  // to the page it was rendered from; the rendered callbacks close over that state, and a
  // retained one cannot write into a successor.
  const activeLease = useRef<HistoryLease | undefined>(undefined), historyPage = useRef<SearchHistoryPage | undefined>(undefined);
  const recent = historyView?.page ? historyView.page.entries.slice(0, 8).map(item => item.q) : [];
  const [now, setNow] = useState(() => Date.now());
  const genres = useSyncExternalStore(binding.genres.subscribe, binding.genres.getSnapshot, binding.genres.getSnapshot);
  const onNow = useSyncExternalStore(binding.onNow.subscribe, binding.onNow.getSnapshot, binding.onNow.getSnapshot);
  const input = useRef<HTMLInputElement>(null), results = useRef<HTMLDivElement>(null), committed = useRef('');
  const group = chip === 'all' ? undefined : chip, problem = searchQueryProblem(text);
  const remember = preferences.effective['search.rememberHistory'] === true;
  const submit = (record = false) => {
    if (!context.isCurrent()) return;
    void binding.select(problem ? '' : text.trim(), {...(group ? {group} : {}), ...(record && remember ? {record: true} : {})}).catch(context.effects.reportError);
  };
  useEffect(() => { if (linkedQuery !== undefined) onConsumeLinkedQuery?.(); }, [linkedQuery, onConsumeLinkedQuery]);
  useEffect(() => {
    if (snapshot.query?.q === text.trim() && snapshot.query?.group === group) return;
    // Withdraw the old query immediately, including during the debounce.
    service.cancel();
    const timer = setTimeout(() => { void binding.select(problem ? '' : text.trim(), group ? {group} : {}).catch(context.effects.reportError); }, 260);
    return () => clearTimeout(timer);
  }, [binding, service, text, group, problem, context.effects.reportError, snapshot.query?.q, snapshot.query?.group]);
  useEffect(() => { input.current?.focus(); }, [binding]);
  useLayoutEffect(() => {
    const lease: HistoryLease = {retired: false, pending: false};
    activeLease.current = lease;
    historyPage.current = undefined;
    setHistoryView({lease});
    return () => {
      lease.retired = true;
      if (activeLease.current === lease) activeLease.current = undefined;
    };
  }, [binding]);
  useEffect(() => {
    const lease = historyView?.lease;
    if (!lease) return;
    if (!remember) {
      historyPage.current = undefined;
      setHistoryView(current => current && current.lease === lease && current.page ? {lease} : current);
      return;
    }
    const controller = new AbortController();
    let active = true;
    void binding.recent(controller.signal).then(page => {
      if (!active || !context.isCurrent() || lease.retired || lease.pending || activeLease.current !== lease) return;
      historyPage.current = page;
      setHistoryView(current => current && current.lease === lease ? {lease, page} : current);
    }, () => {});
    return () => {
      active = false;
      controller.abort();
    };
  }, [binding, context, remember, historyView?.lease]);
  useEffect(() => {
    if (text.trim() || context.identity?.liveAvailable !== true) return;
    if (binding.onNow.getSnapshot().phase === 'idle') void binding.onNow.load().catch(() => {});
  }, [binding, context.identity?.liveAvailable, text]);
  useEffect(() => {
    if (text.trim()) return;
    const timer = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(timer);
  }, [text]);
  const commit = () => { if (remember && text.trim() && !problem && committed.current !== text.trim()) { committed.current = text.trim(); submit(true); } };
  // The rendered callbacks carry their own lease and page from state. Before any write they
  // compare the live lease and page refs with those exact objects, so a retained callback
  // never adopts a successor. One synchronous pending flag on that lease serialises remove
  // and clear; only the server's answer for the same lease and page replaces the list.
  const historyNow = historyView?.page ? {lease: historyView.lease, page: historyView.page} : undefined;
  const removeRecent = historyNow ? (query: string) => {
    const {lease, page} = historyNow;
    if (!context.isCurrent() || lease.retired || lease.pending || activeLease.current !== lease || historyPage.current !== page || !recent.includes(query)) return;
    lease.pending = true;
    try {
      void binding.removeRecent(query).then(next => {
        lease.pending = false;
        if (!context.isCurrent() || lease.retired || activeLease.current !== lease || historyPage.current !== page) return;
        historyPage.current = next;
        setHistoryView(current => current && current.lease === lease ? {lease, page: next} : current);
        setClearFailed(false);
      }, () => {
        lease.pending = false;
        if (!context.isCurrent() || lease.retired || activeLease.current !== lease || historyPage.current !== page) return;
        setClearFailed(true);
      });
    } catch (error) { lease.pending = false; context.effects.reportError(error); }
  } : undefined;
  const clearRecentHistory = historyNow ? () => {
    const {lease, page} = historyNow;
    if (!context.isCurrent() || lease.retired || lease.pending || activeLease.current !== lease || historyPage.current !== page) return;
    lease.pending = true;
    try {
      void binding.clearRecent().then(() => {
        lease.pending = false;
        if (!context.isCurrent() || lease.retired || activeLease.current !== lease || historyPage.current !== page) return;
        historyPage.current = undefined;
        setHistoryView(current => current && current.lease === lease ? {lease} : current);
        setClearFailed(false);
      }, () => {
        lease.pending = false;
        if (!context.isCurrent() || lease.retired || activeLease.current !== lease || historyPage.current !== page) return;
        setClearFailed(true);
      });
    } catch (error) { lease.pending = false; context.effects.reportError(error); }
  } : undefined;
  const showing = snapshot.data?.query.q === text.trim() && snapshot.data.query.group === (group ?? '') ? snapshot.data : null;
  const groups = showing ? snapshot.groups : [], top = showing && !group && !selection?.isActive() ? topSearchResult(text, service.getSnapshot().groups) : undefined;
  // Frame 4: a photo-less artist's circle is cut from one of their own albums in this answer.
  const topAlbumArt = top ? artistAlbumArt(top.entry, groups) : undefined;
  useLayoutEffect(() => {if (selection && top?.entry && 'libraryId' in top.entry) return selection.register(top.entry, -1);}, [selection, top?.entry, selectionEpoch, snapshot.generation]);
  const failed = groups.filter(value => value.status === 'error');
  const size = preferenceValue<number>(preferences, 'appearance.cardSizePercent') / 100;
  const empty = showing && groups.every(value => value.status === 'success' && !value.items.length && !value.nextCursor);
  // A filtered/unreadable page can contain no drawable entries while still offering
  // a real continuation. Keep its pending row in the shared authored chip order.
  const allRows = groups.filter(value => value.items.length > 0 || value.status === 'success' && !!value.nextCursor)
    .sort((left, right) => (groupOrder.get(left.id) ?? 99) - (groupOrder.get(right.id) ?? 99));
  // The frame's "Browse instead" is the same genre browsing as the empty page, so the
  // tiles load for an empty answer too.
  useEffect(() => {
    if (text.trim() && !empty) return;
    if (binding.genres.getSnapshot().phase === 'idle') void binding.genres.load().catch(() => {});
  }, [binding, text, empty]);
  const recentBlock = <RecentSearches queries={recent} compact={compact} onRemove={removeRecent}
    onSelect={query => {if (context.isCurrent()) {setText(query);setChip('all');committed.current = '';}}}
    onClear={() => {clearRecentHistory?.();}}/>;
  const videoKinds = new Intl.ListFormat(locale(), {style: 'long', type: 'conjunction'}).format([
    t('web.libraryKind.plural', {kind: 'movie'}), t('web.libraryKind.plural', {kind: 'tv'}), t('web.libraryKind.plural', {kind: 'anime'})]);
  const liveSourceCount = new Set(onNow.cards.map(card => card.channel.sourceId)).size;
  const genreTiles = <div className={styles.genres}>{genres.genres.flatMap((genre, index) => {
    const art = genre.backdrop ? context.artwork.foreground(genre.backdrop.path, 632, 16 / 9, {group: 'search-genres', order: index}) : undefined;
    if (!art && !genres.all) return [];
    return [<GenreTile key={genre.value} variant="video" context="search" artwork={art} visible
      model={{title: genre.label, caption: [{id: 'count', role: 'supplement', text: t('next.selection.titles', {count: genre.count})}]}}
      onNavigate={() => runScreenCommand(context, () => {const browse = context.effects.openBrowse;if (browse) browse(binding.genres.browseTarget(genre));})} />];
  })}</div>;
  const allGenres = !genres.all && <Button variant="text" size="small" icon="i-right" iconSize={18} className={`${styles.trailingLink} ${styles.allGenresLink}`}
    onClick={() => runScreenCommand(context, () => binding.genres.showAll(genres))}>{t('next.search.allGenres')}</Button>;
  const nextGenres = genres.all && genres.canContinue && <Button variant="glass" size="small" onClick={() => runScreenCommand(context, () => {
    if (binding.genres.getSnapshot() === genres) return binding.genres.next();
  })}>{t('web.pager.next')}</Button>;
  const genreSection = genres.phase === 'ready' && (genres.hasGenres || genres.genres.length > 0 || genres.canContinue) && <section className={styles.emptySection} aria-labelledby="search-genres">
    <header><h2 id="search-genres">{t('next.search.browseByGenre')}</h2><span>{videoKinds}</span>{allGenres}</header>{genreTiles}{nextGenres}</section>;
  // Frame 5: a no-match answer keeps the same genre browsing, so it is not a dead end.
  const browseInstead = genres.phase === 'ready' && (genres.hasGenres || genres.genres.length > 0 || genres.canContinue) && <section className={styles.emptySection} aria-labelledby="search-browse-instead">
    <header><h2 id="search-browse-instead">{t('next.search.browseInstead')}</h2>{allGenres}</header>{genreTiles}{nextGenres}</section>;
  const beforeTyping = <div className={styles.empty}>
    {recentBlock}
    {genreSection}
    {context.identity?.liveAvailable === true && onNow.phase === 'ready' && onNow.cards.length > 0 && <section className={styles.emptySection} aria-labelledby="search-on-now">
      <header><h2 id="search-on-now">{t('next.home.onNow')}</h2><span>{onNow.sourceNames.length ? onNow.sourceNames.join(', ') : t('next.live.sources', {count: liveSourceCount})}</span>
        <Button variant="text" size="small" className={styles.trailingLink} icon="i-right" disabled={!onOpenLive} onClick={() => onOpenLive?.()}>{t('next.controls.guide')}</Button></header>
      <div className={styles.lives}>{onNow.cards.map(card => <LiveCard key={card.channel.sourceId + ':' + card.channel.id}
        channel={{sourceId: card.channel.sourceId, id: card.channel.id, name: card.channel.name, ...(card.channel.number !== undefined ? {number: card.channel.number} : {}),
          ...(card.channel.mark ? {mark: {...card.channel.mark, sourceId: card.channel.sourceId, id: card.channel.id}} : {})}}
        tint={card.channel.mark ? channelTint(card.channel.mark.seed) : undefined}
        logo={card.channel.logoUrl ? context.artwork.foreground(card.channel.logoUrl, 68, 1) : undefined}
        programme={onNowProgramme(card, now)} onNavigate={() => onOpenLive?.(card.channel.sourceId)} />)}</div>
    </section>}
  </div>;
  return <div className={styles.search} style={{'--search-card-scale': size, '--search-columns': Math.max(1, Math.round(7 / size))} as React.CSSProperties}>
    {/* Phone-app 7 draws the Search title only before typing; frame 4's Cancel belongs to the
        results state, so it is absent on the empty one. */}
    {compact && !text.trim() && <h1 className={styles.phoneSearchTitle}>{t('web.search.title')}</h1>}
    <div className={styles.sbar}>
      <form className={styles.field} role="search" onSubmit={event => { event.preventDefault(); submit(true); }}>
        <TextField ref={input} variant={compact ? 'search' : 'large-search'} type="search" autoComplete="off" spellCheck={false} value={text}
          aria-label={t('web.search.title')} placeholder={t('next.controls.searchLibraries')}
          onChange={event => { setText(event.currentTarget.value); setChip('all'); committed.current = ''; }}
          onKeyDown={event => { if (event.key === 'ArrowDown') { event.preventDefault(); results.current?.querySelector<HTMLElement>('button:not(:disabled),a[href]')?.focus(); } }}
          trailing={text ? <Button variant="icon" icon="i-x" iconOnly aria-label={t('web.search.clear')} onClick={() => { setText(''); setChip('all'); input.current?.focus(); }} />
            : <span className={styles.hint}><kbd>{t('next.controls.searchShortcut')}</kbd>{t('next.controls.searchFromAnywhere')}</span>} />
      </form>
      {compact && text.trim() && context.effects.openHome && <Button variant="text" size="small" className={styles.cancel}
        onClick={() => runScreenCommand(context, () => { context.effects.openHome?.(); })}>{t('action.cancel')}</Button>}
    </div>
    <SearchResultStatus identity={`${snapshot.generation}:${text.trim()}:${group ?? ''}`} count={snapshot.phase === 'ready' && showing ? searchResultCount(groups) : undefined}/>
    {selection?.isActive() ? selection.toolbar : text.trim() && !(empty && !group) && <div className={styles.chips} role="group" aria-label={t('web.search.title')}>{SEARCH_CHIPS.map(value => {
      const capability = showing?.capabilities.groups.find(item => item.id === value.id), result = groups.find(item => item.id === value.id) ?? binding.summary().find(item => item.id === value.id);
      if (value.id !== 'all' && capability?.available === false) return null;
      if (value.id === 'reading' && capability?.available !== true) return null;
      const off = value.id !== 'all' && result?.status === 'success' && result.totalCountExact && result.totalCount === 0 && !('nextCursor' in result && result.nextCursor);
      return <Chip key={value.id} variant="search" selected={chip === value.id} dim={off} disabled={off} retry={result?.status === 'error'}
        count={!off && result?.totalCountExact ? result.totalCount : undefined} onClick={() => { setChip(value.id); }}>{t(value.label)}</Chip>;
    })}</div>}
    <div ref={results} className={styles.results}>
      {!text.trim() && beforeTyping}
      {clearFailed && <p role="alert">{t('web.search.clearFailed')}</p>}
      {problem && <p role="status">{t(problem === 'short' ? 'web.search.tooShort' : problem === 'words' ? 'web.search.tooManyWords' : 'web.search.tooLong')}</p>}
      {snapshot.phase === 'loading' && !showing && <SearchLoading key={snapshot.generation} />}
      {(snapshot.phase === 'error' || snapshot.phase === 'refresh-required') && <ScreenProblem embedded context={context} error={snapshot.error} retry={() => { if (binding.results.getSnapshot() === snapshot) void binding.results.retry().catch(context.effects.reportError); }} />}
      {failed.length > 0 && <div role="status" className={styles.partial}><Icon name="i-clock" size={22} /><span>{t('web.search.partial', {groups: new Intl.ListFormat(locale(), {style: 'long', type: 'conjunction'}).format(failed.map(value => t(searchGroupLabel(value.id))))})}</span><Button icon="i-retry" size="small" onClick={() => { void service.refresh(); }}>{t('next.states.tryAgain')}</Button></div>}
      {empty && <SearchEmpty title={t(group ? 'web.search.nothingIn' : 'web.search.noMatches', group ? {group: t(searchGroupLabel(group))} : {query: text.trim()})} body={group ? undefined : t('web.search.noMatchesBody')}
        action={group ? <Button variant="primary" size="compact" onClick={() => setChip('all')}>{t('web.search.everything')}</Button> : undefined} />}
      {empty && !group && browseInstead}
      {top && <TopResult binding={binding} result={top} fallbackArtwork={topAlbumArt} compact={compact}
        onOpen={() => runScreenCommand(context, () => { if (!top) return; binding.open(top.entry); commit(); })} />}
      {group && !empty ? <SearchGroupWindow binding={binding} group={group} result={groups.find(value => value.id === group)} selection={selection} onCommit={commit} />
        : allRows.map((result, groupIndex, ordered) => {
          const offset = ordered.slice(0, groupIndex).reduce((sum, value) => sum + value.items.length, 0);
          return <AllResultsRow key={`${snapshot.generation}:${result.id}`} result={result} binding={binding} selection={selection} excluded={top?.entry} size={size} onSeeAll={() => setChip(result.id)}
            renderEntry={(entry, position, selectionVisible) => <SearchResult key={keyOf(entry)} entry={entry} binding={binding} selection={selection} selectionVisible={selectionVisible} position={offset + position} onCommit={commit}
              albumArt={entry.kind === 'artist' ? artistAlbumArt(entry, groups) : undefined} />} />;
        })}
    </div>
  </div>;
}

function SearchLoading() {
  const elapsed = useLoadingClock();
  return <div className={styles.skeleton} role="status" aria-label={t('next.states.loading')}>{elapsed >= 300 && Array.from({length: 7}, (_, index) => <Skeleton className={styles.skeletonBlock} sheen={index === 2} key={index} />)}{elapsed >= 8000 && <Banner variant="quiet">{t('next.states.stillLoading')}</Banner>}</div>;
}

const noSelectionSubscription = () => () => {};
const noSelectionSnapshot = () => 0;
export function SearchResult({entry, binding, selection, selectionVisible = true, position = 0, onCommit, albumArt}: {entry: SearchEntry; binding: SearchBindings; selection?: SearchSelection; selectionVisible?: boolean; position?: number; onCommit: () => void; albumArt?: string}) {
  useSyncExternalStore(selection?.subscribe ?? noSelectionSubscription, selection?.getSnapshot ?? noSelectionSnapshot, selection?.getSnapshot ?? noSelectionSnapshot);
  const epoch = selection?.epoch();
  useLayoutEffect(() => {if (selection && selectionVisible && 'libraryId' in entry) return selection.register(entry, position);}, [selection, entry, position, epoch, selectionVisible]);
  const interactionRoot = useRef<HTMLDivElement>(null);
  const picked = useCardInteractionAdmission(selectionVisible && 'libraryId' in entry ? selection?.card(entry) : undefined, interactionRoot, !selectionVisible);
  const range = useSelectionRangePreview(picked);
  const {context} = binding, open = () => runScreenCommand(context, () => { binding.open(entry); onCommit(); });
  const liveCards = useSyncExternalStore(binding.live.subscribe, binding.live.getSnapshot, binding.live.getSnapshot);
  const art = context.artwork.foreground(entry.posterUrl, 380, 2 / 3);
  if (entry.kind === 'channel') {
    const live = liveCards.get(entry.id);
    if (live) return <LiveCard className={styles.liveCard} context="search" channel={live.channel}
      programme={live.programme} tint={live.tint} results onNavigate={open} logo={live.channel.logoUrl ? context.artwork.foreground(live.channel.logoUrl, 68, 1) : undefined} />;
    return <button className={styles.channel} type="button" onClick={open}><Icon name="i-tv" size={24} /><b>{entry.title}</b>{entry.subtitle && <small>{entry.subtitle}</small>}</button>;
  }
  if (!('libraryId' in entry)) return <CircleCard context="search" model={{title: entry.title, caption: entry.subtitle ? [{id: 'caption', text: entry.subtitle, role: 'context'}] : []}} covers={art ? [art] : []}
    initials={entry.title.split(/\s+/).filter(Boolean).slice(0, 2).map(word => Array.from(word)[0]).join('')} onNavigate={open} />;
  const model = entryCard(entry, {t, locale: locale()}, 'search'), playable = entry.available === true && !!entry.playback;
  const props = {model, selection: picked, context: 'search' as const, watched: entry.watched, isNew: entry.new, onNavigate: open,
    onPlay: playable ? () => { void binding.play(entry).then(onCommit).catch(context.effects.reportError); } : undefined};
  if (entry.kind === 'song') return <div ref={interactionRoot} className={styles.songSelection} data-selected={picked?.checked || undefined} data-preview={picked?.preview || undefined} {...range}>{picked && <SelectionCircle variant="row" selected={picked.checked} selecting={picked.active} accessibilityLabel={t('next.selection.select')} onClick={event => {event.preventDefault(); picked.onToggle({range: event.shiftKey, source: event.detail === 0 ? 'keyboard' : 'pointer'});}}/>}<button className={styles.song} type="button" onClick={event => {if (picked?.active) picked.onToggle({range: event.shiftKey, source: event.detail === 0 ? 'keyboard' : 'pointer'}); else if (playable) void binding.play(entry).then(onCommit).catch(context.effects.reportError); else open();}}><Artwork source={context.artwork.foreground(entry.posterUrl, 88, 1)} alt="" /><span><b>{entry.displayTitle ?? entry.title}</b><small>{entry.artist?.name}{entry.album?.name && <span>{entry.album.name}</span>}</small></span><span>{clockText(entry.duration)}</span></button></div>;
  if (entry.kind === 'episode') return <EpisodeTile {...props} length={durationText(entry.duration, t)} artwork={context.artwork.foreground(entry.stillUrl ?? entry.backdropUrl, 632, 16 / 9)} artworkKind={entry.stillUrl ? 'still' : 'backdrop'} description={entry.overview} />;
  if (entry.kind === 'artist') {
    const cover = entry.posterUrl ? art : albumArt ? context.artwork.foreground(albumArt, 380, 1) : undefined;
    return <CircleCard {...props} covers={cover ? [cover] : []} variant="artist" />;
  }
  if (entry.kind === 'ebook' || entry.kind === 'comic') {
    const kind = entry.publicationType === 'magazine' || entry.publicationType === 'newspaper' ? entry.publicationType : entry.kind === 'comic' ? 'comic' : 'book';
    const caption: readonly FactPart[] = entry.author ? [{id: 'author', text: entry.author, role: 'context'}] : [];
    return <CoverCard {...props} model={{...model, caption}} kind={kind} artwork={context.artwork.foreground(entry.posterUrl, 300, kind === 'magazine' || kind === 'newspaper' ? 3 / 4 : 2 / 3)}
      generated={entry.posterUrl ? undefined : {title: entry.title, author: entry.author, glyph: Array.from(entry.title)[0] ?? '', tint: generatedCoverTint(entry.title)}} />;
  }
  if (['album', 'book', 'audiobook_file'].includes(entry.kind)) return <SquareCard {...props} artwork={context.artwork.foreground(entry.posterUrl, 300, 1)} />;
  return <PosterCard {...props} artwork={art} progressPlacement="inset" />;
}

/** Desktop-search frame 3: a full-width Songs answer is the Music library's list, with
 * the entry's own artist, album, plays and length. Selection keeps the same row model. */
export function SongTableRow({entry, binding, selection, position = 0, onCommit}: {entry: SearchEntry; binding: SearchBindings; selection?: SearchSelection; position?: number; onCommit: () => void}) {
  useSyncExternalStore(selection?.subscribe ?? noSelectionSubscription, selection?.getSnapshot ?? noSelectionSnapshot, selection?.getSnapshot ?? noSelectionSnapshot);
  const epoch = selection?.epoch();
  // A search answer can also carry a person or a channel, which have no library entry:
  // the row's selection and content helpers only ever receive a real song entry.
  const song = 'libraryId' in entry && entry.kind === 'song' ? entry : undefined;
  useEffect(() => {if (selection && song) return selection.register(song, position);}, [selection, song, position, epoch]);
  const interactionRoot = useRef<HTMLDivElement>(null);
  const picked = useCardInteractionAdmission(song ? selection?.card(song) : undefined, interactionRoot), range = useSelectionRangePreview(picked);
  if (!song) return null;
  const {context} = binding, open = () => runScreenCommand(context, () => { binding.open(song); onCommit(); });
  const playable = song.available === true && !!song.playback, title = song.displayTitle ?? song.title;
  const activate = (event: ReactMouseEvent) => {
    if (picked?.active) picked.onToggle({range: event.shiftKey, source: event.detail === 0 ? 'keyboard' : 'pointer'});
    else if (playable) void binding.play(song).then(onCommit).catch(context.effects.reportError);
    else open();
  };
  return <div ref={interactionRoot} className={styles.songTableRow} data-selected={picked?.checked || undefined} data-preview={picked?.preview || undefined} {...range}>
    <span className={styles.songIndex}>{picked ? <SelectionCircle variant="row" selected={picked.checked} selecting={picked.active} accessibilityLabel={t('next.selection.select')} onClick={event => {event.preventDefault(); picked.onToggle({range: event.shiftKey, source: event.detail === 0 ? 'keyboard' : 'pointer'});}}/>
      : <button type="button" className={styles.songIndexPlay} onClick={activate} aria-label={t('next.browse.play', {title})}><span>{defaultI18n.number(position + 1)}</span><Icon name="i-play" size={16}/></button>}</span>
    <button type="button" className={styles.songTitleCell} onClick={activate} aria-label={title}><Artwork source={context.artwork.foreground(song.posterUrl, 88, 1)} alt="" />
      <span className={styles.songTitleText}><b>{title}</b>
        {/* music-plus f8: a list that mixes albums carries the song's own notable formats after
            its title, at most two; the title shortens first and a badge never does. */}
        {song.audioFacts && musicFormatBadges(song.audioFacts).map(badge => <span key={typeof badge === 'string' ? badge : formatSignalText(badge, t)} className={styles.songBadge}>{typeof badge === 'string' ? badge : formatSignalText(badge, t)}</span>)}
      </span></button>
    <span className={styles.songArtist}>{song.artist?.name}</span>
    <span className={styles.songAlbum}>{song.album?.name}</span>
    <span className={styles.songPlays}>{song.plays === undefined ? undefined : defaultI18n.number(song.plays)}</span>
    <span className={styles.songLength}>{clockText(song.duration)}</span>
  </div>;
}

function SearchGroupWindow({binding, group, result, selection, onCommit}: {binding: SearchBindings; selection?: SearchSelection; group: SearchGroup; result?: SearchGroupResult; onCommit: () => void}) {
  const window = useMemo(() => appendedWindow<SearchEntry>(keyOf), []);
  const entries = useRef<readonly SearchEntry[]>([]), key = useRef(''), [, redraw] = useState(0);
  const snapshot = binding.service.getSnapshot(), queryKey = JSON.stringify([snapshot.query?.q, group, snapshot.data?.scope.viewerFence, snapshot.data?.revision]);
  useEffect(() => {
    if (!result || snapshot.phase !== 'ready') return;
    if (key.current !== queryKey || !snapshot.pagination.cursor) entries.current = [];
    key.current = queryKey;
    const seen = new Set(entries.current.map(keyOf));
    entries.current = [...entries.current, ...result.items.filter(value => !seen.has(keyOf(value)))];
    window.set(entries.current); redraw(value => value + 1);
  }, [window, result, queryKey, snapshot.phase, snapshot.pagination.cursor]);
  useEffect(() => () => window.dispose(), [window]);
  const songs = group === 'songs';
  return <section className={styles.single}><h2>{t(searchGroupLabel(group))}{songs && result?.totalCountExact && <span className={styles.resultTotal}>{t('web.search.results', {count: result.totalCount})}</span>}</h2>
    {songs && <div className={styles.songColumns} aria-hidden="true"><span>{t('next.browse.number')}</span><span>{t('next.browse.title')}</span><span>{t('next.browse.artist')}</span><span>{t('next.browse.album')}</span><span>{t('next.browse.plays')}</span><span>{t('next.browse.duration')}</span></div>}
    <WindowedGrid collection={window.collection} label={t(searchGroupLabel(group))}
    cardWidth={group === 'songs' ? 10000 : group === 'episodes' ? 316 : 174} rowHeight={group === 'songs' ? 58 : group === 'episodes' ? 285 : 320} height="calc(100vh - 270px)" gap={16}
    renderItem={(entry, item) => songs ? <SongTableRow entry={entry} binding={binding} selection={selection} position={item.index} onCommit={onCommit} /> : <SearchResult entry={entry} binding={binding} selection={selection} position={item.index} onCommit={onCommit} />} renderPlaceholder={index => <span className={styles.placeholder} key={index} />}
    onVisibleRange={(_, last) => { if (last >= entries.current.length - 20 && result?.nextCursor && binding.service.getSnapshot().phase === 'ready') void binding.service.next(group); }} /></section>;
}
