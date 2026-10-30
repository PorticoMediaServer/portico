import {ReaderError} from '../errors.ts';
import {spreadFits, compactLandscape, creaseSpread, validateViewport} from '../layout.ts';
import type {ReaderViewport} from '../ports.ts';
/** Narrow engine input. This is structurally satisfied by the canonical fifteen
 * property Reading facade; no defaults, raw registry keys or write API here. */
export type EpubDisplayPreferences = Readonly<{
  theme: 'dark' | 'black' | 'sepia' | 'light'; font: 'publisher' | 'literata' | 'manrope';
  textSize: number; lineSpacing: 'tight' | 'normal' | 'loose'; margins: 'narrow' | 'normal' | 'wide';
  alignment: 'left' | 'justified'; layout: 'pages' | 'scroll'; twoPages: boolean;
  hyphenation: boolean; tapToTurn: boolean; highlightColour: 'yellow' | 'green' | 'blue' | 'pink';
}>;
const palettes = {dark: ['#0c151d','#d4dde4'], black: ['#000000','#c9cfd3'], sepia: ['#f1e6d1','#443629'], light: ['#fbf9f5','#1d252c']} as const;
const colours = {yellow:'#d2b45b', green:'#75ad86', blue:'#75a9cf', pink:'#c88daa'} as const;
export function epubDisplay(preferences: EpubDisplayPreferences, viewport: ReaderViewport, fonts: Readonly<{literata: string; manrope: string}>) {
  validateViewport(viewport);
  const p = preferences;
  if (!palettes[p.theme] || !['publisher','literata','manrope'].includes(p.font) || !Number.isInteger(p.textSize) || p.textSize<1 || p.textSize>7
    || !['tight','normal','loose'].includes(p.lineSpacing) || !['narrow','normal','wide'].includes(p.margins)
    || !['left','justified'].includes(p.alignment) || !['pages','scroll'].includes(p.layout) || !colours[p.highlightColour]
    || [p.twoPages,p.hyphenation,p.tapToTurn].some(v => typeof v !== 'boolean')) throw new ReaderError('invalid_preferences');
  const phoneLandscape = compactLandscape(viewport), compact = viewport.width < 600 || phoneLandscape;
  const [background, ink] = palettes[p.theme];
  const sizes = phoneLandscape ? [11,12,13,14,16,19,22] : compact ? [12,13,14,15,17,20,23] : [14,16,17,18,20,23,26];
  const size = sizes[p.textSize-1];
  const normalLineHeight = phoneLandscape ? 26/16 : compact ? 28/17 : 1.6;
  const lineHeight = normalLineHeight * {tight: 0.875, normal: 1, loose: 1.15625}[p.lineSpacing];
  const normalMargin = phoneLandscape ? 56 : compact ? 24 : viewport.width < 1024 ? 84 : viewport.width >= 1400 ? 150 : 76;
  // W07: where the host draws page arrows, a single column starts clear of them (the mock-up's 120 px inset), also
  // in the narrower area beside an open Contents panel. Keyed on the arrows, not on the area's width.
  const spread = p.layout === 'pages' && p.twoPages && (spreadFits(viewport) || creaseSpread(viewport));
  const clearance = !spread && Number.isFinite(viewport.turnClearance) ? Math.max(0, viewport.turnClearance!) : 0;
  const margin = Math.max(normalMargin * {narrow:0.65,normal:1,wide:1.35}[p.margins], clearance);
  const fold = creaseSpread(viewport);
  const columns = p.layout === 'pages' && p.twoPages && (spreadFits(viewport) || compactLandscape(viewport) || creaseSpread(viewport)) ? 2 : 1;
  const top = phoneLandscape ? 40 : compact ? 86 : 100;
  const bottom = phoneLandscape ? 38 : compact ? 86 : viewport.width < 1024 ? 120 : viewport.width >= 1400 ? 96 : 112;
  // foldable6: a book fold keeps the drawn72 pt gutter, and the crease sits inside it.
  const columnGap = columns === 2 ? fold ? 72 : phoneLandscape ? 44 : viewport.width >= 1400 ? 104 : 100 : 0;
  const contentWidth = viewport.width - 2*margin + columnGap;
  // Foliate adds half a column gap to each outer edge. Compensate in its
  // container width, then invert g/(1-g) to obtain the exact drawn gap.
  // Trusted font URLs are created from app-supplied font Blobs, never book text.
  const fontFaces = `@font-face{font-family:Literata;src:url("${fonts.literata}")}@font-face{font-family:Manrope;src:url("${fonts.manrope}")}`;
  const family = p.font === 'publisher' ? '' : `font-family:${p.font === 'literata' ? 'Literata,serif' : 'Manrope,sans-serif'}!important;`;
  const css = `${fontFaces}\nhtml{color:${ink}!important;background:${background}!important;color-scheme:${p.theme==='dark'||p.theme==='black'?'dark':'light'};}
body{${family}font-size:${size}px!important;line-height:${lineHeight}!important;text-align:${p.alignment==='justified'?'justify':'left'}!important;hyphens:${p.hyphenation?'auto':'none'}!important;-webkit-hyphens:${p.hyphenation?'auto':'none'}!important;color:${ink}!important;background:transparent!important;font-optical-sizing:auto;}
p{margin:0;text-indent:1.6em;}p:first-of-type{ text-indent:0; }
::selection{background:${colours[p.highlightColour]}88;}
a{color:inherit;}img,svg{max-width:100%;max-height:100%;}*{animation:none!important;transition:none!important;}`;
  return Object.freeze({background, ink, columns, margin, top, bottom, css, flow:p.layout==='pages'?'paginated':'scrolled',
    maxInlineSize: contentWidth / columns, gap: columnGap ? 100 * columnGap / (contentWidth + columnGap) : 0});
}
