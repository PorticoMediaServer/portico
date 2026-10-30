import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {Theme} from '../../kit/theme';

/** Kinds LibraryCard draws on a square (album, song, book, artist circle, mosaic, radio). */
const square = new Set(['album', 'song', 'book', 'audiobook_file', 'artist', 'playlist', 'radio']);
const genreTile = (entry: ContentEntry) => entry.kind === 'category' && entry.navigation?.browse?.pivot === 'albums';

/** The fixed cell of one Discover shelf, so the shelf can mount only the cards in view
 * (LR05). A shelf of music genres is the drawn tile; otherwise the tallest card kind
 * in the shelf sets the art, and the caption keeps LibraryCard's two title lines. */
export function discoverCell(theme: Theme, width: number, entries: readonly ContentEntry[]): Readonly<{art: number; caption: number}> {
  if (entries.length && entries.every(genreTile)) return {art: theme.isTV ? 180 : 84, caption: 0};
  const poster = entries.some(entry => !square.has(entry.kind) && !genreTile(entry));
  const caption = theme.spec.card.titleMargin[theme.platform] + 2 * (theme.type.cardTitle.lineHeight ?? 28 * theme.textScale)
    + theme.spec.card.subtitleMargin[theme.platform] + (theme.type.caption.lineHeight ?? 24 * theme.textScale);
  return {art: poster ? width * 1.5 : width, caption};
}
