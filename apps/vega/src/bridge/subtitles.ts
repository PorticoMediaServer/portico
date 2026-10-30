/**
 * The subtitle track the shared player selects, as the Apple and Android engines show it
 * (ENGINE-CONTRACT.md §7): the server's cue document, under the playback session's own media
 * grant, is fetched, checked again every 25 seconds (the answer is also the proof the session may
 * still show it), and the cues that cover the current position are the text on screen.
 * Authorisation lapses 40 seconds after the last good answer: a revoked session's subtitles go
 * dark with its video. Nothing here is drawn by the media element, so the text looks the same
 * whatever is playing.
 */
import {trace} from '../debug';
import {checkResponse, onCredentialExposure, originOf, requestCredentials} from './credential-boundary';
import {BYTE_LIMIT, NOTHING, defaultCaptionStyle, displayAt, parseCueDocument, selectionOf, type CaptionStyle, type CueList, type Selection, type SubtitleCommand, type SubtitleDisplay} from './subtitle-cues';

export type {CaptionStyle, SubtitleCommand, SubtitleDisplay};

// The other engines' words: the shared player and its tests match on them.
const UNAVAILABLE = 'Subtitles are unavailable or access was revoked.';
const UNREADABLE = 'Subtitles could not be read. Check the server connection.';
const INVALID = 'The server returned invalid subtitle timing or text.';
const TOO_LARGE = 'Subtitle data exceeded the supported limit.';
const RECHECK_MS = 25000, AUTHORIZED_MS = 40000, RETRY_MS = 5000;

type Shown = Readonly<{display: SubtitleDisplay; style: CaptionStyle}>;
let shown: Shown = Object.freeze({display: NOTHING, style: defaultCaptionStyle});
const listeners = new Set<() => void>();

let selection: Selection | null = null;
let cues: CueList | null = null;
let etag = '';
let authorizedUntil = 0, lastRequest = 0, generation = 0, positionUs = 0;
let request: XMLHttpRequest | undefined;
let timer: ReturnType<typeof setInterval> | undefined;
let report: (error: string) => void = () => {};

function show(display: SubtitleDisplay, style: CaptionStyle = shown.style): void {
  const next = display.text ? display : NOTHING;
  // A preference change to any field of the look repaints at once, not at the next cue.
  if (next.text === shown.display.text && next.italic === shown.display.italic && next.top === shown.display.top && style.scale === shown.style.scale && style.background === shown.style.background &&
    style.color === shown.style.color && style.position === shown.style.position && style.font === shown.style.font) return;
  if (next.text !== shown.display.text) trace(`subtitle ${next.text ? JSON.stringify(next.text.slice(0, 60)) : 'cleared'}`);
  shown = Object.freeze({display: next, style});
  for (const listener of [...listeners]) listener();
}

/** The line on screen and how to draw it, for the `PorticoPlayer` view. */
export const subtitleDisplay = {
  subscribe(listener: () => void): () => void { listeners.add(listener); return () => { listeners.delete(listener); }; },
  current: (): Shown => shown,
};

/** Turns subtitles off. */
export function clearSubtitles(): void {
  generation++;
  try { request?.abort(); } catch { /* finished */ }
  request = undefined;
  if (timer) clearInterval(timer);
  timer = undefined;
  selection = null;
  cues = null;
  etag = '';
  authorizedUntil = 0;
  lastRequest = 0;
  show(NOTHING);
}

/**
 * Applies the player's subtitle choice for the media at `source`. `status` is told when a track
 * is taken up (an empty string) or cannot be (why).
 */
export function applySubtitles(command: SubtitleCommand | null | undefined, source: string, status: (error: string) => void): void {
  report = status;
  if (!command || command.mode !== 'track') { if (selection || cues) clearSubtitles(); else show(NOTHING); return; }
  const next = selectionOf(command, source);
  if ('error' in next) { clearSubtitles(); status(next.error); return; }
  const current = selection;
  // An older choice for the same playback never replaces a newer one.
  if (current && current.binding === next.binding && next.revision < current.revision) return;
  const same = !!current && current.url === next.url && current.binding === next.binding;
  const refresh = !same || current!.refreshRevision !== next.refreshRevision;
  if (!same) clearSubtitles();
  selection = next;
  timer ??= setInterval(() => {
    const now = Date.now();
    if (now > authorizedUntil) show(NOTHING);
    if (!request && now - lastRequest >= RECHECK_MS) fetchDocument();
  }, 1000);
  if (refresh) fetchDocument(); else tickSubtitles(positionUs / 1e6);
}

function fetchDocument(): void {
  const wanted = selection;
  if (!wanted || request) return;
  lastRequest = Date.now();
  const attempt = generation;
  const fetching = new XMLHttpRequest();
  request = fetching;
  const mine = () => request === fetching && attempt === generation;
  /** `transient`: the server could not be asked. A grant already held stays good until it lapses. */
  const failed = (message: string, transient: boolean) => {
    if (!mine()) return;
    request = undefined;
    if (transient) { lastRequest = Date.now() - (RECHECK_MS - RETRY_MS); tickSubtitles(positionUs / 1e6); report(message); return; }
    cues = null; etag = ''; authorizedUntil = 0;
    show(NOTHING);
    report(message);
  };
  try { requestCredentials(wanted.url, {}, null); } catch { failed(UNAVAILABLE, false); return; }
  fetching.open('GET', wanted.url);
  fetching.withCredentials = false;
  fetching.setRequestHeader('Accept', 'application/json');
  fetching.setRequestHeader('Cache-Control', 'no-store');
  // The validator of the document already held, so "still authorised, unchanged" is one header.
  if (etag && cues) fetching.setRequestHeader('If-None-Match', etag);
  fetching.timeout = 20000;
  fetching.onreadystatechange = () => {
    if (!mine() || fetching.readyState < 2 || fetching.status === 0) return;
    try { checkResponse(wanted.url, fetching.responseURL, fetching.status, true); } catch { failed(UNAVAILABLE, false); fetching.abort(); return; }
    if (Number(fetching.getResponseHeader('Content-Length')) > BYTE_LIMIT || (fetching.responseText?.length ?? 0) > BYTE_LIMIT) { failed(TOO_LARGE, false); fetching.abort(); }
  };
  fetching.onprogress = fetching.onreadystatechange;
  fetching.onload = () => {
    if (!mine()) return;
    if (fetching.status === 0 && !fetching.responseURL) { failed(UNREADABLE, true); return; }
    // The SDK has already followed; discard its answer and contain any grant exposure.
    try { checkResponse(wanted.url, fetching.responseURL, fetching.status, true); } catch { failed(UNAVAILABLE, false); return; }
    const status = fetching.status;
    if (status === 304 && etag && cues) { request = undefined; authorizedUntil = Date.now() + AUTHORIZED_MS; report(''); tickSubtitles(positionUs / 1e6); return; }
    if (status >= 500 && status <= 599) { failed(UNREADABLE, true); return; }
    const json = String(fetching.getResponseHeader('Content-Type') ?? '').split(';')[0].trim().toLowerCase() === 'application/json';
    if (status !== 200 || !json) { failed(UNAVAILABLE, false); return; }
    const body = String(fetching.responseText ?? '');
    if (body.length > BYTE_LIMIT) { failed(TOO_LARGE, false); return; }
    const parsed = parseCueDocument(body);
    if (!parsed) { failed(INVALID, false); return; }
    request = undefined;
    const tag = fetching.getResponseHeader('ETag');
    etag = tag && tag.length <= 256 ? tag : '';
    cues = parsed;
    authorizedUntil = Date.now() + AUTHORIZED_MS;
    report('');
    tickSubtitles(positionUs / 1e6);
  };
  fetching.onerror = fetching.ontimeout = () => failed(UNREADABLE, true);
  try { fetching.send(); } catch { failed(UNREADABLE, true); }
}

/** Shows the cues that cover `seconds` of the media's own timeline. */
export function tickSubtitles(seconds: number): void {
  positionUs = Math.round(seconds * 1e6);
  if (!selection || !cues || Date.now() > authorizedUntil) { show(NOTHING, selection?.style ?? shown.style); return; }
  show(displayAt(cues, positionUs, selection.offsetUs), selection.style);
}

onCredentialExposure(({origin, origins = [origin]}) => {
  if (selection && origins.includes(originOf(selection.url))) clearSubtitles();
});
