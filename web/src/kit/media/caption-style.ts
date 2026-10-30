/** The drawn native-caption palette. Media preferences are independent of the app theme. */
export type CaptionAppearance = Readonly<{
  size:'small'|'medium'|'large'|'extra-large'; color:'white'|'yellow'|'cyan'|'green'|'pink';
  background:'none'|'outline'|'shadow'|'translucent'|'opaque';font:'default'|'serif'|'mono'|'rounded';
}>;
export type CaptionPaint = Readonly<{fontSize:string; color:string; backgroundColor:string; textShadow:string; fontFamily:string}>;
const sizes = {small:'80%',medium:'100%',large:'125%','extra-large':'150%'};
const colors = {white:'#fff',yellow:'#ff0',cyan:'#0ff',green:'#0f0',pink:'#ff80c0'};
const backgrounds = {none:'transparent',outline:'transparent',shadow:'transparent',translucent:'rgba(0,0,0,0.65)',opaque:'#000'};
const fonts = {default:'sans-serif',serif:'serif',mono:'monospace',rounded:'ui-rounded,system-ui,sans-serif'};
const shadows = {none:'none',outline:'-1px -1px 0 #000,1px -1px 0 #000,-1px 1px 0 #000,1px 1px 0 #000',shadow:'0 2px 3px #000',translucent:'none',opaque:'none'};
/** The one palette: cue CSS and the Settings preview both read these values. */
export function captionAppearance(preferences: CaptionAppearance): CaptionPaint {
  return {fontSize: sizes[preferences.size], color: colors[preferences.color], backgroundColor: backgrounds[preferences.background],
    textShadow: shadows[preferences.background], fontFamily: fonts[preferences.font]};
}
/** key is created internally by SubtitleSurface, never provided by a user or a subtitle. */
export function captionStyle(key: string, preferences: CaptionAppearance): string {
  const paint = captionAppearance(preferences);
  return `[data-portico-caption-style="${key}"]::cue {font-size:${paint.fontSize};color:${paint.color};background-color:${paint.backgroundColor};font-family:${paint.fontFamily};text-shadow:${paint.textShadow};}`;
}
