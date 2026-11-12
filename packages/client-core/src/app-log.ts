/** The app's own diagnostics. Only known events and facts enter the ring; raw messages,
 * exceptions, URLs, names and user input have no representation here. Nothing sends this log. */
export type AppLogLevel = 'info' | 'warn' | 'error';
export type AppLogArea = 'app' | 'screen' | 'playback' | 'network';
export type AppLogMessage = 'started' | 'screen' | 'playback' | 'stall' | 'network-failure' | 'warning' | 'error' | 'crash';
export const APP_LOG_BYTES = 1024 * 1024;
export const APP_LOG_AGE_MS = 7 * 24 * 60 * 60 * 1000;
const levels = new Set(['info','warn','error']), areas = new Set(['app','screen','playback','network']);
const messages = new Set(['started','screen','playback','stall','network-failure','warning','error','crash']);
const screens = new Set(['home','library','item','reader','search','settings','player','accounts','downloads','live-tv']);
const codes = new Set(['network_error','http_error','timeout','cancelled','not_found','not_permitted','unsupported_format','decode_error','source_not_supported','audio_decode_error','manifest_incompatible','engine_error','stalled_without_data','source_unreachable','invalid_response','server_unreachable','connection_failed']);
const routes = new Set(['original','repackaged','converted','direct','stream','remux','transcode']);
const codecs = new Set(['aac','mp3','flac','opus','alac','vorbis','h264','hevc','av1','vp9','ac3','eac3','dts','dts_hd','truehd','pcm','mp2','ac4']);
const engines = new Set(['native','mpv','avplayer','media3','browser','web-audio']);
const states = new Set(['playing','paused','buffering','stopped','ended']);
const id = /^(?:(?:itm|med|prf|usr|ses|pbs|ply|req)_[0-9a-hjkmnp-tv-z]{16}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;
const version = /^\d+(?:\.\d+){0,2}(?:-(?:dev|alpha|beta|rc)(?:\.\d+)?)?(?:\+\d+)?$/;
function facts(raw: unknown): Record<string, string | number> {
 const out: Record<string,string | number> = {};
 if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return out;
 for (const [key,value] of Object.entries(raw)) {
  if (['accountId','profileId','itemId','sessionId','versionId','requestId'].includes(key) && typeof value === 'string' && id.test(value)) out[key] = value;
  else if (['appVersion','osVersion'].includes(key) && typeof value === 'string' && value.length <= 60 && version.test(value)) out[key] = value;
  else if (key === 'screen' && typeof value === 'string' && screens.has(value) || key === 'code' && typeof value === 'string' && codes.has(value)
   || key === 'route' && typeof value === 'string' && routes.has(value) || key === 'codec' && typeof value === 'string' && codecs.has(value)
   || key === 'engine' && typeof value === 'string' && engines.has(value) || key === 'state' && typeof value === 'string' && states.has(value)) out[key] = value as string;
  else if (key === 'status' && typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= 599
   || key === 'playerErrorCode' && typeof value === 'number' && Number.isSafeInteger(value)) out[key] = value as number;
 }
 return out;
}
export class AppLog {
 private lines: {at: number; text: string; bytes: number}[] = [];
 private size = 0;
 private listeners = new Set<() => void>();
 private now: () => number;
 constructor(now: () => number = Date.now) {this.now = now;}
 private trim() {
  const oldest = this.now() - APP_LOG_AGE_MS;
  while (this.lines.length && (this.size > APP_LOG_BYTES || this.lines[0].at < oldest)) this.size -= this.lines.shift()!.bytes;
 }
 private append(at: number, level: unknown, area: unknown, message: unknown, raw: unknown) {
  if (typeof level !== 'string' || !levels.has(level) || typeof area !== 'string' || !areas.has(area) || typeof message !== 'string' || !messages.has(message)
   || !Number.isFinite(at) || at < this.now() - APP_LOG_AGE_MS || at > this.now()) return;
  const text = JSON.stringify({time: at, level, area, message, facts: facts(raw)});
  const bytes = new TextEncoder().encode(text).length + 1;
  this.lines.push({at,text,bytes}); this.size += bytes;
 }
 write(level: AppLogLevel, area: AppLogArea, message: AppLogMessage, raw: Readonly<Record<string,unknown>> = {}) {
  this.append(this.now(),level,area,message,raw); this.trim();
  for (const changed of this.listeners) changed();
 }
 /** Re-validate every restored line at write time too. Older runs cannot smuggle free text. */
 restore(text: string) {
  if (new TextEncoder().encode(text).length > APP_LOG_BYTES) return;
  for (const line of text.split('\n')) {
   try {const v = JSON.parse(line); if (v && typeof v === 'object') this.append(v.time,v.level,v.area,v.message,v.facts);} catch { /* not a line this app wrote */ }
  }
  this.lines.sort((a,b)=>a.at-b.at); this.trim();
 }
 snapshot(): string {this.trim(); return this.lines.map(line=>line.text).join('\n');}
 clear() {this.lines=[];this.size=0;for (const changed of this.listeners) changed();}
 subscribe(changed:()=>void) {this.listeners.add(changed);return ()=>{this.listeners.delete(changed);};}
}
/** Web uses this memory-only ring. Native binds its private last-run file to the same instance. */
export const appLog = new AppLog();

/** Capture status and request id, never a request's address, body, headers or response body. */
export function appLogFetch(fetcher: typeof fetch): typeof fetch {
 return async (input,init) => {
  try {
   const response = await fetcher(input,init);
   if (!response.ok) appLog.write('warn','network','network-failure',{status:response.status,code:'http_error',requestId:response.headers.get('X-Request-ID')});
   return response;
  } catch (error) {
   if (!init?.signal?.aborted) appLog.write('warn','network','network-failure',{status:0,code:'network_error'});
   throw error;
  }
 };
}
