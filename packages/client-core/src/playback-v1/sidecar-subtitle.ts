import type {HttpLocalApi} from '../index.ts';
import type {Presentation} from './types.ts';

export type PresentedSidecarSubtitle = Readonly<{trackId: string; fileRevision: number; documentUrl: string; offsetMs: number}>;

/** The WebVTT file an app draws for the subtitle a presentation puts in force (`delivery: 'sidecar'`):
 * its track, the file's revision from its `…/{n}.vtt` address, that address resolved through the
 * captured route, and its offset. Undefined when nothing is shown, the track is not the app's to
 * draw, or the session lists no such file. */
export function presentedSidecarSubtitle(api: Pick<HttpLocalApi, 'mediaUrl'>, presentation: Presentation): PresentedSidecarSubtitle | undefined {
  const shown = presentation.subtitle;
  if (shown?.delivery !== 'sidecar') return undefined;
  const file = presentation.subtitles.find(value => value.trackId === shown.trackId);
  const fileRevision = Number(/\/([1-9][0-9]*)\.vtt$/.exec(file?.url ?? '')?.[1] ?? 0);
  if (!file || !Number.isSafeInteger(fileRevision) || fileRevision < 1) return undefined;
  return {trackId: shown.trackId, fileRevision, documentUrl: api.mediaUrl(file.url), offsetMs: file.offsetMs ?? 0};
}
