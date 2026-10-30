import {isQualityChoice, type QualityChoice} from './choices.ts';
import {playbackFence, playbackIdentity, sameFence, samePlayback, type PlaybackFence, type PlayerPlaybackIdentity} from './identity.ts';
import {invalidState} from './validation.ts';
export type SessionChoices = Readonly<{
  subtitleTrackId: string | null; audioTrackId: string; audioLanguage: string; subtitleLanguage: string | null; speed: number; quality: QualityChoice;
  forcedOnly: boolean; subtitleOffsetMs: number; subtitleSize: 'small' | 'medium' | 'large' | 'extra-large';
  subtitleBackground: 'none' | 'translucent' | 'opaque'; subtitlePosition: number; fill: boolean;
}>;
/** The old run-keyed store's get/has/subscribe semantics, with explicit begin/stop and typed,
 * bounded keys. No singleton or persistence: an old callback cannot resurrect stopped choices. */
export class PlayerSessionChoices {
  private identity?: PlayerPlaybackIdentity;
  private source?: PlaybackFence;
  private held: Partial<SessionChoices> = {};
  private version = 0;
  private listeners = new Set<() => void>();
  getVersion = () => this.version;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private changed() { this.version++; for (const listener of [...this.listeners]) listener(); }
  begin(identity: PlayerPlaybackIdentity): void {
    const next = playbackIdentity(identity);
    if (samePlayback(this.identity,next)) return;
    this.identity = next; this.source=undefined; this.held = {}; this.changed();
  }
  stop(identity: PlayerPlaybackIdentity): boolean {
    if (!samePlayback(this.identity,identity)) return false;
    this.identity = undefined; this.source=undefined; this.held = {}; this.changed(); return true;
  }
  get<K extends keyof SessionChoices>(name: K, identity: PlayerPlaybackIdentity, settings: SessionChoices[K]): SessionChoices[K] {
    return samePlayback(this.identity,identity) && Object.hasOwn(this.held,name) ? this.held[name] as SessionChoices[K] : settings;
  }
  has(name: keyof SessionChoices, identity: PlayerPlaybackIdentity): boolean { return samePlayback(this.identity,identity) && Object.hasOwn(this.held,name); }
  set<K extends keyof SessionChoices>(name: K, identity: PlayerPlaybackIdentity, value: SessionChoices[K], source?: PlaybackFence): boolean {
    if (!samePlayback(this.identity,identity)) return false;
    const sourceChoice=['audioTrackId','subtitleTrackId','audioLanguage','subtitleLanguage','subtitleOffsetMs'].includes(name);
    if ((sourceChoice || source!==undefined) && (!source || !sameFence(this.source,source))) return false;
    const track = (v: unknown) => typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v);
    const finite = (min:number,max:number) => typeof value === 'number' && Number.isFinite(value) && value >= min && value <= max;
    const language=(v:unknown)=>typeof v==='string'&&/^[a-z]{2,16}$/.test(v);
    const valid = name === 'audioLanguage' ? language(value) : name === 'subtitleLanguage' ? value===null||language(value) : name === 'quality' ? isQualityChoice(value) : name === 'speed' ? finite(0.5,3) : name === 'subtitleTrackId' ? value === null || track(value) : name === 'audioTrackId' ? track(value) : name === 'forcedOnly' || name === 'fill' ? typeof value === 'boolean' : name === 'subtitleOffsetMs' ? finite(-600000,600000) : name === 'subtitleSize' ? ['small','medium','large','extra-large'].includes(String(value)) : name === 'subtitlePosition' ? finite(0,100) : name === 'subtitleBackground' && ['none','translucent','opaque'].includes(String(value));
    if (!valid) invalidState();
    this.held = {...this.held,[name]:value}; this.changed(); return true;
  }
  /** Languages (from the acknowledged presentation), speed and quality carry across entries.
   * Track numbers and timing belong to the source. The existing server queue also carries the
   * selected languages; adapters never apply an old file's track id to a new source. */
  bindSource(fence: PlaybackFence): boolean {
    const next=playbackFence(fence);
    if (!samePlayback(this.identity,next.playback)) return false;
    if (sameFence(this.source,next)) return true;
    this.source=next;
    const {audioTrackId,subtitleTrackId,subtitleOffsetMs,...rest}=this.held;
    this.held=subtitleTrackId===null?{...rest,subtitleTrackId:null}:rest;
    if(audioTrackId!==undefined||subtitleTrackId!==undefined||subtitleOffsetMs!==undefined)this.changed();
    return true;
  }
}
