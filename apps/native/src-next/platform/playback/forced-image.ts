import type {SubtitleRender} from './engine-media';

/** Only the server-offered count belonging to this actual image file and media
 * grant. Return that offered path; never synthesize a request for missing data. */
export function forcedImageLinesPath(mediaUrl: string, render: SubtitleRender | undefined,
  resource: Readonly<{id: string; revision: number}> | undefined, offered: string | undefined): string | undefined {
  if (!render?.url || !resource || !Number.isSafeInteger(resource.revision) || resource.revision < 1
    || !offered || !['pgs', 'vobsub'].includes(render.kind)) return;
  if ([mediaUrl, render.url, offered].some(value => value.length > 2048 || /[\x00-\x20\x7f\\]/.test(value))) return;
  if (render.url.includes('%') || offered.includes('%') || /(?:^|\/)\.{1,2}(?:\/|$)/.test(render.url)) return;
  try {
    const media = new URL(mediaUrl), file = new URL(render.url);
    if (!['https:', 'http:'].includes(media.protocol) || file.origin !== media.origin
      || media.username || media.password || media.hash || file.username || file.password || file.search || file.hash) return;
    const grant = /^\/v1\/media\/([A-Za-z0-9_-]{1,256})(?:\/|$)/.exec(media.pathname)?.[1];
    const image = /^\/v1\/media\/subtitles\/([A-Za-z0-9_-]{1,256})\/([A-Za-z0-9_-]{1,128})\/([1-9]\d{0,17})\/original\.(sup|idx)$/.exec(file.pathname);
    if (!grant || !image || image[1] !== grant || image[2] !== resource.id || image[3] !== String(resource.revision)
      || image[4] !== (render.kind === 'pgs' ? 'sup' : 'idx')) return;
    if (offered !== file.pathname.replace(/\/original\.(?:sup|idx)$/, '/lines')) return;
    return offered;
  } catch { return; }
}
