/**
 * The one place a subtitle track meets the video element.
 *
 * Today a subtitle is the server's cue document drawn on a text track this surface creates and
 * owns. A renderer for styled (ASS) or image (PGS, VobSub) subtitles attaches here too: it is
 * given the same element, the same `timeline` offset and the same show/clear calls, and nothing
 * else in the player touches `textTracks` for a title's subtitles.
 *
 * `timeline` comes from the media player port: hls.js may place a stream's first frame at a time
 * other than its playlist time, so cue times (title time) are shifted by that offset; `null`
 * means "not known yet" and nothing is shown.
 *
 * Styled and image subtitles are drawn on a canvas by `subtitles/overlay.ts`. The surface passes
 * the same `timeline` facts on to it (`companion`), so the port still tells one object.
 */
import {captionStyle} from '../../kit/media/caption-style';
import type {SubtitleTimeline} from './web-media-player.ts';
import type {SubtitlePreferences} from '@core/preference-effects.ts';

export type SurfaceCue = Readonly<{startUs: string | number; endUs: string | number; text: string; italic?: boolean; top?: boolean}>;

/** The parts of a media element a surface uses. */
export type SubtitleHost = {
  ownerDocument?: Document;
  setAttribute?(name: string, value: string): void;
  removeAttribute?(name: string): void;
  addTextTrack(kind: 'subtitles', label: string, language: string): TextTrackLike;
  readonly textTracks: ArrayLike<{kind: string; mode: string}> & {addEventListener?(type: string, fn: () => void): void; removeEventListener?(type: string, fn: () => void): void};
};
export type TextTrackLike = {mode: string; readonly cues: ArrayLike<unknown> | null; addCue(cue: unknown): void; removeCue(cue: unknown): void};
type CueLike = {align: string; snapToLines: boolean; line: number | string};
export type CueFactory = (start: number, end: number, text: string) => CueLike;

/** Whether the browser can lift its cue box above the player's controls with CSS; Firefox cannot. */
function cssLiftsCues(): boolean {
  try { return typeof CSS !== 'undefined' && CSS.supports('selector(::-webkit-media-text-track-container)'); } catch { return false; }
}

export class SubtitleSurface implements SubtitleTimeline {
  private readonly host: SubtitleHost;
  private readonly makeCue: CueFactory;
  private readonly lift: boolean;
  private track?: TextTrackLike;
  private cues: readonly SurfaceCue[] | null = null;
  private delay = 0;
  private offset: number | null = 0;
  private disposed = false;
  private preferences?: SubtitlePreferences;
  private style?: HTMLStyleElement;
  private styleKey?: string;
  private readonly exclusive = () => {
    // Only this surface's track shows a title's subtitles; an engine's own renditions stay off.
    const list = this.host.textTracks;
    for (let i = 0; i < list.length; i++) { const t = list[i]!; if ((t as unknown) !== this.track && (t.kind === 'subtitles' || t.kind === 'captions') && t.mode !== 'disabled') t.mode = 'disabled'; }
  };

  private readonly companion?: SubtitleTimeline;

  constructor(host: SubtitleHost, options: {cue?: CueFactory; lift?: boolean; companion?: SubtitleTimeline} = {}) {
    this.host = host;
    this.companion = options.companion;
    this.makeCue = options.cue ?? ((start, end, text) => new VTTCue(start, end, text) as unknown as CueLike);
    this.lift = options.lift ?? !cssLiftsCues();
  }

  /** Whether this browser can draw text subtitles at all. */
  static supported(host: {addTextTrack?: unknown} | null): boolean {
    return !!host && typeof host.addTextTrack === 'function' && typeof VTTCue === 'function';
  }

  /** Native cues delegate accessibility styling to the browser. There is no browser API that
   * reads the OS caption style, so opting in removes app CSS rather than inventing an OS value. */
  applyPreferences(preferences: SubtitlePreferences | undefined): void {
    if (JSON.stringify(this.preferences) === JSON.stringify(preferences)) return;
    this.preferences = preferences;
    this.style?.remove(); this.style = undefined;
    if (preferences && !preferences.useSystemCaptionStyle && this.host.ownerDocument && this.host.setAttribute) {
      const key = this.styleKey ??= 'portico-cue-' + (++styleSerial);
      this.host.setAttribute('data-portico-caption-style', key);
      this.style = this.host.ownerDocument.createElement('style');
      const nonce = this.host.ownerDocument.querySelector<HTMLMetaElement>('meta[name="portico-style-nonce"]')?.content;
      if (nonce && nonce !== '__PORTICO_STYLE_NONCE__') this.style.nonce = nonce;
      this.style.textContent = captionStyle(key, preferences);
      this.host.ownerDocument.head.append(this.style);
    } else this.host.removeAttribute?.('data-portico-caption-style');
    this.render();
  }

  /** Show these cues, each shifted by `delaySeconds` (the viewer's timing correction). */
  show(cues: readonly SurfaceCue[], delaySeconds: number): void {
    this.cues = cues;
    this.delay = delaySeconds;
    this.render();
  }

  /** Take the subtitle off the picture. */
  clear(): void {
    this.cues = null;
    this.render();
  }

  /** While this surface shows a title's subtitles, an engine's own subtitle renditions stay off. */
  guard(on: boolean): void {
    const list = this.host.textTracks;
    if (on) { list.addEventListener?.('change', this.exclusive); list.addEventListener?.('addtrack', this.exclusive); this.exclusive(); }
    else { list.removeEventListener?.('change', this.exclusive); list.removeEventListener?.('addtrack', this.exclusive); }
  }

  // SubtitleTimeline (the media player port)
  reset(): void { this.companion?.reset(); this.offset = null; this.render(); }
  timeline(offsetSeconds: number | null): void {
    this.companion?.timeline(offsetSeconds);
    if (this.offset === offsetSeconds) return;
    this.offset = offsetSeconds;
    this.render();
  }

  dispose(): void {
    if (this.disposed) return;
    this.guard(false);
    this.cues = null;
    this.render();
    this.disposed = true;
    this.style?.remove(); this.host.removeAttribute?.('data-portico-caption-style');
  }

  private own(): TextTrackLike {
    return this.track ??= this.host.addTextTrack('subtitles', 'Portico subtitles', 'und');
  }

  private render() {
    if (this.disposed) return;
    if (!this.track && !this.cues) return;
    const track = this.own();
    // A disabled track lists no cues (`cues` is null), so they are removed while it can still
    // name them; otherwise every render stacks another copy of each line.
    if (track.mode === 'disabled') track.mode = 'hidden';
    if (track.cues) for (const cue of Array.from(track.cues)) track.removeCue(cue);
    track.mode = 'disabled';
    const cues = this.cues, offset = this.offset;
    if (!cues || offset === null || !Number.isFinite(offset)) return;
    const shift = this.delay + offset;
    for (const cue of cues) {
      const start = Number(cue.startUs) / 1e6 + shift, end = Number(cue.endUs) / 1e6 + shift;
      if (end <= 0) continue;
      const words = cue.text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      const styling = this.preferences?.keepStyling !== false;
      const vtt = this.makeCue(Math.max(0, start), end, styling && cue.italic ? '<i>' + words + '</i>' : words);
      vtt.align = 'center';
      // A cue authored at the top of the picture (a sign, a second speaker) stays there.
      if (styling && cue.top || !this.preferences?.useSystemCaptionStyle && this.preferences?.position === 'top') { vtt.snapToLines = true; vtt.line = 0; }
      else if (!this.preferences?.useSystemCaptionStyle && this.preferences?.position === 'raised') { vtt.snapToLines = true; vtt.line = -6; }
      // Where the cue box cannot be lifted with CSS, cues sit a few lines up so the controls
      // never cover them.
      else if (this.lift) { vtt.snapToLines = true; vtt.line = -4; }
      track.addCue(vtt);
    }
    track.mode = 'showing';
  }
}

let styleSerial = 0;
