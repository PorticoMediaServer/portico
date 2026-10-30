import type {RefObject} from 'react';
import type {View} from 'react-native';
import type {NativeSelectionActionPresentation} from '../library/selection-actions';
/** One download intent raised from a title page. The shell owns the sheet, so a
 * title renderer only names the real item (or container) and its drawn title. */
export type NativeDownloadContainerKind = 'show' | 'season' | 'album' | 'book' | 'playlist';
export type NativeDownloadRequest = Readonly<{
  title: string;
  queryAction?: NativeSelectionActionPresentation;
  /** Actual Reading publications use their own original-version choice, never a video ladder. */
  reading?: true;
  /** The single item to ask the server's real quality menu for. */
  itemId?: string;
  /** A whole show/season/album/book/playlist goes through the durable request. */
  container?: Readonly<{kind: NativeDownloadContainerKind; id: string}>;
  /** The real control the page raised this from, for an anchored presentation. */
  anchorRef?: RefObject<View | null>;
  /** The original page lifetime. A later route or viewer cannot take over the
   * intent, its version choice or its preparation. */
  lifetime?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
}>;

let current: NativeDownloadRequest | undefined;
const openingKeys = new WeakMap<NativeDownloadRequest, number>();
let nextOpeningKey = 0;
const listeners = new Set<() => void>();
const publish = () => {for (const listener of [...listeners]) listener();};

/** A newer intent replaces the sheet's request; the sheet is never stacked. */
export function askNativeDownload(request: NativeDownloadRequest): void {
  if (!request.title || !(request.itemId || request.container?.id || request.queryAction)) return;
  const previous = current;
  current = Object.freeze({...request,
    ...(request.container ? {container: Object.freeze({...request.container})} : {}),
    ...(request.lifetime ? {lifetime: Object.freeze({...request.lifetime})} : {}),
  });
  openingKeys.set(current, ++nextOpeningKey);
  if (previous?.queryAction && previous.queryAction !== current.queryAction) previous.queryAction.dispose();
  publish();
}

/** Presentation keys distinguish repeated equal-valued requests; they confer no authority. */
export function nativeDownloadRequestKey(request: NativeDownloadRequest): number | undefined {
  return openingKeys.get(request);
}

export function closeNativeDownload(expected?: NativeDownloadRequest): boolean {
  if (expected && current !== expected) return false;
  if (!current) return false;
  const closing = current;
  current = undefined;
  closing.queryAction?.dispose();
  publish();
  return true;
}

export function getNativeDownloadSnapshot(): NativeDownloadRequest | undefined {
  return current;
}

export function subscribeNativeDownload(listener: () => void): () => void {
  listeners.add(listener);
  return () => {listeners.delete(listener);};
}
