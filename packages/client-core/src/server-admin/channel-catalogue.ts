import type {BrowseFieldCapability} from '../browse.ts';
import {browseValueLabel} from '../presentation/browse-toolbar.ts';
import type {MessageId} from '../../../i18n/src/index.ts';

type Translate = (id: MessageId) => string;
/** Closed catalogue vocabulary; a future server field stays preserved but uneditable. */
const FIELD_MESSAGES: Readonly<Record<string, MessageId>> = {
  actor: 'filter.field.actor',
  added: 'filter.field.dateAdded',
  aired: 'filter.field.aired',
  album: 'filter.field.album',
  artist: 'filter.field.artist',
  audienceRating: 'filter.field.audienceRating',
  audioLanguage: 'filter.field.audioLanguage',
  author: 'filter.field.author',
  availability: 'filter.field.availability',
  cast: 'browse.field.cast',
  collection: 'filter.field.collection',
  communityRating: 'filter.field.audienceRating',
  contentRating: 'filter.field.contentRating',
  crew: 'browse.field.crew',
  criticRating: 'filter.field.criticRating',
  dateAdded: 'filter.field.dateAdded',
  decade: 'filter.field.decade',
  director: 'filter.field.director',
  discNumber: 'browse.field.discNumber',
  duration: 'filter.field.duration',
  durationMs: 'filter.field.duration',
  durationSeconds: 'filter.field.duration',
  dynamicRange: 'filter.field.dynamicRange',
  entityKind: 'filter.field.entityKind',
  episodeGroup: 'browse.field.episodeGroup',
  favorite: 'filter.field.favorite',
  genre: 'filter.field.genre',
  homeRow: 'browse.field.homeRow',
  label: 'filter.field.label',
  lastPlayed: 'filter.field.lastPlayed',
  lastPlayedAt: 'filter.field.lastPlayed',
  latestAired: 'filter.field.latestAired',
  library: 'browse.field.library',
  list: 'browse.field.list',
  match: 'filter.field.match',
  memberOf: 'browse.field.memberOf',
  narrator: 'filter.field.narrator',
  network: 'filter.field.network',
  person: 'filter.field.person',
  personalRating: 'filter.field.yourRating',
  playState: 'filter.field.playState',
  played: 'filter.field.played',
  rating: 'filter.field.rating',
  releaseDate: 'filter.field.releaseDate',
  resolution: 'filter.field.resolution',
  savedCollection: 'browse.field.savedCollection',
  search: 'browse.field.search',
  series: 'filter.field.series',
  studio: 'filter.field.studio',
  subtitleLanguage: 'browse.field.subtitleLanguage',
  tag: 'filter.field.tag',
  title: 'filter.field.title',
  userRating: 'filter.field.userRating',
  versions: 'browse.field.versions',
  watchlisted: 'filter.field.watchlisted',
  writer: 'filter.field.writer',
  year: 'filter.field.year',
  yourRating: 'filter.field.yourRating',
};
export function channelFieldMessage(field: Pick<BrowseFieldCapability, 'id'>): MessageId | undefined {
  return Object.hasOwn(FIELD_MESSAGES, field.id) ? FIELD_MESSAGES[field.id] : undefined;
}
export function channelFieldLabel(field: Pick<BrowseFieldCapability, 'id'>, t: Translate): string | undefined {
  const key = channelFieldMessage(field);return key === undefined ? undefined : t(key);
}
const VALUE_MESSAGES: Readonly<Record<string, MessageId>> = {true: 'web.filters.yes', false: 'web.filters.no'};
export function channelValueLabel(value: string, t: Translate): string {
  const key = Object.hasOwn(VALUE_MESSAGES, value) ? VALUE_MESSAGES[value] : undefined;
  return key === undefined ? browseValueLabel(value) : t(key);
}
