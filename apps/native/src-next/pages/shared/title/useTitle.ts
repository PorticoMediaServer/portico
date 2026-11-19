import {useEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import type {View} from 'react-native';
import type {NativeStartArtworkOffer} from '../../../platform/playback/start-presentation';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {titleFactParts, type FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import type {CollectionCardProps} from '../../../kit/cards';
import type {PageStateProps, TitleAction, TitleKicker, TitlePrimary, TitleDetailsProps, CastPerson} from '../../../kit/frames';
import type {PosterRowProps} from '../../../kit/layout';
import type {NativeTitleScreenProps} from '../../../screens/watch/TitleScreen';
import type {NativeTitleTab} from '../../../screens/watch/title-memory';

export type TitleTab = Readonly<{value: NativeTitleTab; label: string}>;
/** What a film's title page shows, from the title controller (the same reads and commands the old screen used).
 * No layout: the touch and TV pages draw it on `TitleFrame`. */
export type TitleView = Readonly<{
  retired: boolean;
  entry: ContentEntry;
  title: string;
  art: Readonly<{source?: AmbientArtSource; foreground?: ArtworkSource; ref: RefObject<View | null>; onReady: (art: ArtworkSource) => void; onError: (art: ArtworkSource) => void}>;
  /** Where the page was scrolled, kept by the title controller (Back to the page returns there). */
  scroll: Readonly<{offset: number; onOffset: (y: number) => void}>;
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
  /** Loading with nothing to show yet, or the whole page failed. */
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
}>;

const t = defaultI18n.t;
const ratingKind = (provider: string) => provider === 'community' ? t('title.rating.community') : provider === 'critic' ? t('title.rating.critics') : provider;
const dateText = (value: string, locale: string, utc = false) => new Intl.DateTimeFormat(locale, {month: 'long', day: 'numeric', year: 'numeric', ...(utc ? {timeZone: 'UTC'} : {})}).format(new Date(value));

export function useTitle(props: NativeTitleScreenProps): TitleView {
  const {controller, text} = props, snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  useEffect(() => {void controller.open().catch(props.reportError);}, [controller, props.reportError]);
  const entry = snapshot.entry, detail = snapshot.detail?.data, locale = text.locale ?? 'en-US';
  const fail = (error: unknown) => props.reportError(error);
  // Play opens the player from the title's art: the art box, measured at the press, while the art drawn is the art offered
  // and the page has not moved since (the old title screen's start motion).
  const artRef = useRef<View>(null), drawn = useRef<ArtworkSource | undefined>(undefined), moved = useRef(0), mounted = useRef(false), artListeners = useRef(new Set<() => void>());
  const tell = () => {for (const listener of [...artListeners.current]) listener();};
  useEffect(() => {mounted.current = true; return () => {mounted.current = false; tell();};}, [controller]);
  // Remove from Continue Watching on a removed title (states13), once, cancelled if the page goes.
  const removal = useRef<AbortController | undefined>(undefined), [removing, setRemoving] = useState(false), [removed, setRemoved] = useState(false);
  useEffect(() => () => removal.current?.abort(), []);
  const title = entry.displayTitle ?? detail?.item.title ?? entry.title;
  const backdrop = detail?.item.backdropUrl ?? entry.backdropUrl;

  // Facts: year, genres, length; the rating as its outlined tag at the end (phone-detail d1).
  const contentRating = detail?.facts?.contentRating ?? entry.contentRating, duration = detail?.item.duration ?? entry.duration;
  const factSource = titleFactParts({year: detail?.item.year ?? entry.year, durationSeconds: duration,
    genres: detail?.metadata.genres.map(genre => genre.name) ?? entry.genres, contentRating}, text, 'home-movie');
  const facts = [...factSource.filter(fact => fact.id !== 'content-rating'), ...(contentRating ? [{id: 'content-rating', text: contentRating, role: 'context' as const}] : [])];

  // Play or Resume, with the played part, the time left and Start over.
  const action = detail?.actions.find(value => value.enabled && value.playback && value.id !== 'start_over');
  const playback = action?.playback ?? entry.playback;
  const progressSeconds = detail?.personal.progressSeconds ?? entry.progressSeconds;
  const resuming = !!playback?.startSeconds && (progressSeconds ?? 0) > 0 && !!duration;
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
  const primary: TitlePrimary | undefined = playback ? {icon: 'i-play', label: t(resuming ? 'entry.resume' : 'entry.play'), onPress: () => play(), testID: 'watch-title-play',
    ...(resuming ? {progress: Math.min(1, progressSeconds! / duration!), remaining: t('next.kit.remaining', {time: durationText(Math.max(0, duration! - progressSeconds!), text.t)}),
      ...(startOver ? {startOver: {label: t('action.startOver'), onPress: () => play(true)}} : {})} : {})} : undefined;

  // My List, Watched (in More on a phone), Download, More: phone-detail d1.
  const myList = props.myList ?? (detail?.actions.some(value => value.id === 'watchlist' && value.enabled)
    ? {selected: detail.personal.watchlisted, change: (value: boolean) => controller.personal({action: 'watchlist', value})} : undefined);
  const watched = detail?.actions.some(value => value.id === 'watched' && value.enabled) ? detail.personal.watched : undefined;
  const actions: TitleAction[] = [
    ...(myList ? [{icon: myList.selected ? 'i-check' as const : 'i-plus' as const, label: t(myList.selected ? 'entry.removeFromWatchlist' : 'entry.addToWatchlist'), active: myList.selected, labelled: true, pinned: true,
      testID: 'watch-title-my-list', onPress: () => {void myList.change(!myList.selected).catch(fail);}}] : []),
    ...(watched !== undefined ? [{icon: 'i-check' as const, label: t(watched ? 'entry.markUnwatched' : 'entry.markWatched'), active: watched, labelled: true, wide: true,
      onPress: () => {void controller.personal({action: 'watched', value: !watched}).catch(fail);}}] : []),
    ...(props.download ? [{icon: 'i-dl' as const, label: t('next.cards.download'), labelled: true, onPress: () => props.download!(entry)}] : []),
    {icon: 'i-more', label: t('card.more', {title}), testID: 'watch-title-more', onPress: () => props.more(entry)},
  ];

  const collection = detail?.collections?.[0];
  const openCollection = collection && props.openCollection ? () => props.openCollection!({...collection, libraryId: detail!.scope.libraryId}) : undefined;
  const kicker: TitleKicker | undefined = collection ? {icon: 'i-layers', label: collection.name, detail: t('next.browse.filmCount', {count: collection.memberCount}), onPress: openCollection} : undefined;

  // People: the cast first, then the crew; directors and writers named under them.
  const credits = detail?.metadata.credits ?? [];
  const department = (names: readonly string[]) => credits.filter(credit => names.includes(credit.department.toLowerCase()));
  const person = (credit: typeof credits[number]): CastPerson => ({id: credit.id, name: credit.name, role: credit.role || undefined, portrait: props.artwork(credit.portraitUrl),
    onPress: credit.personId && props.person ? () => props.person!(credit.personId!) : undefined});
  const cast = department(['acting', 'cast']), crew = credits.filter(credit => !cast.includes(credit));
  const names = (list: readonly {name: string}[]) => defaultI18n.list([...new Set(list.map(credit => credit.name))]);
  const directors = department(['directing', 'director', 'direction']), writers = department(['writing', 'writer', 'writers']);
  const creditLines = [...(directors.length ? [{label: t('title.directedBy', {names: ''}).trim(), names: names(directors)}] : []),
    ...(writers.length ? [{label: t('title.writtenBy', {names: ''}).trim(), names: names(writers)}] : [])];
  const castNames = [...new Set(cast.map(credit => credit.name))].slice(0, 3);

  const about = [
    detail?.facts?.releaseDate && {label: t('title.releaseDate'), values: [{text: dateText(detail.facts.releaseDate, locale, true)}]},
    detail?.metadata.genres.length && {label: t('title.genres'), values: detail.metadata.genres.map(genre => ({text: genre.name}))},
    // "Community 8.7": the server names its rating's kind, not its source, so the page says which kind (phone-detail d1 frame 2 draws the source's name).
    detail?.metadata.ratings.length && {label: t('title.ratings'), values: [{text: detail.metadata.ratings.map(rating => `${ratingKind(rating.provider)} ${defaultI18n.number(rating.value, {maximumFractionDigits: 1})}`).join(', ')}]},
    detail?.facts?.studio && {label: t('title.studio'), values: [{text: detail.facts.studio}]},
    detail?.item.addedAt && {label: t('title.added'), values: [{text: dateText(detail.item.addedAt, locale)}]},
  ].filter((row): row is {label: string; values: {text: string}[]} => !!row);
  const banner: CollectionCardProps | undefined = collection && openCollection ? {title: collection.name, kindLabel: t('next.home.collection'),
    body: t('next.collection.filmsOnServer', {count: collection.memberCount}), covers: collection.posterUrls.slice(0, 2).map(url => props.artwork(url)).filter((art): art is ArtworkSource => !!art),
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
    const phase = snapshot.detail?.phase, error = snapshot.detail?.error;
    if (phase === 'loading' && !detail) return {kind: 'loading'};
    if (!error || detail) return undefined;
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
    return {icon: 'i-warn', title: presented.title, body: presented.body, primaryAction: {id: 'retry', label: t('action.tryAgain'), onPress: () => {void controller.detail.refresh().catch(fail);}}};
  })();

  const anchor = snapshot.place.anchor;
  return {retired: snapshot.retired, entry, title, kicker,
    art: {source: props.ambient(backdrop), foreground, ref: artRef, onReady: art => {drawn.current = art;}, onError: art => {if (drawn.current === art) {drawn.current = undefined; tell();}}},
    scroll: {offset: anchor && !anchor.itemId ? anchor.offset : 0, onOffset: y => {moved.current++; tell(); controller.remember({itemId: null, offset: y});}}, facts, primary, actions,
    tagline: detail?.facts?.tagline || undefined, synopsis: detail?.item.overview ?? entry.overview,
    credits: [...(directors.length ? [{label: t('title.directedBy', {names: ''}).trim(), names: names(directors)}] : []), ...(castNames.length ? [{label: t('title.starring', {names: ''}).trim(), names: defaultI18n.list(castNames)}] : [])],
    tabs: {value: snapshot.place.tab === 'episodes' ? 'related' : snapshot.place.tab, onValueChange: value => controller.tab(value as NativeTitleTab),
      items: [{value: 'related', label: t('next.controls.moreLikeThis')}, {value: 'details', label: t('title.details')}]},
    related,
    details: {about: about.length ? {title: t('next.show.about'), rows: about} : undefined,
      people: credits.length ? {title: t('web.metadata.tabPeople'), groups: [cast.map(person), crew.map(person)], lines: creditLines} : undefined, collection: banner},
    state};
}
