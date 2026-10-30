import {defaultI18n, type MessageId} from '../../../../packages/i18n/src';
import type {HomeSelectionMember} from '../../../../packages/client-core/src/home-selection/index';
import type {RepairFieldSpec} from '../../../../packages/client-core/src/metadata-repair';
import type {SelectionKind, SelectionSummary} from '../../../../packages/client-core/src/selection-model/index';
import {homeCardText} from '../screens/home/model';
import type {NativeShellRuntime} from './runtime';

const selectedKindLabels: Readonly<Partial<Record<SelectionKind, MessageId>>> = {
  movie: 'next.selection.movie', show: 'next.selection.show', season: 'next.selection.season', episode: 'next.selection.episode',
  artist: 'next.selection.artist', album: 'next.selection.album', song: 'next.selection.track', book: 'next.selection.book',
  audiobook_file: 'next.selection.audiobookPart', chapter: 'next.selection.chapter', collection: 'next.selection.collection',
  category: 'next.selection.category', author: 'next.selection.author', book_series: 'next.selection.bookSeries', disc: 'next.selection.disc', extra: 'next.selection.extra',
};

/** Name a kind only when the complete genuine summary agrees it covers every pick. */
export function selectedItemsLabel(summary: Pick<SelectionSummary, 'count' | 'types'>): string {
  const type = summary.types.length === 1 && summary.types[0].count === summary.count ? summary.types[0] : undefined;
  const key = type && Object.hasOwn(selectedKindLabels, type.kind) ? selectedKindLabels[type.kind] : undefined;
  return defaultI18n.t(key ?? 'next.selection.titles', {count: summary.count});
}

export function memberTitle(member: HomeSelectionMember, runtime: NativeShellRuntime): string | undefined {
  return member.entry ? homeCardText(member.entry, false, runtime.text).title : member.collection?.collection.name ?? member.channel?.channel.name;
}

/** Server fields require real catalogue labels; an unknown schema is never displayed as an id. */
export function metadataFieldLabel(field: RepairFieldSpec): string {
  const key = field.field === 'genres' ? 'web.metadata.genres' : field.field === 'metadataLanguage' ? 'web.libraries.metadataLanguage' : `web.metadata.field.${field.field}`;
  if (!defaultI18n.has(key)) throw new Error('metadata_field_copy_required');
  return defaultI18n.t(key as MessageId);
}
