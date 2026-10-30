import type {MessageId} from '@i18n';
import type {Library} from '@core/index.ts';

const types: readonly Readonly<{kind: string; label: MessageId}>[] = [
  {kind: 'movie', label: 'search.chip.movies'}, {kind: 'tv', label: 'next.browse.tvShows'},
  {kind: 'anime', label: 'next.browse.kind.anime'}, {kind: 'audiobook', label: 'search.chip.audiobooks'},
];
export type ScopeType = Readonly<{kind: string; label: MessageId; libraryIds: readonly string[]}>;
/** Kind comes from authorized library facts, never a name, item id or page sample. */
export function scopeTypes(libraries: readonly Library[]): readonly ScopeType[] {
  return types.flatMap(type => {
    const libraryIds = libraries.filter(library => library.kind === type.kind).map(library => library.id);
    return libraryIds.length ? [{...type, libraryIds}] : [];
  });
}
