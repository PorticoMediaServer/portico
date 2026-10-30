import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeDownloadRequest} from '../downloads/request';

/** The title page's real download intent: a single item, or the page's own
 * container. Kinds the download port does not cover (artist, category, extra)
 * raise no intent, so the drawn button cannot open an empty sheet. */
export function nativeDownloadRequest(entry: ContentEntry): NativeDownloadRequest | undefined {
  const title = entry.displayTitle ?? entry.title;
  if (!title) return;
  if (entry.kind === 'show' || entry.kind === 'season' || entry.kind === 'album' || entry.kind === 'playlist')
    return Object.freeze({title, container: Object.freeze({kind: entry.kind, id: entry.id})});
  if (entry.kind === 'movie' || entry.kind === 'episode' || entry.kind === 'book' || entry.kind === 'audiobook_file' || entry.kind === 'chapter')
    return Object.freeze({title, itemId: entry.id});
}
