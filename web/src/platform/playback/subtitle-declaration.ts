/**
 * What this browser draws itself, as told to the server: the one place the web player's
 * `subtitleRendering` list is decided and written into the capability document.
 *
 * The server burns a subtitle into the video only when the device has not said it can draw that
 * kind. The values are the protocol's: `text`, `ass`, `pgs`, `vobsub`, `dvb`.
 *
 * `declareSubtitleRendering` is called from this platform's `browserClientProfile`.
 * It writes `engines.native.subtitleRendering` when the
 * document has engines (the capability document), and the older `subtitles.styled` / `.bitmap`
 * lists when it does not.
 *
 * Styled and image subtitles are drawn on a canvas over the video (`subtitles/drawn-surface.ts`).
 * The player never hands its video to the system's full-screen player (on iPhone, full screen
 * fills Safari's window), so a drawn subtitle is seen wherever the picture is. Picture in Picture
 * and AirPlay cannot carry it; the player keeps the picture on the page for those.
 */
export type SubtitleRenderingKind = 'text' | 'ass' | 'pgs' | 'vobsub' | 'dvb';

export type RenderingFacts = {
  /** A canvas can be put over the video and workers exist. */
  canvas: boolean;
  /** WebAssembly can be compiled (styled subtitles need it). */
  webAssembly: boolean;
};

function browserFacts(): RenderingFacts {
  const safe = (probe: () => boolean) => { try { return probe(); } catch { return false; } };
  return {
    canvas: safe(() => typeof document !== 'undefined' && typeof document.createElement('canvas').getContext === 'function' && typeof Worker === 'function'),
    webAssembly: safe(() => typeof WebAssembly === 'object' && typeof WebAssembly.instantiate === 'function'),
  };
}

/** The subtitle kinds this browser draws: text always, the rest where the overlay canvas can. */
export function subtitleRenderingKinds(facts: RenderingFacts = browserFacts()): SubtitleRenderingKind[] {
  const kinds: SubtitleRenderingKind[] = ['text'];
  if (!facts.canvas) return kinds;
  if (facts.webAssembly) kinds.push('ass');
  kinds.push('pgs', 'vobsub', 'dvb');
  return kinds;
}

type Fields = Record<string, unknown>;
const record = (v: unknown): v is Fields => !!v && typeof v === 'object' && !Array.isArray(v);

/** The capability document with this browser's drawn subtitle kinds written in. */
export function declareSubtitleRendering<T>(document: T, kinds: readonly SubtitleRenderingKind[] = subtitleRenderingKinds()): T {
  if (!record(document)) return document;
  if (record(document.engines)) {
    const engines: Fields = {};
    for (const [name, engine] of Object.entries(document.engines)) engines[name] = record(engine) ? {...engine, subtitleRendering: [...kinds]} : engine;
    return {...document, engines} as T;
  }
  const before = record(document.subtitles) ? document.subtitles : {};
  return {...document, subtitles: {...before, styled: kinds.includes('ass') ? ['ass', 'ssa'] : [], bitmap: kinds.filter(kind => kind === 'pgs' || kind === 'vobsub' || kind === 'dvb')}} as T;
}
