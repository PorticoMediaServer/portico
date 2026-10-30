import {CatalogueSelectionEntry, useCatalogueSelectionActive, type CatalogueDocumentSelection} from '../browse/document-selection';
import {t, locale, environment as env} from './text';
import {Fragment, useId, useEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {preferenceValue} from '@core/preferences.ts';
import type {ShowWorkspaceRoute, ShowWorkspaceData, ShowSettings, WorkspaceSeason, WorkspaceShowCredit} from '@core/show-workspace.ts';
import {cardText, episodeLabel, titleFactParts, type FactPart} from '@core/presentation/kit-model';
import {Button, Chip, ResumeButton, Tabs, Segmented, Switch} from '../../kit/controls';
import {AmbientArt, Artwork} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {Lockup, layoutType, platformType} from '../../kit/layout';
import {EpisodeTile} from '../../kit/cards';
import {Menu, MenuItem, MenuSeparator, Popover} from '../../kit/overlays';
import {OwnerTag, ServerMark} from '../../kit/shell';
import {StateView} from '../../kit/states';
import {useShellWidth} from '../../shell/viewport';
import {DetailLoading, ScreenFailure, TitleActions, TitleRecommendationRow} from './Detail';
import {titleRecommendationRows} from '@core/recommendations.ts';
import {episodeCode} from '@core/presentation/card-caption.ts';
import {entrySubject, titlePageFacts, calendarDate, instantDate} from './model';
import type {ShowBindings} from './show-bindings';
import {showDetailFacts} from './show-bindings';
import {seasonProgress, showPeople} from '@core/presentation/title-page.ts';
import styles from './show.module.css';
import detail from './detail.module.css';
import {dependsOn, titlesWithPersonalState} from '@core/freshness-hub.ts';
import {useFreshDocument} from '../../platform/freshness';
import {EpisodeDetailsLayout, TitleProse, TitleFactsLine} from '../../kit/title/TitleParts';
import {TitlePeople, ShowPeople, CreditNames} from './People';
import {TitleTabs} from './TitleTabs';
import {ScreenProblem} from './ScreenProblem';
import {runScreenCommand} from './context';
import {GenreNames} from './GenreNames';
import type {ContainerState} from '@core/container-state.ts';
import {SeasonMenu} from './SeasonMenu';
import {showPrimary} from './show-primary';

export type ShowScreenProps = {binding: ShowBindings; selection?: CatalogueDocumentSelection; target: ShowWorkspaceRoute};
export function ShowScreen({binding, target, selection}: ShowScreenProps) {
  const key = JSON.stringify(target);
  useEffect(() => { const route: ShowWorkspaceRoute = JSON.parse(key), request = new AbortController(); void binding.select(route, request.signal).catch(error => { if (!request.signal.aborted) binding.context.effects.reportError(error); }); return () => { request.abort(); binding.cancel(); }; }, [binding, key]);
  return <ShowBody binding={binding} selection={selection} />;
}

/** Root §14: complete-season actions; the display editor remains show-scoped. */
export function SeasonOptions({binding, data, label}: {binding: ShowBindings; data: ShowWorkspaceData; label: string}) {
  const [open, setOpen] = useState(false), [display, setDisplay] = useState(false), [state, setState] = useState<ContainerState>();
  const anchor = useRef<HTMLButtonElement>(null), owner = binding.canManage();
  useEffect(() => {
    setState(undefined);
    if (!open) return;
    const request = new AbortController();
    void binding.readSeasonState(data, request.signal).then(value => { if (!request.signal.aborted) setState(value); }).catch(error => { if (!request.signal.aborted) binding.context.effects.reportError(error); });
    return () => request.abort();
  }, [binding, data, open]);
  const complete = !!state && (state.watchedCount ?? 0) > 0 && state.unwatchedCount === 0;
  return <>
    <Menu presentation="anchored" open={open} onOpenChange={setOpen} title={t('next.detail.seasonOptions', {season: label})} trigger={<Button ref={anchor} variant="icon" size="small" icon="i-more" iconOnly aria-label={t('next.detail.seasonOptions', {season: label})} />}>
      <MenuItem onSelect={() => runScreenCommand(binding.context, () => binding.playSeason(data))}>{t('next.detail.playSeason')}</MenuItem>
      <MenuItem disabled={!state} onSelect={() => runScreenCommand(binding.context, () => { if (!state) return; return binding.setSeasonWatched(data, state, !complete); })}>{t(complete ? 'next.detail.markSeasonUnwatched' : 'next.detail.markSeasonWatched')}</MenuItem>
      {owner && <><MenuSeparator /><MenuItem onSelect={() => runScreenCommand(binding.context, () => { binding.current(data); if (!binding.canManage()) throw new DOMException('Show management changed', 'AbortError'); setDisplay(true); })}>{t('next.detail.episodeDisplay')}</MenuItem></>}
    </Menu>
    {owner && <Popover open={display} onOpenChange={setDisplay} anchorRef={anchor} returnFocusRef={anchor} width={540} className={styles.settingsMenu} title={t('title.showSettings')}><ShowSettingsMenu binding={binding} data={data} /></Popover>}
  </>;
}

/** The drawn season row: its years, episode count and the viewer's real progress. */
export function seasonMenuDescription(season: WorkspaceSeason): ReactNode {
  const year = (value?: string) => { if (!value) return undefined; const date = new Date(value); return Number.isFinite(date.getTime()) ? date.getUTCFullYear() : undefined; };
  const first = year(season.firstAired), last = year(season.lastAired), progress = seasonProgress(season.watchedCount, season.unwatchedCount);
  const years = first === undefined ? undefined : t('next.detail.seasonYears', {first, last: last ?? first});
  const parts = [years, season.episodeCount !== undefined ? t('count.episodes', {count: season.episodeCount}) : undefined,
    progress ? t('title.seasonProgress', {watched: progress.watched, total: progress.total}) : undefined];
  const values = parts.filter(Boolean);
  return values.length ? <span className={styles.seasonDescription}>{values.map((value, index) => <span key={index}>{value}</span>)}</span> : undefined;
}

/** Show-level commands must not target the next episode's detail binding. */
function ShowMore({binding, data, personal, busy}: {binding: ShowBindings; data: ShowWorkspaceData; personal?: ContainerState; busy: boolean}) {
  const [open, setOpen] = useState(false), [settings, setSettings] = useState(false), anchor = useRef<HTMLButtonElement>(null), owner = binding.canManage();
  useEffect(() => { setOpen(false); setSettings(false); }, [data]);
  if (!personal && !owner) return null;
  return <>
    <Menu open={open} onOpenChange={setOpen} title={data.show.title} trigger={<Button ref={anchor} icon="i-more" iconOnly aria-label={t('next.controls.more')} />}>
      {personal && <MenuItem icon="i-heart" disabled={busy} selected={personal.favorite} onSelect={() => runScreenCommand(binding.context, () => binding.setFavorite(data, !personal.favorite))}>{t(personal.favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites')}</MenuItem>}
      {owner && <>{personal && <MenuSeparator />}<MenuItem onSelect={() => runScreenCommand(binding.context, () => { binding.current(data); if (!binding.canManage()) throw new DOMException('Show management changed', 'AbortError'); setSettings(true); })}>{t('title.showSettings')}</MenuItem></>}
    </Menu>
    {owner && <Popover open={settings} onOpenChange={setSettings} anchorRef={anchor} returnFocusRef={anchor} width={540} className={styles.settingsMenu} title={t('title.showSettings')}><ShowSettingsMenu binding={binding} data={data} /></Popover>}
  </>;
}

/** One body below Watch's video as well as on the title route. No independent video host. */
export function ShowBody({binding, compact = false, selection}: {binding: ShowBindings; compact?: boolean; selection?: CatalogueDocumentSelection}) {
  const tabPanelId = useId();
  const selecting = useCatalogueSelectionActive(selection);
  const snapshot = useSyncExternalStore(binding.service.subscribe, binding.service.getSnapshot, binding.service.getSnapshot);
  useFreshDocument(() => [dependsOn.libraries, ...titlesWithPersonalState(binding.service.getSnapshot().route?.libraryId), dependsOn.event('stream.resync')], refresh => binding.revalidate(refresh), () => binding.service.getSnapshot().phase === 'ready');
  const preferences = useSyncExternalStore(binding.context.preferences.subscribe, binding.context.preferences.getSnapshot, binding.context.preferences.getSnapshot);
  const personal = useSyncExternalStore(binding.personal.subscribe, binding.personal.getSnapshot, binding.personal.getSnapshot);
  const busy = useSyncExternalStore(binding.personal.subscribe, binding.personal.getBusy, binding.personal.getBusy);
  const primary = useSyncExternalStore(binding.primary.service.subscribe, binding.primary.service.getSnapshot, binding.primary.service.getSnapshot);
  const {tab, layout} = useSyncExternalStore(binding.view.subscribe, binding.view.getSnapshot, binding.view.getSnapshot), [seasonMenu, setSeasonMenu] = useState(false), [filterMenu, setFilterMenu] = useState(false), [orderMenu, setOrderMenu] = useState(false);
  const width = useShellWidth(), phone = width < 1024, data = snapshot.data;
  const recommendations = useSyncExternalStore(binding.recommendations.subscribe, binding.recommendations.getSnapshot, binding.recommendations.getSnapshot);
  useEffect(() => { if (data && tab === 'related') void binding.recommendations.select(data, () => binding.service.getSnapshot().data === data); }, [binding, data, tab]);
  if (!data) return snapshot.phase === 'error' ? <ScreenProblem context={binding.context} error={snapshot.error} subject={binding.subject()} retry={() => { void binding.service.retry(); }} /> : <DetailLoading key={snapshot.route?.showId ?? snapshot.route?.episodeId ?? snapshot.route?.seasonId} />;
  const {show} = data, selectedSeason = data.seasons.find(season => season.id === data.selected.seasonId);
  const selectedProgress = selectedSeason ? seasonProgress(selectedSeason.watchedCount, selectedSeason.unwatchedCount) : undefined;
  const seasonLabel = selectedSeason ? selectedSeason.number === 0 ? t('next.kit.special') : t('next.kit.season', {number: selectedSeason.number}) : data.groups.find(group => group.id === data.selected.group)?.title ?? t('next.controls.episodes');
  const selectSeason = (id: string) => { runScreenCommand(binding.context, () => { binding.current(data); return binding.service.selectSeason(id).then(() => setSeasonMenu(false)); }); };
  const pageSeasons = (next: boolean) => { runScreenCommand(binding.context, () => { binding.current(data); return next ? binding.service.nextSeasons() : binding.service.previousSeasons(); }); };
  const setPlayState = (state: 'all' | 'played' | 'unplayed' | 'in-progress') => {
    runScreenCommand(binding.context, () => { binding.current(data); const route = snapshot.route; if (route) return binding.select({...route, playState: state === 'all' ? undefined : state}); });
    setFilterMenu(false);
  };
  const jumpSeason = async (number: number) => {
    try {
      for (let page = 0; page < 20; page++) {
        const snapshot = binding.service.getSnapshot(), seasons = snapshot.data?.seasons ?? [], target = seasons.find(season => season.number === number);
        if (target) { await binding.service.selectSeason(target.id); return true; }
        if (!snapshot.seasonPagination.nextCursor) return false;
        await binding.service.nextSeasons();
      }
      return false;
    } catch (error) { binding.context.effects.reportError(error); return false; }
  };
  const episodes = data.episodes.sections[0]?.entries ?? [], art = binding.context.artwork;
  const offer = showPrimary(data, primary), code = offer ? episodeCode(offer.next, t) : undefined;
  const primaryLabel = offer ? code ? t(offer.resume ? 'title.resumeEpisode' : 'title.playEpisode', {code}) : t(offer.resume ? 'next.controls.resume' : 'next.controls.play') : undefined;
  const playPrimary = () => runScreenCommand(binding.context, () => { binding.current(data); if (offer) return binding.primary.play(offer.detail, offer.action.id); });
  const actions = <div className={`${detail.actions} ${styles.heroActions}`}>
    {offer && (offer.resume ? <ResumeButton progress={offer.progress} onClick={playPrimary}>{primaryLabel}</ResumeButton> : <Button variant="primary" icon="i-play" onClick={playPrimary}>{primaryLabel}</Button>)}
    {personal && <Button disabled={busy} icon={personal.watchlisted ? 'i-check' : 'i-plus'} aria-pressed={personal.watchlisted} onClick={() => { void binding.setMyList(data, !personal.watchlisted).catch(binding.context.effects.reportError); }}>{t('next.controls.myList')}</Button>}
    {personal && <Button disabled={busy} icon="i-check" iconOnly aria-label={t(personal.watched ? 'title.markUnwatched' : 'title.markWatched')} aria-pressed={personal.watched} onClick={() => { void binding.setWatched(data, !personal.watched).catch(binding.context.effects.reportError); }} />}
    <ShowMore binding={binding} data={data} personal={personal} busy={busy} />
  </div>;
  // The pinned bar carries the drawn Resume Sx Ey and My List controls once the hero is gone.
  const pin = offer && (offer.resume ? <ResumeButton size="small" progress={offer.progress} onClick={playPrimary}>{primaryLabel}</ResumeButton> : <Button variant="primary" size="small" icon="i-play" onClick={playPrimary}>{primaryLabel}</Button>);
  const owner = binding.canManage();
  const facts = titlePageFacts({year: show.year, seasonCount: data.seasonTotalCount, genres: show.genres, contentRating: show.contentRating}, env);
  const starring = showPeople(show.credits).cast.slice(0, 4), [starringBefore, starringAfter] = t('title.starring', {names: '\uFFF0'}).split('\uFFF0');
  const credits = starring.length > 0 && <div className={styles.starring}>{starringBefore}<ShowPersonLinks credits={starring} binding={binding} data={data} />{starringAfter}</div>;
  const titleFacts = <TitleFactsLine parts={facts} genres={<GenreNames names={show.genres ?? []} onOpen={binding.context.effects.openBrowse ? genre => runScreenCommand(binding.context, () => binding.browseGenre(data, genre)) : undefined} />} />;
  // Phone frames always list episodes; retain the desktop choice across rotation.
  const episodeLayout = phone ? 'list' : layout, columns = 4;
  return <article className={`${detail.page} ${styles.showPage}`} data-show-id={show.id} data-compact={compact || undefined}>
    {!compact ? phone ? <header className={styles.phoneHero}>
      <div className={styles.phonePicture}>
        {preferenceValue<boolean>(preferences, 'appearance.showBackdrops') && <AmbientArt className={`${detail.phoneHeroArt} ${styles.phoneArt}`} treatment="phone" position={width < 600 ? '44% 20%' : '30% 30%'} source={art.ambient(show.backdropUrl ?? show.posterUrl)} foreground={art.foreground(show.backdropUrl ?? show.posterUrl, width < 600 ? 800 : 1600, 16 / 9)} />}
        <div className={styles.phoneCopy}><Lockup title={show.title} platform="phone-web" style={width < 600 ? undefined : layoutType(platformType('desktop').lockupLong)} />{titleFacts}</div>
      </div>
      {actions}{show.overview && <TitleProse variant="hero" className={styles.phoneSynopsis}>{show.overview}</TitleProse>}{credits}
    </header> : <header className={detail.hero}>
      {preferenceValue<boolean>(preferences, 'appearance.showBackdrops') && <AmbientArt className={detail.heroArt} treatment="title" source={art.ambient(show.backdropUrl ?? show.posterUrl)} foreground={art.foreground(show.backdropUrl ?? show.posterUrl, 1920, 16 / 9)} />}
      <div className={detail.copy}><Lockup title={show.title} platform="desktop" />{titleFacts}{show.overview && <TitleProse variant="hero" className={detail.blurb}>{show.overview}</TitleProse>}{credits}{actions}</div>
    </header> : <div className={detail.compactHeader}><h1>{show.title}</h1>{actions}</div>}
    <TitleTabs title={show.title} compact={compact} onBack={binding.context.effects.back ? () => runScreenCommand(binding.context, () => { binding.current(data); binding.context.effects.back?.(); }) : undefined} actions={!compact ? <div className={`${detail.actions} ${styles.pinActions}`}>{pin}{personal && <Button size="small" disabled={busy} icon={personal.watchlisted ? 'i-check' : 'i-plus'} iconOnly aria-label={t('next.controls.myList')} title={t('next.controls.myList')} aria-pressed={personal.watchlisted} onClick={() => { void binding.setMyList(data, !personal.watchlisted).catch(binding.context.effects.reportError); }} />}</div> : undefined}><Tabs externalPanelId={tabPanelId} value={tab} onValueChange={value => runScreenCommand(binding.context, () => { binding.current(data); if (value === 'episodes' || value === 'related' || value === 'details') binding.view.tab(value); })} items={[{value: 'episodes', label: t('next.controls.episodes')}, {value: 'related', label: t('next.controls.moreLikeThis')}, {value: 'details', label: t('next.controls.details')}]} /></TitleTabs>
    <div key={tab} id={tabPanelId} role="tabpanel" aria-labelledby={`${tabPanelId}-tab-${tab}`} tabIndex={0} className={detail.tabPanel}>
    {tab === 'episodes' && <section className={styles.episodes}>
      {selection?.toolbar}
      {!selecting && <div className={styles.toolbar}>
        {data.groups.length === 0 ? <SeasonMenu open={seasonMenu} onOpenChange={setSeasonMenu} label={seasonLabel} seasons={data.seasons} selectedId={data.selected.seasonId ?? undefined}
          totalCount={data.seasonTotalCount} hasPrevious={snapshot.seasonPagination.canPrevious} hasNext={!!snapshot.seasonPagination.nextCursor}
          onSelect={selectSeason} onPrevious={() => pageSeasons(false)} onNext={() => pageSeasons(true)} onJump={jumpSeason} />
          : <Menu open={seasonMenu} onOpenChange={setSeasonMenu} title={t('title.seasonMenu')} trigger={<Chip variant="toolbar" trailingIcon="i-down">{seasonLabel}</Chip>}>
            {data.seasons.map(season => <MenuItem key={season.id} selected={season.id === data.selected.seasonId} description={seasonMenuDescription(season)} onSelect={() => selectSeason(season.id)}>{season.number === 0 ? t('next.kit.special') : t('next.kit.season', {number: season.number})}</MenuItem>)}
            {data.groups.map(group => <MenuItem key={group.id} selected={group.id === data.selected.group} onSelect={() => runScreenCommand(binding.context, () => { binding.current(data); return binding.service.selectGroup(group.id); })}>{group.title}</MenuItem>)}
            {snapshot.seasonPagination.canPrevious && <MenuItem onSelect={() => pageSeasons(false)}>{t('title.earlierSeasons')}</MenuItem>}
            {snapshot.seasonPagination.nextCursor && <MenuItem onSelect={() => pageSeasons(true)}>{t('title.laterSeasons')}</MenuItem>}
          </Menu>}
        {selectedSeason?.episodeCount !== undefined && <span className={styles.progress}>{t('count.episodes', {count: selectedSeason.episodeCount})}</span>}
        {selectedProgress ? <><span className={styles.progressDivider} aria-hidden="true" /><span className={styles.watchedProgress}><span className={styles.watchedMeter} aria-hidden="true"><i style={{width: `${selectedProgress.total > 0 ? 100 * selectedProgress.watched / selectedProgress.total : 0}%`}} /></span>{t('title.seasonProgress', {watched: selectedProgress.watched, total: selectedProgress.total})}</span></>
          : null}
        <div className={styles.toolbarActions}>
        <Menu presentation="anchored" open={orderMenu} onOpenChange={setOrderMenu} title={t('next.detail.episodeOrder')} trigger={<Chip variant="toolbar" icon="i-sort" iconSize={18} trailingIcon="i-down" trailingSize={16}>{t('next.detail.episodeOrder')}</Chip>}>
          <MenuItem selected={(snapshot.route?.episodeDirection ?? 'asc') === 'asc'} onSelect={() => runScreenCommand(binding.context, () => { binding.current(data); setOrderMenu(false); return binding.service.selectEpisodeDirection('asc'); })}>{t('next.detail.episodeOrder')}</MenuItem>
          <MenuItem selected={snapshot.route?.episodeDirection === 'desc'} onSelect={() => runScreenCommand(binding.context, () => { binding.current(data); setOrderMenu(false); return binding.service.selectEpisodeDirection('desc'); })}>{t('next.detail.reverseEpisodeOrder')}</MenuItem>
        </Menu>
        <Menu open={filterMenu} onOpenChange={setFilterMenu} title={t('next.controls.filters')} trigger={<Chip variant="toolbar">{t('next.controls.filters')}</Chip>}>
          <MenuItem selected={!snapshot.route?.playState} onSelect={() => setPlayState('all')}>{t('next.controls.all')}</MenuItem>
          <MenuItem selected={snapshot.route?.playState === 'unplayed'} onSelect={() => setPlayState('unplayed')}>{t('filter.quick.unplayed')}</MenuItem>
          <MenuItem selected={snapshot.route?.playState === 'played'} onSelect={() => setPlayState('played')}>{t('filter.quick.played')}</MenuItem>
          <MenuItem selected={snapshot.route?.playState === 'in-progress'} onSelect={() => setPlayState('in-progress')}>{t('filter.quick.inProgress')}</MenuItem>
        </Menu>
        <label className={styles.hide}>{t('next.controls.hideWatched')}<Switch checked={snapshot.route?.playState === 'unplayed'} onCheckedChange={checked => runScreenCommand(binding.context, () => { binding.current(data); const route = snapshot.route; if (route) return binding.select({...route, playState: checked ? 'unplayed' : 'all'}); })} /></label>
        <span className={styles.layoutToggle} role="group" aria-label={t('next.controls.layout')}>
          <button type="button" aria-label={t('next.controls.list')} aria-pressed={layout === 'list'} onClick={() => runScreenCommand(binding.context, () => binding.view.layout('list'))}><Icon name="i-list" size={18} /></button>
          <button type="button" aria-label={t('next.controls.grid')} aria-pressed={layout === 'grid'} onClick={() => runScreenCommand(binding.context, () => binding.view.layout('grid'))}><Icon name="i-grid" size={18} /></button>
        </span>
        {data.selected.seasonId && <SeasonOptions binding={binding} data={data} label={seasonLabel} />}
        </div>
      </div>}
      {snapshot.phase === 'error' || snapshot.phase === 'refresh-required' ? <ScreenFailure retry={() => { void binding.service.retry(); }} /> : null}
      {!episodes.length && snapshot.phase === 'ready' && <StateView title={t('title.noEpisodes')} body={t('title.noEpisodesBody')} />}
      <div className={styles.tiles} data-layout={episodeLayout} style={{'--episode-columns': columns} as React.CSSProperties}>
        {episodes.map((episode, index) => {
          const expanded = snapshot.anchor.expandedEpisodeId === episode.id, rowEnd = episodeLayout === 'list' ? index : Math.min(episodes.length - 1, Math.floor(index / columns) * columns + columns - 1);
          const opened = episodes.slice(episodeLayout === 'list' ? index : Math.floor(index / columns) * columns, rowEnd + 1).find(entry => entry.id === snapshot.anchor.expandedEpisodeId);
          const subject = entrySubject(episode), model = cardText(subject, 'in-show', env), next = data.nextUp?.id === episode.id;
          const date = subject.episode?.mode !== 'date' && subject.episode?.mode !== 'special' ? episodeLabel({mode: 'date', day: episode.airDate}, 'in-show', env) : undefined;
          const continuing = !episode.watched && (episode.progressSeconds ?? 0) > 0;
          const status: FactPart[] = continuing ? [{id: 'continue', text: t('title.continueEpisode'), role: 'status'}, ...cardText(subject, 'continuation', env).detail] : next ? [{id: 'up-next', text: t('title.nextUp'), role: 'status'}] : [];
          return <Fragment key={episode.id}><CatalogueSelectionEntry selection={selection} entry={episode} position={index}>{picked => <EpisodeTile selection={picked} className={styles.episodeRow} model={{...model, caption: [...model.caption, ...status]}} length={model.detail.find(part => part.id === 'duration')?.text} airDate={!continuing && date ? {...date, id: 'air-date'} : undefined} context="detail" artworkKind={episode.stillUrl ? 'still' : 'backdrop'} artwork={art.foreground(episode.stillUrl ?? episode.backdropUrl ?? show.backdropUrl, 632, 16 / 9)} watched={episode.watched} expanded={expanded} next={next} description={episode.overview}
            onPlay={episode.available === true && episode.playback?.itemId === episode.id ? () => runScreenCommand(binding.context, () => binding.playEpisode(data, episode)) : undefined}
            onNavigate={() => { void binding.expand(data, episode).catch(binding.context.effects.reportError); }} />}</CatalogueSelectionEntry>
            {rowEnd === index && opened && <div className={styles.expanded}><EpisodeBody binding={binding} data={data} /></div>}
          </Fragment>;
        })}
      </div>
      <div className={styles.paging}>{snapshot.pagination.canPrevious && <Button size="compact" onClick={() => runScreenCommand(binding.context, () => { binding.current(data); return binding.service.previousEpisodes(); })}>{t('title.previousPage')}</Button>}{snapshot.pagination.nextCursor && <Button size="compact" onClick={() => runScreenCommand(binding.context, () => { binding.current(data); return binding.service.nextEpisodes(); })}>{t('title.nextPage')}</Button>}</div>
    </section>}
    {tab === 'details' && <ShowDetails binding={binding} data={data} owner={owner} />}
    {tab === 'related' && (recommendations.phase === 'error' ? <ScreenFailure retry={() => { void binding.recommendations.select(data, () => binding.service.getSnapshot().data === data); }} /> : recommendations.phase === 'loading' ? <DetailLoading /> : <div className={styles.relatedRows}>{titleRecommendationRows(recommendations.data).map(row => <TitleRecommendationRow key={row.id} binding={binding} row={row} owner={{kind: 'show', id: data.show.id}} guard={() => binding.current(data)} />)}</div>)}
    </div>
  </article>;
}

export function EpisodeBody({binding, data}: {binding: ShowBindings; data: ShowWorkspaceData}) {
  const snapshot = useSyncExternalStore(binding.episode.service.subscribe, binding.episode.service.getSnapshot, binding.episode.service.getSnapshot), episode = snapshot.data;
  if (!episode) return snapshot.phase === 'error' ? <ScreenFailure retry={() => { void binding.episode.service.retry(); }} /> : <DetailLoading shape="episode" />;
  const season = data.seasons.find(value => value.id === data.selected.seasonId);
  const listed = data.episodes.sections[0]?.entries ?? [];
  const entry = listed.find(value => value.id === episode.item.id);
  const code = entry ? episodeCode(entry, t) : undefined, aired = entry?.airDate ? calendarDate(entry.airDate, locale()) : undefined;
  const open = (id: string) => runScreenCommand(binding.context, () => { binding.current(data); return binding.select({libraryId: data.scope.libraryId, showId: data.show.id, episodeId: id}); });
  const neighbours = (['previous', 'next'] as const).map(direction => {
    const target = episode.episodeDetails?.[direction];
    if (!target) return null;
    const art = listed.find(value => value.id === target.id);
    return <button key={direction} type="button" className={styles.episodeNav} aria-label={t(direction === 'previous' ? 'title.previousEpisode' : 'title.nextEpisode')} onClick={() => open(target.id)}>
      <span className={styles.episodeNavArt}><Artwork source={binding.context.artwork.foreground(art?.stillUrl ?? art?.backdropUrl ?? data.show.backdropUrl, 224, 16 / 9)} alt="" /></span>
      <span className={styles.episodeNavCopy}><small>{direction === 'previous' && <Icon name="i-left" size={14} />}{t(direction === 'previous' ? 'title.previousEpisode' : 'title.nextEpisode')}{direction === 'next' && <Icon name="i-right" size={14} />}</small><b>{target.title}</b><small>{t('next.kit.episode', {number: target.number})}</small></span>
    </button>;
  });
  const credits = episode.metadata.credits;
  const guests = credits.filter(credit => /acting|cast/i.test(credit.department ?? ''));
  const directors = credits.filter(credit => /directing|director/i.test(`${credit.department ?? ''} ${credit.role ?? ''}`));
  const writers = credits.filter(credit => /writing|writer|screenplay|teleplay/i.test(`${credit.department ?? ''} ${credit.role ?? ''}`));
  const facts: FactPart[] = [
    ...(code ? [{id: 'episode-code', text: code, role: 'context' as const}] : []),
    ...(aired ? [{id: 'aired', text: t('title.airedOn', {date: aired}), role: 'context' as const}] : []),
    ...titleFactParts({durationSeconds: episode.item.duration, contentRating: episode.episodeDetails?.contentRating}, env),
  ];
  const side = <div className={styles.episodeSide}>
    {neighbours}
    {(guests.length > 0 || directors.length > 0 || writers.length > 0) && <dl className={styles.episodeCredits}>
      {guests.length > 0 && <><dt>{t('next.show.guestStars')}</dt><dd>{guests.map(credit => <span key={credit.id ?? credit.name} className={styles.guestLine}><CreditNames binding={binding.episode} data={episode} credits={[credit]} /><span>{credit.role}</span></span>)}</dd></>}
      {directors.length > 0 && <><dt>{t('title.directedByLabel')}</dt><dd><CreditNames binding={binding.episode} data={episode} credits={directors} /></dd></>}
      {writers.length > 0 && <><dt>{t('title.writtenByLabel')}</dt><dd><CreditNames binding={binding.episode} data={episode} credits={writers} /></dd></>}
    </dl>}
  </div>;
  return <EpisodeDetailsLayout title={episode.item.title} onClose={() => runScreenCommand(binding.context, binding.closeEpisode)} context={<><b className={styles.contextShow}>{data.show.title}</b>{season && <span>{season.title}</span>}</>}
    artwork={binding.context.artwork.foreground(episode.item.stillUrl ?? data.show.backdropUrl, 880, 16 / 9)} backdrop={!episode.item.stillUrl}
    facts={<TitleFactsLine parts={facts} />}
    synopsis={episode.item.overview} actions={<TitleActions binding={binding.episode} data={episode} />}
    credits={side} />;
}

/** The drawn About column: the show's own stored facts, never a file fact. */
export function ShowDetails({binding, data, owner}: {binding: ShowBindings; data: ShowWorkspaceData; owner: boolean}) {
  const {show} = data, facts = showDetailFacts(show, data.seasonTotalCount, locale());
  const onServer = facts.onServer ? [t('title.season', {number: facts.onServer.seasons}), facts.onServer.episodes !== undefined ? t('count.episodes', {count: facts.onServer.episodes}) : undefined].filter(Boolean).join(', ') : undefined;
  const rows: (readonly [string, ReactNode])[] = [
    [t('next.show.firstAired'), calendarDate(facts.firstAired, locale())],
    [t('next.show.status'), facts.status],
    [t('title.network'), facts.network ? <button type="button" className={styles.personLink} onClick={() => runScreenCommand(binding.context, () => binding.browseFacet(data, 'network', facts.network!))}>{facts.network}</button> : undefined],
    [t('title.studio'), facts.studio ? <button type="button" className={styles.personLink} onClick={() => runScreenCommand(binding.context, () => binding.browseFacet(data, 'studio', facts.studio!))}>{facts.studio}</button> : undefined],
    [t('title.country'), facts.country],
    [t('title.genres'), facts.genres.length ? <GenreNames names={facts.genres} onOpen={binding.context.effects.openBrowse ? genre => runScreenCommand(binding.context, () => binding.browseGenre(data, genre)) : undefined} /> : undefined],
    [t('next.show.createdBy'), facts.creators.length ? <ShowPersonLinks credits={facts.creators} binding={binding} data={data} /> : undefined],
    [t('next.show.rated'), facts.rating],
    [t('next.selection.language'), facts.language],
    [t('next.show.onThisServer'), onServer],
    [t('title.added'), instantDate(facts.addedAt, locale())],
  ];
  return <div className={styles.detailsTab} data-show-details>
    <div className={styles.detailsGrid}>
      <section>
        <h2 className={styles.sectionHeading}>{t('next.show.about')}</h2>
        <dl className={styles.facts}>{rows.filter(row => !!row[1]).map(([label, value]) => <Fragment key={label}><dt>{label}</dt><dd>{value}</dd></Fragment>)}</dl>
      </section>
      <div className={styles.castBlock}><ShowPeople binding={binding} data={data} /></div>
    </div>
    {owner && <ShowSettingsPanel binding={binding} data={data} />}
  </div>;
}

function ShowPersonLinks({credits, binding, data}: {credits: readonly WorkspaceShowCredit[]; binding: ShowBindings; data: ShowWorkspaceData}) {
  return <>{credits.map((credit, index) => <Fragment key={`${credit.id ?? credit.name}:${credit.ordinal}`}>{index > 0 && ', '}{credit.id
    ? <button type="button" className={styles.personLink} onClick={() => runScreenCommand(binding.context, () => {
      binding.current(data);
      binding.context.effects.openEntry({id: credit.id!, kind: 'person', title: credit.name, navigation: {view: 'person', entityId: credit.id!}});
    })}>{credit.name}</button>
    : credit.name}</Fragment>)}</>;
}

/** The owner's drawn Server panel. It carries no file facts; the four stored settings stay the source. */
export function ShowSettingsPanel({binding, data}: {binding: ShowBindings; data: ShowWorkspaceData}) {
  const [open, setOpen] = useState(false), settings = data.settings;
  const grouping = settings.ranges ? t('next.show.group.ranges') : settings.hideSeasons ? t('next.show.group.oneList') : t('next.show.group.bySeason');
  return <section className={styles.owner} aria-label={t('next.shell.ownerOnly')}>
    <div className={styles.ownerHeader}>
      <ServerMark size={34} />
      <div><b>{t('title.showSettings')}<OwnerTag /></b><small>{t('next.show.membersNeverSee')}</small></div>
    </div>
    <div className={styles.ownerRow}>
      <div className={styles.ownerCell} data-show-settings>
        <div className={styles.ownerLabel}>{t('next.show.episodeDisplay')}</div>
        <div className={styles.ownerBig}><span>{grouping}</span><span>{settings.newestFirst ? t('next.overlays.newest') : t('next.overlays.oldest')}</span></div>
        <p>{t('next.show.howListed')}</p>
        <Popover open={open} onOpenChange={setOpen} placement="top-end" sideOffset={8} alignOffset={-18} width={540} className={styles.settingsMenu}
          title={t('title.showSettings')} trigger={<Chip className={styles.changeChip} trailingIcon="i-down">{t('next.show.change')}</Chip>}>
          <ShowSettingsMenu binding={binding} data={data} />
        </Popover>
      </div>
    </div>
  </section>;
}

/** The drawn Episode display menu: three authored controls over the four stored settings. */
export function ShowSettingsMenu({binding, data}: {binding: ShowBindings; data: ShowWorkspaceData}) {
  const settings = data.settings, preview = data.nextUp ?? data.episodes.sections[0]?.entries[0];
  const group = settings.ranges ? 'ranges' : settings.hideSeasons ? 'oneList' : 'bySeason';
  const numbering = settings.absoluteNumbering ? 'across' : 'bySeason';
  const order = settings.newestFirst ? 'newest' : 'oldest';
  const code = preview ? (settings.absoluteNumbering && preview.absoluteNumber ? t('next.kit.episode', {number: preview.absoluteNumber}) : episodeCode(preview, t)) : undefined;
  const save = (patch: Partial<Pick<ShowSettings, 'hideSeasons' | 'ranges' | 'absoluteNumbering' | 'newestFirst'>>) => {
    const next: ShowSettings = {...settings, ...patch};
    return binding.setSettings(data, next);
  };
  return <>
    <div className={styles.menuHead}><b>{t('next.show.episodeDisplay')}</b><span className={styles.menuTag}>{t('next.show.forEveryone')}</span></div>
    <p className={styles.menuLede}>{t('title.showSettings.lede')}</p>
    <div className={styles.menuGrid}>
      <span className={styles.menuLabel}>{t('next.show.groupEpisodes')}</span>
      <Segmented className={styles.settingsSeg} value={group} aria-label={t('next.show.groupEpisodes')} items={[{value: 'bySeason', label: t('next.show.group.bySeason')}, {value: 'oneList', label: t('next.show.group.oneList')}, {value: 'ranges', label: t('next.show.group.ranges')}]}
        onValueChange={value => { if (value === 'bySeason') runScreenCommand(binding.context, () => save({hideSeasons: false, ranges: false})); else if (value === 'oneList') runScreenCommand(binding.context, () => save({hideSeasons: true, ranges: false})); else if (value === 'ranges') runScreenCommand(binding.context, () => save({hideSeasons: false, ranges: true})); }} />
      <span className={styles.menuLabel}>{t('next.show.numberThem')}</span>
      <Segmented className={styles.settingsSeg} value={numbering} aria-label={t('next.show.numberThem')} items={[{value: 'bySeason', label: t('next.show.group.bySeason')}, {value: 'across', label: t('next.show.number.acrossSeasons')}]}
        onValueChange={value => { if (value === 'bySeason' || value === 'across') runScreenCommand(binding.context, () => save({absoluteNumbering: value === 'across'})); }} />
      <span className={styles.menuLabel}>{t('next.show.order')}</span>
      <Segmented className={styles.settingsSeg} value={order} aria-label={t('next.show.order')} items={[{value: 'oldest', label: t('next.overlays.oldest')}, {value: 'newest', label: t('next.overlays.newest')}]}
        onValueChange={value => { if (value === 'oldest' || value === 'newest') runScreenCommand(binding.context, () => save({newestFirst: value === 'newest'})); }} />
    </div>
    {preview && <div className={styles.preview}><span>{t('next.show.readsAs')}</span>{code && <b>{code}</b>}<span>{preview.title}</span></div>}
  </>;
}
