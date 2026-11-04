import {memberUnreadable} from '@portico/reader-core';
import type {ReadingSession} from '@portico/client-core';

/** One page tile in the owner's "choose cover from pages" picker. `url` is absent for a page
 * the server rejected as content (RD-2, 422 `reading_member_unreadable`): it draws as a plain
 * tile that can't be chosen, and the picker carries on with the other pages. */
export type CoverPagePreview = Readonly<{index: number; url?: string}>;

/** Reads one page's thumbnail; every failure other than an unreadable page propagates. The
 * caller owns and revokes a returned `url`. */
export async function coverPagePreview(session: Pick<ReadingSession, 'page'>, index: number, signal: AbortSignal): Promise<CoverPagePreview> {
  try {
    const image = await session.page(index, {width: 180, signal});
    return Object.freeze({index, url: URL.createObjectURL(new Blob([image.bytes.slice() as Uint8Array<ArrayBuffer>], {type: image.mime}))});
  } catch (error) {
    if (!memberUnreadable(error) || signal.aborted) throw error;
    return Object.freeze({index});
  }
}
