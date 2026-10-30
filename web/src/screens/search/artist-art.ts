import type {SearchEntry, SearchGroupResult} from '@core/search.ts';

/** Frame 4 (phone-web): an artist with no portrait is drawn as a circle cut from one of
 * their own albums. The album comes from this answer's server projection and matches the
 * album's explicit artist link by id in the artist's own library, so nothing is inferred
 * from a name. Artists that do have a portrait, or answers without one of their albums,
 * keep the existing circle. */
export function artistAlbumArt(artist: SearchEntry, groups: readonly SearchGroupResult[]): string | undefined {
  if (artist.kind !== 'artist' || artist.posterUrl || !artist.libraryId) return undefined;
  for (const group of groups) {
    if (group.id !== 'albums') continue;
    for (const album of group.items) {
      if (album.kind !== 'album' || !album.posterUrl || album.libraryId !== artist.libraryId) continue;
      if (album.artist?.id === artist.id) return album.posterUrl;
    }
  }
  return undefined;
}
