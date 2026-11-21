import {useEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import type {View} from 'react-native';
import type {NativeStartArtworkOffer} from '../../../platform/playback/start-presentation';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {WorkspaceSeason} from '../../../../../../packages/client-core/src/show-workspace';
import {cardText, episodeLabel, titleFactParts, type FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {CollectionCardProps} from '../../../kit/cards';
import type {EpisodeListProps, EpisodePlateProps, PageStateProps, PeopleSheetProps, TitleAction, TitleKicker, TitlePrimary, TitleDetailsProps, CastPerson} from '../../../kit/frames';
import type {PosterRowProps} from '../../../kit/layout';
import type {SeasonSheetProps, SeasonSheetRow} from '../../../kit/layout/SeasonSheet';
import type {NativeTitlePageLease} from '../../../screens/watch/title-controller';
import type {NativeTitleScreenProps} from '../../../screens/watch/TitleScreen';
import type {NativeTitleTab} from '../../../screens/watch/title-memory';

export type TitleTab = Readonly<{value: NativeTitleTab; label: string}>;
/** What a film's title page shows, from the title controller (the same reads and commands the old screen used).
 * No layout: the touch and TV pages draw it on `TitleFrame`. */
/** One season (or an owner's episode group) for the TV column, with what the band says about it. */
export type TVSeason = Readonly<{id: string; label: string; count?: string; year?: string; overview?: string; progress?: string; fraction?: number; current: boolean; complete: boolean; group: boolean}>;
/** One episode tile on TV (detail-tv-5b): the length alone on the still (no air date there), Up next, its progress. */
export type TVTile = Readonly<{key: string; title: string; label: readonly FactPart[]; length?: string; status?: string; watched?: boolean; progress?: number; artwork?: ArtworkSource; backdropFallback: boolean;
  onMore: (anchor: React.RefObject<View | null>) => void}>;
export type TVEpisodes = Readonly<{seasons: readonly TVSeason[]; tiles: readonly TVTile[]; nextUp?: string; select: (id: string) => void; focusEpisode: (key: string | undefined) => void; play: (key: string) => void;
  playSeason: (id: string) => void; markSeason: (id: string, watched: boolean) => void; moreSeasons: () => void}>;
export type TitleView = Readonly<{
  retired: boolean;
  entry: ContentEntry;
  title: string;
  art: Readonly<{source?: AmbientArtSource; foreground?: ArtworkSource; ref: RefObject<View | null>; onReady: (art: ArtworkSource) => void; onError: (art: ArtworkSource) => void}>;
  /** Where the page was scrolled, kept by the title controller (Back to the page returns there). */
  scroll: Readonly<{offset: number; onOffset: (y: number) => void}>;
  /** The frame's `onEnd`: on a show's Episodes, the season's next page of episodes (the old screen's scroll paging). */
  onEnd?: () => void;
  kicker?: TitleKicker;
  facts: readonly FactPart[];
  primary?: TitlePrimary;
  actions: readonly TitleAction[];
  tagline?: string;
  synopsis?: string;
  credits: readonly Readonly<{label: string; names: string}>[];
  tabs: Readonly<{value: NativeTitleTab; items: readonly TitleTab[]; onValueChange: (value: string) => void}>;
  related: readonly PosterRowProps[];
  details: TitleDetailsProps;
  /** Cast & crew's See all: everyone, paged from the server. */
  people: PeopleSheetProps;
  /** A show (or a season or episode page): the season bar, the season's episodes with the opened one, the season sheet. */
  episodes?: Readonly<{bar: Readonly<{label: string; count?: string; onSeasons: () => void; hideWatched: boolean; onHideWatched: (value: boolean) => void; settings?: Readonly<{label: string}>}>;
    list: EpisodeListProps; sheet: SeasonSheetProps;
    /** TV (detail-tv-5a, 5b): the season column chooses a season as focus reaches it (no sheet), the band describes the
     * episode in focus (the plate in `list.opened`), OK on a tile plays it, and Hold OK on a season offers its options. */
    tv: TVEpisodes}>;
  /** Loading with nothing to show yet, or the whole page failed. */
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
}>;

const t = defaultI18n.t;
const ratingKind = (provider: string) => provider === 'community' ? t('title.rating.community') : provider === 'critic' ? t('title.rating.critics') : provider;
/** An episode's own numbering, the shape the episode rows and the player label use. */
const numbering = (entry: ContentEntry) => entry.absoluteNumber !== undefined ? {mode: 'absolute' as const, number: entry.absoluteNumber}
  : entry.dateBased && entry.airDate ? {mode: 'date' as const, day: entry.airDate}
    : entry.seasonNumber === 0 ? {mode: 'special' as const} : {mode: 'seasonal' as const, season: entry.seasonNumber, episode: entry.episodeNumber};
const dateText = (value: string, locale: string, utc = false) => new Intl.DateTimeFormat(locale, {month: 'long', day: 'numeric', year: 'numeric', ...(utc ? {timeZone: 'UTC'} : {})}).format(new Date(value));

export function useTitle(props: NativeTitleScreenProps): TitleView {
  const {controller, text} = props, snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  useEffect(() => {void controller.open().catch(props.reportError);}, [controller, props.reportError]);
  const entry = snapshot.entry, detail = snapshot.detail?.data, locale = text.locale ?? 'en-US';
  // A show, season or episode page reads the show's workspace (its seasons and episodes); a film its detail.
  const show = ['show', 'season', 'episode'].includes(entry.kind), workspace = show ? snapshot.workspace?.data : undefined;
  const openEpisode = useSyncExternalStore(controller.episode.subscribe, controller.episode.getSnapshot);
  // The season sheet acts for the page it opened on, never a later one (the old screen's lease).
  const [seasonsLease, setSeasonsLease] = useState<NativeTitlePageLease | undefined>(undefined);
  // An episode's own route (a Search hit, a neighbour past the season's edge) lands on that episode: once its row is
  // listed, it opens in place and the page scrolls to it (desk, 19 Nov; Plex shows the episode). Only on a first visit:
  // a page with a place of its own to go back to keeps that place.
  const arrived = useRef(false), revealFor = useRef<string | undefined>(undefined);
  const arrival = entry.kind === 'episode' && !arrived.current && !snapshot.place.anchor?.offset ? snapshot.episodes?.find(row => row.id === entry.id) : undefined;
  useEffect(() => {
    if (!arrival) return;
    arrived.current = true; revealFor.current = arrival.id;
    if (openEpisode.entry?.id !== arrival.id) controller.episode.open(arrival);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [arrival?.id]);
  const fail = (error: unknown) => props.reportError(error);
  // Play opens the player from the title's art: the art box, measured at the press, while the art drawn is the art offered
  // and the page has not moved since (the old title screen's start motion).
  const artRef = useRef<View>(null), drawn = useRef<ArtworkSource | undefined>(undefined), moved = useRef(0), mounted = useRef(false), artListeners = useRef(new Set<() => void>());
  const tell = () => {for (const listener of [...artListeners.current]) listener();};
  useEffect(() => {mounted.current = true; return () => {mounted.current = false; tell();};}, [controller]);
  // Remove from Continue Watching on a removed title (states13), once, cancelled if the page goes.
  const removal = useRef<AbortController | undefined>(undefined), [removing, setRemoving] = useState(false), [removed, setRemoved] = useState(false);
  const [peopleOpen, setPeopleOpen] = useState(false);
  useEffect(() => () => removal.current?.abort(), []);
  const title = workspace?.show.title ?? entry.displayTitle ?? detail?.item.title ?? entry.title;
  const backdrop = workspace?.show.backdropUrl ?? detail?.item.backdropUrl ?? entry.backdropUrl;

  // Facts: year, genres, then a movie's length or a show's seasons (final3-tv-03 "1 season"); the rating as its outlined tag
  // at the end (phone-detail d1).
  const contentRating = workspace?.show.contentRating ?? detail?.facts?.contentRating ?? entry.contentRating, duration = detail?.item.duration ?? entry.duration;
  const factSource = titleFactParts({year: workspace?.show.year ?? detail?.item.year ?? entry.year, durationSeconds: show ? undefined : duration,
    genres: workspace?.show.genres ?? detail?.metadata.genres.map(genre => genre.name) ?? entry.genres, contentRating, seasonCount: show ? workspace?.seasonTotalCount : undefined}, text, show ? 'home-show' : 'home-movie');
  const facts = [...factSource.filter(fact => fact.id !== 'content-rating'), ...(contentRating ? [{id: 'content-rating', text: contentRating, role: 'context' as const}] : [])];

  // Play or Resume, with the played part, the time left and Start over.
  const action = detail?.actions.find(value => value.enabled && value.playback && value.id !== 'start_over');
  // A show plays its next episode, named on the button ("Play S1 E1", phone-detail d2).
  const nextUp = show ? workspace?.nextUp : undefined, code = nextUp ? episodeLabel(numbering(nextUp), 'player', text)?.text : undefined;
  const playback = action?.playback ?? nextUp?.playback ?? entry.playback;
  const progressSeconds = nextUp ? nextUp.progressSeconds : detail?.personal.progressSeconds ?? entry.progressSeconds;
  const playDuration = nextUp ? nextUp.duration : duration;
  const resuming = !!playback?.startSeconds && (progressSeconds ?? 0) > 0 && !!playDuration;
  const startOver = detail?.actions.some(value => value.id === 'start_over' && value.enabled && value.playback);
  const foreground = props.artwork(backdrop);
  const startArt = (): NativeStartArtworkOffer | undefined => {
    const node = artRef.current, shown = drawn.current, at = moved.current;
    if (!mounted.current || !node || !foreground || shown?.cacheKey !== foreground.cacheKey || shown.uri !== foreground.uri) return undefined;
    const paintCurrent = () => mounted.current && artRef.current === node && moved.current === at && drawn.current === shown;
    const bounds = new Promise<Readonly<{x: number; y: number; width: number; height: number}> | undefined>(resolve => {
      try {node.measureInWindow((x, y, width, height) => resolve(paintCurrent() && [x, y, width, height].every(Number.isFinite) && width > 0 && height > 0 && y + height > 0 ? Object.freeze({x, y, width, height}) : undefined));}
      catch {resolve(undefined);}
    });
    const subscribe = (listener: () => void) => {artListeners.current.add(listener); if (!paintCurrent()) listener(); return () => {artListeners.current.delete(listener);};};
    return Object.freeze({artwork: foreground, bounds, paintCurrent, subscribe});
  };
  const play = (fromStart = false) => {void controller.play(fromStart ? entry : undefined, fromStart, fromStart ? undefined : startArt()).catch(fail);};
  const primary: TitlePrimary | undefined = playback ? {icon: 'i-play', label: code ? t(resuming ? 'title.resumeEpisode' : 'title.playEpisode', {code}) : t(resuming ? 'entry.resume' : 'entry.play'), onPress: () => play(), testID: 'watch-title-play',
    ...(resuming ? {progress: Math.min(1, progressSeconds! / playDuration!), remaining: t('next.kit.remaining', {time: durationText(Math.max(0, playDuration! - progressSeconds!), text.t)}),
      ...(startOver ? {startOver: {label: t('action.startOver'), onPress: () => play(true)}} : {})} : {})} : undefined;

  // My List, Watched (in More on a phone), Download, More: phone-detail d1.
  // A show's My List is the show's own (W-8), a film's its item action.
  const myList = props.myList ?? (show ? snapshot.showState ? {selected: snapshot.showState.watchlisted, change: (value: boolean) => controller.setShowMyList(value)} : undefined
    : detail?.actions.some(value => value.id === 'watchlist' && value.enabled)
    ? {selected: detail.personal.watchlisted, change: (value: boolean) => controller.personal({action: 'watchlist', value})} : undefined);
  // Watched is the film's. A show's page (also reached as a season or an episode) has none: an episode route's detail is
  // the episode's, and its watched lives in the episode's own menu and plate, not on the show's hero.
  const watched = !show && detail?.actions.some(value => value.id === 'watched' && value.enabled) ? detail.personal.watched : undefined;
  const actions: TitleAction[] = [
    ...(myList ? [{icon: myList.selected ? 'i-check' as const : 'i-plus' as const, label: t(myList.selected ? 'entry.removeFromWatchlist' : 'entry.addToWatchlist'), shortLabel: t('title.watchlist'), active: myList.selected, labelled: true, pinned: true,
      testID: 'watch-title-my-list', onPress: () => {void myList.change(!myList.selected).catch(fail);}}] : []),
    ...(watched !== undefined ? [{icon: 'i-check' as const, label: t(watched ? 'entry.markUnwatched' : 'entry.markWatched'), shortLabel: t('card.watched'), active: watched, labelled: true, wide: true,
      onPress: () => {void controller.personal({action: 'watched', value: !watched}).catch(fail);}}] : []),
    ...(props.download ? [{icon: 'i-dl' as const, label: t('next.cards.download'), labelled: true, onPress: () => props.download!(entry)}] : []),
    {icon: 'i-more', label: t('card.more', {title}), testID: 'watch-title-more', onPress: () => props.more(entry)},
  ];

  const collection = detail?.collections?.[0];
  const openCollection = collection && props.openCollection ? () => props.openCollection!({...collection, libraryId: detail!.scope.libraryId}) : undefined;
  const kicker: TitleKicker | undefined = collection ? {icon: 'i-layers', label: collection.name, detail: t('next.browse.filmCount', {count: collection.memberCount}), onPress: openCollection} : undefined;

  // People: the cast first, then the crew; directors and writers named under them.
  // A show's own workspace carries its credits (a credit's id is its person, who can hold two roles, so each is keyed by its place).
  const credits = show ? (workspace?.show.credits ?? []).map(credit => ({id: `show-credit-${credit.ordinal}`, ...(credit.id ? {personId: credit.id} : {}), name: credit.name,
    role: credit.role ?? '', department: credit.department ?? '', provider: '', ...(credit.portraitUrl ? {portraitUrl: credit.portraitUrl} : {})})) : detail?.metadata.credits ?? [];
  const department = (names: readonly string[]) => credits.filter(credit => names.includes(credit.department.toLowerCase()));
  const person = (credit: typeof credits[number]): CastPerson => ({id: credit.id, name: credit.name, role: credit.role || undefined, portrait: props.artwork(credit.portraitUrl),
    onPress: credit.personId && props.person ? () => props.person!(credit.personId!) : undefined});
  const cast = department(['acting', 'cast']), crew = credits.filter(credit => !cast.includes(credit));
  const names = (list: readonly {name: string}[]) => defaultI18n.list([...new Set(list.map(credit => credit.name))]);
  const directors = department(['directing', 'director', 'direction']), writers = department(['writing', 'writer', 'writers']);
  const creditLines = [...(directors.length ? [{label: t('title.directedBy', {names: ''}).trim(), names: names(directors)}] : []),
    ...(writers.length ? [{label: t('title.writtenBy', {names: ''}).trim(), names: names(writers)}] : [])];
  const castNames = [...new Set(cast.map(credit => credit.name))].slice(0, 3);
  // See all: the title's credits as the controller pages them (37 at a time), the cast then the crew.
  const paged = snapshot.credits, everyone = paged?.credits ?? credits;
  const everyoneCast = everyone.filter(credit => ['acting', 'cast'].includes(credit.department.toLowerCase()));
  const people: PeopleSheetProps = {open: peopleOpen, onOpenChange: setPeopleOpen, title: t('web.metadata.tabPeople'), back: {label: t('title.details'), onPress: () => setPeopleOpen(false)},
    groups: [everyoneCast.map(person), everyone.filter(credit => !everyoneCast.includes(credit)).map(person)],
    ...(paged?.hasMore ? {more: {label: t('action.showMore'), loading: paged.loading, onPress: () => {void controller.moreCredits(paged).catch(fail);}}} : {}),
    ...(paged?.error ? {note: presentError(paged.error, 'detail').body} : {})};

  const showInfo = workspace?.show;
  const about = show ? [
    showInfo?.firstAired && {label: t('title.releaseDate'), values: [{text: dateText(showInfo.firstAired, locale, true)}]},
    showInfo?.genres?.length && {label: t('title.genres'), values: showInfo.genres.map(name => ({text: name}))},
    showInfo?.network && {label: t('title.network'), values: [{text: showInfo.network}]},
    showInfo?.studio && {label: t('title.studio'), values: [{text: showInfo.studio}]},
    showInfo?.addedAt && {label: t('title.added'), values: [{text: dateText(showInfo.addedAt, locale)}]},
  ].filter((row): row is {label: string; values: {text: string; onPress?: () => void}[]} => !!row) : [
    detail?.facts?.releaseDate && {label: t('title.releaseDate'), values: [{text: dateText(detail.facts.releaseDate, locale, true)}]},
    // A genre opens its Search genre page, drawn as a link (phone-detail d1 frame 2).
    detail?.metadata.genres.length && {label: t('title.genres'), values: detail.metadata.genres.map(genre => ({text: genre.name, ...(props.genre ? {onPress: () => props.genre!({id: genre.id, name: genre.name})} : {})}))},
    // "Community 8.7": the server names its rating's kind, not its source, so the page says which kind (phone-detail d1 frame 2 draws the source's name).
    detail?.metadata.ratings.length && {label: t('title.ratings'), values: [{text: detail.metadata.ratings.map(rating => `${ratingKind(rating.provider)} ${defaultI18n.number(rating.value, {maximumFractionDigits: 1})}`).join(', ')}]},
    detail?.facts?.studio && {label: t('title.studio'), values: [{text: detail.facts.studio}]},
    detail?.item.addedAt && {label: t('title.added'), values: [{text: dateText(detail.item.addedAt, locale)}]},
  ].filter((row): row is {label: string; values: {text: string; onPress?: () => void}[]} => !!row);
  const banner: CollectionCardProps | undefined = collection && openCollection ? {title: collection.name, kindLabel: t('next.home.collection'),
    // The count is the banner's body and, on a phone's link card, its one caption line (phone-detail d1 `.colc`).
    body: t('next.collection.filmsOnServer', {count: collection.memberCount}), caption: [{id: 'count', text: t('next.collection.filmsOnServer', {count: collection.memberCount}), role: 'context'}], covers: collection.posterUrls.slice(0, 2).map(url => props.artwork(url)).filter((art): art is ArtworkSource => !!art),
    backdrop: props.artwork(backdrop), onNavigate: openCollection} : undefined;

  // More like this: the title's related rows and the server's recommendations; each continues to its end (MOB-054).
  const related = [...(detail?.related?.rows ?? []), ...(snapshot.recommendationRows ?? [])].filter(row => row.entries.length > 0).map((row): PosterRowProps => {
    const pages = snapshot.related?.[row.id], entries = pages?.entries ?? row.entries;
    // A row the server calls "More like this" under the More like this tab is named for the title, as drawn ("More like Dune: Part Two").
    return {title: row.heading === t('next.controls.moreLikeThis') ? t('home.row.moreLike', {title}) : row.heading, testID: `watch-related-${row.id}`, items: entries.map(value => ({key: value.id, title: value.displayTitle ?? value.title,
      year: value.year !== undefined ? defaultI18n.number(value.year, {useGrouping: false}) : undefined, artwork: props.artwork(value.posterUrl), watched: value.watched,
      progress: value.duration && value.progressSeconds !== undefined ? Math.max(0, Math.min(1, value.progressSeconds / value.duration)) : undefined,
      selection: props.selection?.(value), onNavigate: () => props.navigate(value), onMore: anchor => props.more(value, anchor)})),
      ...(pages ? {paging: {hasMore: pages.hasMore, loading: pages.loading, failed: pages.failed, waitingId: pages.waitingId, fulfilled: pages.fulfilled,
        waitingItem: {key: pages.waitingId, title: '', onNavigate: () => {}}, requestNext: () => controller.moreRelated(row.id, pages)}} : {})};
  });

  const state = ((): TitleView['state'] => {
    const read = show ? snapshot.workspace : snapshot.detail, loaded = show ? workspace : detail, phase = read?.phase, error = read?.error;
    if (phase === 'loading' && !loaded) return {kind: 'loading'};
    if (!error || loaded) return undefined;
    const presented = presentError(error, 'detail'), captured = (entry.displayTitle || entry.title).trim();
    // states13: a permission refusal names nothing; a gone title is named from the captured entry and the saved server.
    if (presented.category === 'permission') return {icon: 'i-lock', title: presented.title, body: presented.body};
    const removeContinue = props.removeContinue, remove = () => {
      if (!removeContinue || removing) return;
      removal.current?.abort(); const lease = new AbortController(); removal.current = lease; setRemoving(true);
      void removeContinue(lease.signal).then(done => {if (done && !lease.signal.aborted) setRemoved(true);})
        .catch(error => {if (!lease.signal.aborted && (error as {name?: string}).name !== 'AbortError') fail(error);})
        .finally(() => {if (removal.current === lease) {removal.current = undefined; setRemoving(false);}});
    };
    if (presented.category === 'not-found' && captured && props.serverName) return {icon: 'i-not', title: t('next.states.removed', {title: captured, server: props.serverName}), body: t('next.states.removedBody'),
      ...(props.search ? {primaryAction: {id: 'search', icon: 'i-search', label: t('next.states.searchTitle', {title: captured}), onPress: () => props.search?.(captured)}} : {}),
      ...(removeContinue && !removed ? {secondaryAction: {id: 'remove-continue', label: t('next.states.removeContinue'), disabled: removing, onPress: remove}} : {})};
    return {icon: 'i-warn', title: presented.title, body: presented.body, primaryAction: {id: 'retry', label: t('action.tryAgain'), onPress: () => {void (show ? controller.workspace.retry() : controller.detail.refresh()).catch(fail);}}};
  })();

  const airDay = (date: string) => new Intl.DateTimeFormat(locale, {month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC'}).format(new Date(`${date}T00:00:00Z`));
  // The season bar, the season's episodes (the opened one with its plate) and the season sheet: phone-detail d2.
  function episodesView(): TitleView['episodes'] {
    const space = workspace!, seasons = snapshot.seasons ?? [], selected = seasons.find(season => season.id === space.selected.seasonId);
    const group = space.selected.group ? space.groups.find(value => value.id === space.selected.group) : undefined;
    const label = group ? group.title : selected ? selected.title || t('title.season', {number: selected.number}) : t('title.seasons');
    const count = group ? group.count > 0 ? t('count.episodes', {count: group.count}) : undefined : selected?.episodeCount !== undefined ? t('count.episodes', {count: selected.episodeCount}) : undefined;
    const opened = openEpisode.entry?.kind === 'episode' ? openEpisode.entry : undefined, lease = () => controller.pageLease();
    const items = (snapshot.episodes ?? []).map(episode => {
      const copy = cardText({id: episode.id, kind: episode.kind, title: episode.displayTitle ?? episode.title, episode: numbering(episode), durationSeconds: episode.duration, positionSeconds: episode.progressSeconds}, 'in-show', text);
      // phone-detail d2 frame 4: "Episode 4" under the title, then its length and air date apart ("46 min  Feb 5, 2023"); the kit
      // places them per form (a tablet's length is on the picture and the date follows the number, tablet f7, f8).
      const length = episode.duration ? durationText(episode.duration, t) : undefined, aired = episode.airDate ? airDay(episode.airDate) : undefined;
      return {key: episode.id, title: episode.displayTitle ?? episode.title, label: copy.caption, length, aired, synopsis: episode.overview, progress: copy.progress, watched: episode.watched,
        status: episode.id === nextUp?.id ? t('player.queue') : undefined,
        artwork: props.artwork(episode.stillUrl ?? space.show.backdropUrl), backdropFallback: !episode.stillUrl, selection: props.selection?.(episode),
        onNavigate: () => controller.episode.open(episode), onMore: (anchor: React.RefObject<View | null>) => props.more(episode, anchor),
        ...(props.download ? {onDownload: () => props.download!(episode)} : {})};
    });
    const plate = opened ? episodePlate(opened, space.show) : undefined;
    // The season sheet: each season with its count and watched progress, then the owner's episode groups (kit/layout/ShowSeasonsSheet.tsx's rows).
    const rows: SeasonSheetRow[] = [...seasons.map(season => {
      const watched = season.watchedCount, total = season.episodeCount, complete = watched !== undefined && total !== undefined && total > 0 && watched >= total;
      const partial = watched !== undefined && total !== undefined && !complete && watched > 0;
      return {id: season.id, number: season.number, label: season.title || t('title.season', {number: season.number}), count: total !== undefined ? t('count.episodes', {count: total}) : undefined,
        progress: complete ? t('card.watched') : partial ? t('title.seasonProgress', {watched, total}) : undefined, complete, fraction: partial && total ? watched! / total : undefined};
    }), ...space.groups.map(value => ({id: `group:${value.id}`, label: value.title, count: value.count > 0 ? t('count.episodes', {count: value.count}) : undefined}))];
    const close = () => setSeasonsLease(undefined);
    return {
      bar: {label, count, onSeasons: () => setSeasonsLease(lease()), hideWatched: snapshot.place.hideWatched,
        onHideWatched: value => {const held = lease(); if (held) void controller.hideWatched(value, held).catch(fail);}, ...(props.owner ? {settings: {label: t('card.more', {title})}} : {})},
      list: {items, ...(plate && opened ? {opened: {key: opened.id, ...plate}} : {})},
      sheet: {open: !!seasonsLease, onOpenChange: value => {if (value) setSeasonsLease(lease()); else close();}, rows, total: space.seasonTotalCount ?? seasons.length,
        selectedId: group ? `group:${group.id}` : space.selected.seasonId ?? null,
        onSelect: id => {const held = seasonsLease; close(); if (!held) return; void (id.startsWith('group:') ? controller.group(id.slice(6), held) : controller.season(id, held)).catch(fail);},
        onMore: () => {void controller.moreSeasons().catch(fail);}},
      tv: tvEpisodes(rows, seasons, group ? `group:${group.id}` : space.selected.seasonId ?? null),
    };
  }
  /** TV: the same rows with each season's year and overview for the band; choosing a season or group takes the page's
   * lease at that moment (there is no sheet to hold one), and an episode is opened for the band, played, or closed by key. */
  function tvEpisodes(rows: readonly SeasonSheetRow[], seasons: readonly WorkspaceSeason[], selectedId: string | null): TVEpisodes {
    const byId = new Map(seasons.map(season => [season.id, season])), episodes = snapshot.episodes ?? [], find = (key: string) => episodes.find(episode => episode.id === key);
    const tiles = episodes.map((episode): TVTile => {
      const copy = cardText({id: episode.id, kind: episode.kind, title: episode.displayTitle ?? episode.title, episode: numbering(episode), durationSeconds: episode.duration, positionSeconds: episode.progressSeconds}, 'in-show', text);
      return {key: episode.id, title: episode.displayTitle ?? episode.title, label: copy.caption, length: episode.duration ? durationText(episode.duration, t) : undefined,
        status: episode.id === nextUp?.id ? t('player.post.upNext') : undefined, watched: episode.watched, progress: copy.progress,
        artwork: props.artwork(episode.stillUrl ?? workspace?.show.backdropUrl), backdropFallback: !episode.stillUrl, onMore: anchor => props.more(episode, anchor)};
    });
    return {
      tiles, nextUp: nextUp && episodes.some(episode => episode.id === nextUp.id) ? nextUp.id : episodes[0]?.id,
      seasons: rows.map(row => {const season = byId.get(row.id); return {id: row.id, label: row.label, count: row.count, progress: row.progress, fraction: row.fraction, complete: !!row.complete,
        current: row.id === selectedId, group: row.id.startsWith('group:'), year: season?.firstAired?.slice(0, 4), overview: season?.overview || undefined};}),
      select: id => {if (id === selectedId) return; const lease = controller.pageLease(); if (!lease) return;
        void (id.startsWith('group:') ? controller.group(id.slice(6), lease) : controller.season(id, lease)).catch(fail);},
      focusEpisode: key => {const episode = key ? find(key) : undefined; if (episode) controller.episode.open(episode); else controller.episode.close();},
      play: key => {const episode = find(key); if (episode) void controller.play(episode).catch(fail);},
      playSeason: id => {void controller.playSeason(id).catch(fail);},
      markSeason: (id, watched) => {void controller.markSeason(id, watched).catch(fail);},
      moreSeasons: () => {void controller.moreSeasons().catch(fail);},
    };
  }
  /** The opened episode (phone-detail d2 frame 4): its own detail read by the controller, the row's words meanwhile. */
  function episodePlate(episode: ContentEntry, showInfo: Readonly<{id: string; title: string; libraryId: string}>): EpisodePlateProps {
    const data = openEpisode.data, personal = data?.personal, episodeCredits = data?.metadata.credits ?? [];
    const of = (list: readonly string[]) => [...new Set(episodeCredits.filter(credit => list.includes(credit.department.toLowerCase())).map(credit => credit.name))];
    const guests = episodeCredits.filter(credit => ['acting', 'cast'].includes(credit.department.toLowerCase()));
    const length = (data?.item.duration ?? episode.duration) !== undefined ? durationText(data?.item.duration ?? episode.duration!, t) : undefined;
    const airDate = episode.airDate ?? data?.item.episode?.airDate, rating = data?.facts?.contentRating ?? episode.contentRating;
    const watchedNow = personal?.watched ?? episode.watched, progressNow = personal?.progressSeconds ?? episode.progressSeconds ?? 0, resume = progressNow > 0 && !watchedNow;
    const neighbour = (value: Readonly<{id: string; number: number; title: string}> | undefined, direction: 'previous' | 'next') => value ? {
      label: t(direction === 'previous' ? 'title.previousEpisode' : 'title.nextEpisode'), title: value.title,
      onPress: () => props.navigate({id: value.id, kind: 'episode', title: value.title, libraryId: showInfo.libraryId, show: {id: showInfo.id, name: showInfo.title}} as ContentEntry)} : undefined;
    // The still, then the code, title and facts: date, length, rating (phone-detail d2 frame 4 has no length on the still).
    // A neighbour in the loaded season opens in place, as a row does; past the season's edge the server's opens its page.
    const list = snapshot.episodes ?? [], at = list.findIndex(row => row.id === episode.id);
    const inList = (step: -1 | 1) => {
      const row = at >= 0 ? list[at + step] : undefined;
      return row ? {label: t(step < 0 ? 'title.previousEpisode' : 'title.nextEpisode'), title: row.displayTitle ?? row.title, onPress: () => controller.episode.open(row)} : undefined;
    };
    return {still: props.artwork(data?.item.stillUrl ?? episode.stillUrl ?? workspace?.show.backdropUrl),
      code: episodeLabel(numbering(episode), 'player', text)?.text, title: episode.displayTitle ?? episode.title,
      facts: [...(airDate ? [{id: 'date', text: airDay(airDate), role: 'identity' as const}] : []),
        ...(length ? [{id: 'length', text: length, role: 'context' as const}] : []), ...(rating ? [{id: 'content-rating', text: rating, role: 'context' as const}] : [])],
      synopsis: data?.item.overview ?? episode.overview,
      primary: {icon: 'i-play', label: t(resume ? 'entry.resume' : 'entry.play'), onPress: () => {void controller.episode.play().catch(fail);},
        ...(resume ? {startOver: {label: t('action.startOver'), onPress: () => {void controller.episode.play(true).catch(fail);}}} : {})},
      actions: [...(data?.actions.some(value => value.id === 'watchlist' && value.enabled) ? [{icon: personal?.watchlisted ? 'i-check' as const : 'i-plus' as const,
        label: t(personal?.watchlisted ? 'entry.removeFromWatchlist' : 'entry.addToWatchlist'), active: !!personal?.watchlisted, onPress: () => {void controller.episode.personal({action: 'watchlist', value: !personal?.watchlisted}).catch(fail);}}] : []),
        ...(props.download ? [{icon: 'i-dl' as const, label: t('next.cards.download'), onPress: () => props.download!(episode)}] : []),
        {icon: 'i-more' as const, label: t('card.more', {title: episode.displayTitle ?? episode.title}), onPress: () => props.more(episode)}],
      credits: [...(guests.length ? [{label: t('next.watch.guestStars'), people: guests.map(credit => ({name: credit.name, ...(credit.role ? {role: t('next.watch.asCharacter', {character: credit.role})} : {}),
        ...(credit.personId && props.person ? {onPress: () => props.person!(credit.personId!)} : {})}))}] : []),
        ...(of(['directing', 'director', 'direction']).length ? [{label: t('title.directedBy', {names: ''}).trim(), people: of(['directing', 'director', 'direction']).map(name => ({name}))}] : []),
        ...(of(['writing', 'writer', 'writers']).length ? [{label: t('title.writtenBy', {names: ''}).trim(), people: of(['writing', 'writer', 'writers']).map(name => ({name}))}] : [])],
      showAll: count => t('shelf.seeAllCount', {count}),
      previous: inList(-1) ?? neighbour(data?.episodeDetails?.previous, 'previous'), next: inList(1) ?? neighbour(data?.episodeDetails?.next, 'next'),
      close: {label: t('action.close'), onPress: () => {revealFor.current = undefined; controller.episode.close();}}, reveal: revealFor.current === episode.id,
      ...(openEpisode.phase === 'loading' ? {state: {kind: 'loading' as const}} : openEpisode.phase === 'error' ? {state: {kind: 'error' as const, icon: 'i-warn' as const, title: presentError(openEpisode.error, 'detail').title,
        action: {label: t('action.tryAgain'), onPress: () => controller.episode.retry()}}} : {}),
      testID: `watch-episode-plate-${episode.id}`};
  }

  const anchor = snapshot.place.anchor;
  return {retired: snapshot.retired, entry, title, kicker,
    art: {source: props.ambient(backdrop), foreground, ref: artRef, onReady: art => {drawn.current = art;}, onError: art => {if (drawn.current === art) {drawn.current = undefined; tell();}}},
    scroll: {offset: anchor && !anchor.itemId ? anchor.offset : 0, onOffset: y => {moved.current++; tell(); controller.remember({itemId: null, offset: y});}}, facts, primary, actions,
    tagline: detail?.facts?.tagline || undefined, synopsis: workspace?.show.overview ?? detail?.item.overview ?? entry.overview,
    credits: [...(directors.length ? [{label: t('title.directedBy', {names: ''}).trim(), names: names(directors)}] : []), ...(castNames.length ? [{label: t('title.starring', {names: ''}).trim(), names: defaultI18n.list(castNames)}] : [])],
    tabs: {value: !show && snapshot.place.tab === 'episodes' ? 'related' : snapshot.place.tab, onValueChange: value => controller.tab(value as NativeTitleTab),
      items: [...(show ? [{value: 'episodes' as const, label: t('title.episodes')}] : []), {value: 'related', label: t('next.controls.moreLikeThis')}, {value: 'details', label: t('title.details')}]},
    episodes: workspace ? episodesView() : undefined,
    ...(workspace && snapshot.place.tab === 'episodes' ? {onEnd: () => {void controller.moreEpisodes().catch(fail);}} : {}),
    related,
    details: {about: about.length ? {title: t('next.show.about'), rows: about} : undefined,
      people: credits.length ? {title: t('web.metadata.tabPeople'), groups: [cast.map(person), crew.map(person)], lines: creditLines,
        seeAll: {label: t('action.seeAll'), onPress: () => setPeopleOpen(true)}} : undefined, collection: banner},
    people,
    state};
}
