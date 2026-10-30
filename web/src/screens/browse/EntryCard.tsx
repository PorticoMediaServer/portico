import {defaultI18n} from '@i18n';
import {generatedCoverTint} from '@design';
import type {CSSProperties} from 'react';
import type {ContentEntry} from '@core/library-content.ts';
import {shapeFor} from '@core/presentation/card-shape.ts';
import {homeCardSubject} from '../home/model';
import {cardText} from '@portico/client-core/kit-model';
import type {TextEnvironment} from '@portico/client-core/kit-model';
import {GeneratedCover} from '../../kit/artwork';
import {CircleCard, CollectionTile, CoverCard, GenreTile, PosterCard, SquareCard, type CardSelection} from '../../kit/cards';
import type {ShellArtwork} from '../../shell/artwork';
import {collectionWatchedCount, collectionWatchedProgress} from './collection-card';

export type BrowseItemActions = Readonly<{
  onOpen: () => void;
  onPlay?: () => void;
  onMore?: (anchor: HTMLButtonElement) => void;
  moreExpanded?: boolean;
  onNotInterested?: () => void;
  selection?: CardSelection;
}>;
export type BrowseEntryCardProps = Readonly<{entry: ContentEntry; actions: BrowseItemActions; artwork: ShellArtwork; env: TextEnvironment;
  width: number; family?: string; mixed?: boolean; order: number; group: string; visible: boolean; backdrops: boolean}>;
/** Display fields never stand in for a navigation target or a permission offer. */
export function BrowseEntryCard({entry, actions, artwork, env, width, family, mixed, order, group, visible, backdrops}: BrowseEntryCardProps) {
  const title = entry.displayTitle ?? entry.title;
  const shape = shapeFor(entry.kind, 'grid');
  const ordinary = cardText({...homeCardSubject(entry), itemCount: ['collection', 'category', 'playlist'].includes(entry.kind) ? entry.count : undefined, albumCount: entry.albumCount, songCount: entry.kind === 'album' || entry.kind === 'disc' ? entry.count : undefined, countIsLowerBound: entry.countIsLowerBound}, 'ordinary', env);
  const kind = entry.kind === 'movie' ? 'next.browse.kind.movie' : entry.kind === 'show' && family === 'anime' ? 'next.browse.kind.anime' : entry.kind === 'show' && family === 'tv' ? 'next.browse.kind.show' : undefined;
  // Frame #collections draws the watched share after the years, and only when a film is watched.
  const watched = entry.kind === 'collection' ? collectionWatchedCount(entry) : undefined;
  const watchedProgress = entry.kind === 'collection' ? collectionWatchedProgress(entry) : undefined;
  const model = entry.kind === 'collection' && family === 'movie' ? {...ordinary, caption: entry.count === undefined ? [] : [{id: 'films', role: 'supplement' as const, text: defaultI18n.t(entry.countIsLowerBound ? 'next.browse.filmCountAtLeast' : 'next.browse.filmCount', {count: entry.count})}],
    detail: [...(entry.yearMin !== undefined && entry.yearMax !== undefined ? [{id: 'years', role: 'supplement' as const, text: env.t('next.kit.range', {start: String(entry.yearMin), end: String(entry.yearMax)})}] : []),
      ...(watched !== undefined && watched > 0 && entry.count !== undefined ? [{id: 'watched', role: 'supplement' as const, text: defaultI18n.t('next.browse.watchedOf', {watched, count: entry.count})}] : [])],
    ...(watchedProgress === undefined ? {} : {progress: watchedProgress})} :
    mixed && kind ? {...ordinary, caption: [...(entry.year !== undefined ? [{id: 'year', role: 'supplement' as const, text: String(entry.year)}] : []), {id: 'kind', role: 'supplement' as const, text: defaultI18n.t(kind)}]} : ordinary;
  const props = {model, context: 'grid' as const, visible, watched: entry.watched, isNew: entry.new,
    onNavigate: actions.onOpen, onPlay: actions.onPlay, onMore: actions.onMore, moreExpanded: actions.moreExpanded,
    onNotInterested: actions.onNotInterested, selection: actions.selection, style: {'--cards-width': `${width}px`} as CSSProperties,
    fallback: <GeneratedCover title={title} glyph={Array.from(title.trim())[0] ?? ''} tint={generatedCoverTint(title)}/>};
  const source = artwork.foreground(entry.posterUrl, width * devicePixelRatio, shape === 'poster' ? 2 / 3 : 1, {group, order});
  if (entry.kind === 'category' && family === 'music') return <GenreTile variant="music" context="grid" visible={visible} onNavigate={actions.onOpen} selection={actions.selection} style={props.style}
    model={{title, caption: entry.count === undefined ? [] : [{id: 'song-count', role: 'supplement', text: defaultI18n.t(entry.countIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: entry.count, pivot: 'songs'})}]}}
    covers={(entry.artworkPaths ?? []).slice(0, 3).flatMap((path, index) => {const cover = artwork.foreground(path, 76 * devicePixelRatio, 1, {group, order: order * 3 + index});return cover ? [cover] : [];})}/>;
  if (entry.kind === 'collection') return <CollectionTile {...props} artwork={backdrops ? artwork.foreground(entry.backdropUrl, width * devicePixelRatio, 16 / 9, {group, order}) : undefined}
    ambient={backdrops ? artwork.ambient(entry.backdropUrl, {group, order}) : undefined}
    covers={(entry.artworkPaths ?? []).slice(0, 4).flatMap((path, index) => {const cover = artwork.foreground(path, 90 * devicePixelRatio, 2 / 3, {group, order: order * 4 + index});return cover ? [cover] : [];})}/>;
  if (entry.kind === 'artist' || shape === 'circle') return <CircleCard {...props} covers={(entry.artworkPaths?.length ? entry.artworkPaths : entry.posterUrl ? [entry.posterUrl] : []).slice(0, 4).flatMap(path => {const cover = artwork.foreground(path, width * devicePixelRatio, 1, {group, order});return cover ? [cover] : [];})} initials={title.trim().split(/\s+/).slice(0, 2).map(word => Array.from(word)[0] ?? '').join('')} variant={entry.kind === 'artist' ? 'artist' : 'person'}/>;
  // desktop-read 2/2c: a Reading title is drawn as a book cover — 2:3, its own left spine, and
  // the typographic generated cover when the file carries no artwork. It never takes a play
  // action: an ebook or comic has no playback, and opening it is the detail page's own action.
  // The finished check is this profile's own mark on this exact row, read from the parser's
  // checked readingMark; the video watched flag and an explicit unread mark never draw it.
  if (entry.kind === 'ebook' || entry.kind === 'comic') return <CoverCard {...props} onPlay={undefined} watched={entry.readingMark?.read === true}
    watchedLabel={entry.readingMark?.read === true ? defaultI18n.t('next.cards.finished') : undefined} kind={entry.kind === 'comic' ? 'comic' : 'book'} artwork={source}
    generated={{title, glyph: Array.from(title.trim())[0] ?? '', tint: generatedCoverTint(title)}}/>;
  return shape === 'square' ? <SquareCard {...props} artwork={source} variant={entry.kind === 'book' || entry.kind === 'audiobook_file' ? 'audiobook' : 'music'}/> : <PosterCard {...props} artwork={source}/>;
}
