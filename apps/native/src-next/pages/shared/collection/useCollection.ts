import {useState, useSyncExternalStore, type ReactNode} from 'react';
import type {ViewStyle} from 'react-native';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {AmbientArtSource, ArtworkSource} from '../../../kit/artwork';
import {posterGridScroller, titleRowsScroller, type StateRowProps, type TitleCollapse} from '../../../kit/frames';
import type {MenuEntry} from '../../../kit/overlays';
import {useTheme} from '../../../kit/theme';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {sortLabel, years, type NativeCollectionScreenProps} from '../../../screens/collections/CollectionScreen';
import type {NativeCollectionSort} from '../../../screens/collections/controller';

const t = defaultI18n.t;
const keyOf = (entry: ContentEntry) => `${entry.kind}:${entry.id}`;
/** A collection's sorts in words: the read names them with keys the catalogue does not hold as sort names (`sort.custom`
 * has none, and `sort.title` is the sheet's own "Sort by"), so the page says what each one is. */
const sortName = (sort: NativeCollectionSort): string => ({
  custom: t('next.collection.sortCustom'), title: t('next.browse.native.title'), added: t('next.browse.native.added'),
  year: t('sort.releaseDate'), release: t('sort.releaseDate'), releaseDate: t('sort.releaseDate'),
} as Record<string, string | undefined>)[sort.id] ?? sortLabel(sort);
const length = (seconds: number | undefined) => seconds ? durationText(seconds, t) : undefined;

type Menu = Readonly<{title: string; items: readonly MenuEntry[]; open: boolean; onOpenChange: (open: boolean) => void}>;

/** What a collection's page shows (phone-detail d3 frame 8), from the old collection screen's read: data only, so the
 * touch page and a TV page draw the same collection. */
export type CollectionView = Readonly<{
  title: string; ambient?: AmbientArtSource; backdrop?: ArtworkSource;
  kicker: string; facts: readonly FactPart[];
  /** Portico made it from the films' details (the read says so): the frame's quiet note. */
  note?: string;
  heading: string;
  /** The read's sorts, when it offers more than one; Select on a tablet (a phone selects from a row's menu). */
  sort?: Readonly<{label: string; open: () => void}>; select?: Readonly<{label: string; onPress: () => void}>;
  state?: StateRowProps;
  /** The members, windowed: rows on a phone (frame 8), posters on a tablet (the old screen's grid). */
  scroller: (content: ReactNode, style: ViewStyle, collapse: TitleCollapse) => ReactNode;
  sortMenu: Menu; pageMenu: Menu;
}>;

export function useCollection({state, runtime, actions, selection, onPlay, onShuffle, onSort, onOpen, onRetry}: NativeCollectionScreenProps): CollectionView & Readonly<{play: () => void; shuffle: () => void}> {
  // A row's open menu and the selection redraw the rows that show them.
  useSyncExternalStore(actions?.menuState?.subscribe ?? noSubscribe, actions?.menuState?.getSnapshot ?? zero);
  useSyncExternalStore(selection?.subscribe ?? noSubscribe, selection?.getSnapshot ?? zero);
  const phone = useTheme().form === 'phone';
  const [sortOpen, setSortOpen] = useState(false), [pageOpen, setPageOpen] = useState(false);
  const backdrop = state.first?.backdropUrl, sort = state.sorts.find(value => value.id === state.sort);
  const count = t('next.browse.filmCount', {count: state.totalCount}), span = years(state);
  // The collection's own genres, else (when every film is in memory) the ones its films share most, three at most.
  const genres = state.projection?.entity?.genres?.length ? state.projection.entity.genres.slice(0, 3) : topGenres(state.complete);
  const failed = !!state.error && !state.totalCount, empty = state.phase === 'ready' && state.totalCount === 0;
  const retry = {label: t('action.tryAgain'), onPress: onRetry};
  const more = (entry: ContentEntry) => actions ? (anchor: Parameters<NonNullable<typeof actions>['menu']>[1]) => actions.menu(entry, anchor, selection?.item(entry)?.onEnter) : undefined;
  const select = selection?.start ? () => selection.start?.() : undefined;
  return {
    title: state.title, ambient: runtime.ambient(backdrop), backdrop: runtime.artwork(backdrop),
    kicker: t('next.home.collection'),
    // The count, the years and the genres (frame 8), each only when the read carries it.
    facts: [{id: 'count', text: count, role: 'identity'}, ...(span ? [{id: 'years', text: span, role: 'context' as const}] : []),
      ...(genres.length ? [{id: 'genres', text: genres.join(', '), role: 'context' as const}] : [])],
    note: state.projection?.entity?.origin === 'automatic' ? t('next.collection.automaticDescription') : undefined,
    heading: count,
    sort: state.sorts.length > 1 ? {label: sort ? sortName(sort) : t('web.filters.sortBy'), open: () => setSortOpen(true)} : undefined,
    select: !phone && select ? {label: t('next.selection.select'), onPress: select} : undefined,
    // The read failed with nothing shown, the collection is empty, or a later page failed (the members shown stay).
    state: state.phase === 'loading' && !state.totalCount ? {kind: 'loading'}
      : failed ? {kind: 'error', icon: 'i-warn', title: presentError(state.error, 'detail').title, action: retry}
      : empty ? {kind: 'empty', icon: 'i-layers', title: t('next.collection.empty')}
      : state.error ? {kind: 'error', icon: 'i-warn', title: presentError(state.error, 'detail').title, action: retry} : undefined,
    scroller: phone
      ? titleRowsScroller({collection: state.collection, getKey: keyOf, testID: 'collection-list', item: entry => {
        const progress = entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
        // Lengths read as on every title page ("1 h 21 min", durationText), not the Saved list's short form.
        return {title: entry.displayTitle || entry.title, facts: [entry.year === undefined ? undefined : String(entry.year), length(entry.duration)].filter((fact): fact is string => !!fact),
          watched: entry.watched, progress, remaining: progress !== undefined ? t('next.kit.remaining', {time: length(Math.max(0, (entry.duration ?? 0) - (entry.progressSeconds ?? 0))) ?? ''}) : undefined,
          artwork: runtime.artwork(entry.posterUrl), selection: selection?.item(entry), onNavigate: () => onOpen(entry), onMore: more(entry), testID: `collection-item-${entry.id}`};
      }})
      : posterGridScroller({collection: state.collection, getKey: keyOf, testID: 'collection-grid', item: entry => ({title: entry.displayTitle || entry.title,
        caption: entry.year === undefined ? undefined : String(entry.year), artwork: runtime.artwork(entry.posterUrl), watched: entry.watched,
        progress: entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined,
        selection: selection?.item(entry), onNavigate: () => onOpen(entry), onMore: more(entry)})}),
    sortMenu: {title: t('web.filters.sortBy'), open: sortOpen, onOpenChange: setSortOpen,
      items: state.sorts.map(value => ({id: value.id, label: sortName(value), selected: value.id === state.sort, onSelect: () => onSort(value.id, value.directions[0] ?? 'asc')}))},
    pageMenu: {title: state.title, open: pageOpen, onOpenChange: setPageOpen, items: [
      {id: 'play', label: t('next.cards.playAll'), icon: 'i-play', onSelect: onPlay},
      {id: 'shuffle', label: t('title.shuffle'), icon: 'i-shuffle', onSelect: onShuffle},
      ...(select ? [{id: 'select', label: t('next.selection.select'), icon: 'i-check' as const, onSelect: select}] : [])]},
    play: onPlay, shuffle: onShuffle,
  };
}

function topGenres(members: readonly ContentEntry[] | undefined): readonly string[] {
  if (!members?.length) return [];
  const counts = new Map<string, number>();
  for (const member of members) for (const genre of member.genres ?? []) counts.set(genre, (counts.get(genre) ?? 0) + 1);
  return [...counts.entries()].sort((a, b) => b[1] - a[1]).slice(0, 3).map(([genre]) => genre);
}

const noSubscribe = () => () => {};
const zero = () => 0;
