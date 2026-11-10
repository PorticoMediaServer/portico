/** The playback service: one title playing on this device, through the platform's player. */
import {trackName} from './presentation/language.ts';
import {randomId} from './random-id.ts';
import type {PreparedChoice} from './prepared-media.ts';
import type {PlaybackChoices} from './playback-v1/choices.ts';
import {ListeningDeadlineClock, noListeningDeadlines, type NativeListeningStatus, type NativeListeningV1Control, type NativeListeningV1Event, type ListeningDeadlines, type ListeningPolicy} from './playback/native-listening.ts';
import {audioEffectsEnabled, defaultAudioEffects, parseAudioEffects, type AudioEffectsSnapshot, type AudioEffectsSettings, type PreparedAudio, type ListeningNoticeCode, type AudioProcessingBinding, type AudioProcessingApplied} from './audio-effects.ts';
import {sessionEndMessage} from './session-end.ts';
import {serviceText} from './presentation/service-text.ts';
import {TUNER_RESERVED, tunerHandedOver} from './live-handover.ts';
import type {PlayerFailureCode} from './client-profile.ts';
import {channelSeconds, channelMessage, type ChannelProjection, type ChannelReference} from './playback/linear-protocol.ts';
import type {ChannelPlayback, ChannelView, ChannelStartOptions} from './playback-v1/channel-control.ts';
import type {StartPreparation} from './playback-v1/sessions.ts';
import type {QueueController} from './queue-controller.ts';
import {AudioSelection, emptyAudio, type AudioPlan, type AudioBinding, type AudioSelectionSnapshot} from './audio-selection.ts';
import {type Library, renderable, type PlaybackLocalStartContext, type PlaybackSession, ApiError, type PlaybackApi, type SessionEndInfo} from './http.ts';
function localStartCurrent(context?:PlaybackLocalStartContext):boolean {
  try{return !context?.signal?.aborted&&context?.isCurrent?.()!==false;}catch{return false;}
}
function localStartCancelled():Error{return Object.assign(new Error('cancelled'),{name:'AbortError'});}
function statusOf(error: unknown): number {
  const status = (error as { status?: unknown } | null)?.status;
  return typeof status === "number" ? status : 0;
}
export type ProgressCheckpoint = {
  generation: number;
  sequence: number;
  positionSeconds: number;
  state: "playing" | "paused" | "ended";
};
export type PlaybackJournalEntry = {
  id?: string;
  request?: { itemId: string; requestId: string };
  checkpoint?: ProgressCheckpoint;
};
/** Adapter must scope the protected journal to the selected server/account/profile. */
export interface PlaybackJournal {
  load(): Promise<PlaybackJournalEntry[]>;
  save(entries: PlaybackJournalEntry[]): Promise<void>;
}
export type PlaybackOptions = {
  /** Diagnostic codes and bounded server reasons never enter a UI snapshot. */
  audioDiagnostic?: (code:string,detail?:string)=>void;
  /** Live TV and Library Channels on v1 sessions (spec §18.6): V1ChannelControl. */
  channels?: ChannelPlayback;
  /** A new playback (`playbackRun`) is about to start: put back to the Settings whatever the
   * player changed for the last one that the server plans from (a player engine chosen in the
   * player), and resolve when the server has it. The start waits for it. */
  beforeFreshPlayback?: () => Promise<void>;
  journal?: PlaybackJournal;
  deferRecovery?: boolean;
  startupTimeoutMs?: number;
  operationTimeoutMs?: number;
  seekTimeoutMs?: number;
  audioTimeoutMs?: number;
  maxPendingCleanup?: number;
  queueRequired?: boolean;
  /** Development builds: log why a start timed out (see setStartupProbe). */
  diagnostics?: boolean;
  /**
   * Whether the page/app is hidden. Startup and seek deadlines pause while media loading
   * is suspended; platforms with onChange preserve the remaining foreground window.
   */
  pageVisibility?: PageVisibility;
  /** The wall clock (tests), for tuner deadlines and foreground media windows. */
  now?: () => number;
};
/** Page/app visibility. onChange reports both hide and show before suspended timers resume. */
export type PageVisibility = {hidden(): boolean; onVisible(listener: () => void): () => void; onChange?(listener: () => void): () => void};
/** One selectable audio track of the playing source. */
export type AudioTrackOffer = Readonly<{streamIndex:number;label:string;language?:string;channels?:number;codec:string}>;
export type AudioTracksSnapshot = Readonly<{tracks:readonly AudioTrackOffer[];selected:number|null;pending?:number}>;
/** A readable name for a track the file did not name. */
export function audioTrackLabel(t:{title?:string;language?:string;channels?:number;codec:string},ordinal:number):string{
  const layout=t.channels===undefined||t.channels<1?'':t.channels===1?'Mono':t.channels===2?'Stereo':t.channels===6?'5.1':t.channels===8?'7.1':t.channels+' ch';
  const codec=({aac:'AAC',ac3:'Dolby Digital',eac3:'Dolby Digital Plus',truehd:'Dolby TrueHD',dts:'DTS',flac:'FLAC',opus:'Opus',vorbis:'Vorbis',mp3:'MP3',alac:'ALAC',pcm:'PCM'} as Record<string,string>)[t.codec]??t.codec.toUpperCase();
  // The language leads; the track's own title follows only when it says more than the language (presentation/language.ts).
  return [trackName(t)||('Track '+ordinal),[codec,layout].filter(Boolean).join(' ')].filter(Boolean).join(' · ');
}
export type PlaybackSnapshot = {
  /** Genuine server-directed start preparation; carries no session or media authority. */
  preparation?: StartPreparation;
  audioTracks?: AudioTracksSnapshot;
  nativeListening?: NativeListeningStatus;
  listeningDeadlines?: ListeningDeadlines;
  audioEffects: AudioEffectsSnapshot;
  listeningNotice?: ListeningNoticeCode;
  /** The music engine fell back for this song: the signal path says so, never a notice. */
  musicFallback?: 'processing' | 'eq';
  channel?: ChannelReference;
  linear?: ChannelProjection;
  /** The channel's start can't proceed (occurrence status "recoverable"); `linear.media.errorCode` says why. */
  linearStalled?: boolean;
  audio: AudioSelectionSnapshot;
  revision: number;
  intentId: number;
  /** One continuous playback. A start the viewer asks for (Play on a title, a new queue, a
   * download) begins a new one; the queue moving on (autoplay, Up Next, Next and Previous, a
   * retry or restart in place) stays in it. A choice made in the player (speed, subtitle
   * position, Forced only, picture fill) lasts while this stays the same and goes back to the
   * Settings when it changes; the audio and subtitle tracks are carried by the server. */
  playbackRun: number;
  seekRevision: number;
  pendingSeek?: Readonly<{revision: number; positionSeconds: number}>;
  seekError?: string;
  failedSeek?: Readonly<{revision: number; positionSeconds: number}>;
  /** The player ran out of buffered media and is trying to get more. A status, never an error:
   * playback is only ended by `fail`, on a definite refusal or after recovery is exhausted. */
  recovering?: boolean;
  phase: "idle" | "starting" | "ready" | "ended" | "error";
  intent: "playing" | "paused";
  itemId?: string;
  session?: PlaybackSession;
  positionSeconds: number;
  duration: number;
  error?: string;
  /** The server ended this playback, and why: "terminated" by an administrator (with their
   * message), "transferred", "lease_expired", "stopped"… `phase` is "error" and `error` is the
   * viewer's text for it (`sessionEndMessage`); nothing advances. Cleared by the next start. */
  ended?: SessionEndInfo;
  cleanupPending: number;
  cleanupError?: string;
};
export interface PlaybackAdapter {
  apply(snapshot: PlaybackSnapshot): void;
}
/** Actual current-song applied leveling gain: only when the report says leveling was applied and
 * its dB amount is finite and inside the renderer's admitted range. */
function appliedLevelingGain(facts:Pick<NativeListeningStatus,'levelingApplied'|'levelingGainDb'>|undefined):number|undefined{
 return facts?.levelingApplied===true&&typeof facts.levelingGainDb==='number'&&Number.isFinite(facts.levelingGainDb)&&facts.levelingGainDb>=-60&&facts.levelingGainDb<=30?facts.levelingGainDb:undefined;
}
/** UI intent is the sole command authority. Native events report facts, never autoplay intent. */
export class PlaybackService {
  /** Device engine processing (for example device-local EQ), never an account preference. */
  private deviceAudioProcessing = false;

  private channels?: ChannelPlayback;
  private channelWantedState:'playing'|'paused'='playing';
  private channelSeekId?: string;
  private channelObservedSeek?: string;
  private channelSubmittedSeek?: string;
  private channelReference?: ChannelReference;
  private audioSelection: AudioSelection;
  private queue?: QueueController;
  private queueConnection:{error?:string;retry?:()=>void}={};
  private api: PlaybackApi;
  /** A Playback v1 audio session the native listening owner plays (Apple, plan §9.3 C3): it
   * reports the timeline and moves the queue; this service only forwards intents to it. */
  private nativeV1?: NativeListeningV1Control;
  private listeningPolicy?:ListeningPolicy;
  private nativePolicyRevision=0;private nativePolicyKey="";
  private nativeResumeRevision=0;
  private nativePolicyPending?:{key:string;native:NativeListeningV1Control;revision:number;promise:Promise<boolean>};
  private listeningClock=new ListeningDeadlineClock(()=>{this.pause();this.update({listeningDeadlines:this.listeningClock.getSnapshot(),error:'Listening timer paused playback.'});});
  private options: PlaybackOptions;
  /** `beforeFreshPlayback` for the playback being started, awaited before its first request. */
  private freshReady?: Promise<void>;
  private snapshot: PlaybackSnapshot = {
    audioEffects: {settings:defaultAudioEffects,rendering:false,rate:1,eqApplied:false,levelingApplied:false},
    audio: emptyAudio(),
    revision: 0,
    intentId: 0,
    playbackRun: 0,
    seekRevision: 0,
    phase: "idle",
    intent: "paused",
    positionSeconds: 0,
    duration: 0,
    cleanupPending: 0,
  };
  private listeners = new Set<() => void>();
  private adapter?: PlaybackAdapter;
  private sequence = 0;
  private endedSeekPosition?: number;
  private failedRecovery?: { positionSeconds?: number; intent: "playing" | "paused" };
  /** The server's music preferences have been adopted: the device journal no longer overrides them. */
  private effectsFromServer = false;
  private lastPlay?: { itemId: string; prepared?: PreparedChoice; quality?: string; audioStream?: number; choices?: PlaybackChoices; local?: () => PlaybackSession; localContext?: PlaybackLocalStartContext };
  /** The session the engine-failure count belongs to. */
  private escalationSession?: string;
  private seekRearm?: () => void;
  private startupRearm?: () => void;
  private escalations = 0;
  private seekWait?: () => void;
  private hasObserved = false;
  private bookmarkProtected = true;
  private lastProgress = 0;
  private creates = new Map<string, { itemId: string; running: boolean }>();
  private cleanup = new Map<
    string,
    { running: boolean; checkpoint?: ProgressCheckpoint; work?: Promise<void> }
  >();
  private progressPending = new Map<string, ProgressCheckpoint>();
  private localSessions = new Map<string, PlaybackApi['localPlayback']>();
  private progressRunning = new Set<string>();
  private requestId: () => string;
  private startupWait?: () => void;
  private journalTail = Promise.resolve();
  private restored?: Promise<void>;
  constructor(
    api: PlaybackApi,
    requestId: () => string = () => randomId(),
    options: PlaybackOptions = {}
  ) {
    this.api = api;
    this.requestId = requestId;
    this.options = options;
    this.channels=options.channels;
    this.audioSelection = new AudioSelection(audio => this.update({audio}), () => this.snapshot, options.audioTimeoutMs);
    if (options.seekTimeoutMs !== undefined && (!Number.isFinite(options.seekTimeoutMs) || options.seekTimeoutMs < 1 || options.seekTimeoutMs > 60000)) throw new Error("Invalid seek timeout");
    api.onServerEnded?.((id, end) => this.serverEnded(id, end));
    if (!options.deferRecovery) this.startRecovery();
  }
  /** The server ended a session: an old one is dropped from the progress backlog; the current one
   * stops, telling the viewer why (an administrator's message as written). A session the engine
   * already finished is completion, unless an administrator ended it. */
  private serverEnded(sessionId: string, end: SessionEndInfo) {
    this.progressPending.delete(sessionId);
    const s = this.snapshot;
    if (s.session?.id !== sessionId || s.phase === "idle" || (s.phase === "ended" && end.reason !== "terminated")) return;
    this.fail(s.intentId, sessionEndMessage(end), { ended: Object.freeze({ reason: end.reason, message: end.message }) });
  }
  /** Explicit committed startup for React owners; safe to call repeatedly. */
  startRecovery(): Promise<void> {
    if (this.restored) return this.restored;
    this.restored = this.options.journal
      ? this.options.journal
          .load()
          .then((entries) => {
            for (const entry of entries) {
              if (entry.id && !this.cleanup.has(entry.id))
                this.cleanup.set(entry.id, {
                  running: false,
                  checkpoint: entry.checkpoint,
                });
              if (entry.request)
                this.creates.set(entry.request.requestId, {
                  itemId: entry.request.itemId,
                  running: false,
                });
            }
            this.update({});
            this.retryCleanup();
          })
          .catch(() => {
            this.update({
              cleanupError:
                "Playback recovery could not be read. Retry before starting another session.",
            });
          })
      : Promise.resolve();
    return this.restored;
  }

  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  attachAdapter(adapter: PlaybackAdapter) {
    this.adapter = adapter;
    adapter.apply(this.snapshot);
    return () => {
      if (this.adapter === adapter) this.adapter = undefined;
    };
  }
  private update(patch: Partial<PlaybackSnapshot>) {
    if(patch.phase==='idle'||patch.phase==='error'||patch.phase==='ended'||'intentId' in patch&&patch.intentId!==this.snapshot.intentId)patch={...patch,preparation:undefined};
    if(patch.intent==='playing'||patch.phase==='idle'||patch.phase==='error'||patch.error||'intentId' in patch&&patch.intentId!==this.snapshot.intentId)patch={...patch,listeningNotice:undefined};
    // A fallback lasts for this song only: its own terminal and new-run resets, separate
    // from the notice, including a real ended fact that keeps the same item and intent.
    if(patch.phase==='ended'||patch.phase==='idle'||patch.phase==='error'||patch.error||'intentId' in patch&&patch.intentId!==this.snapshot.intentId||patch.itemId!==undefined&&patch.itemId!==this.snapshot.itemId)patch={...patch,musicFallback:undefined};
    const changed='session' in patch&&(patch.session?.id!==this.snapshot.session?.id||patch.session?.generation!==this.snapshot.session?.generation);
    if(changed||patch.phase==='idle'||patch.phase==='error'||patch.phase==='ended'){
      const actual=patch.phase==='idle'||patch.phase==='error'||patch.phase==='ended'?undefined:patch.nativeListening;
      patch={...patch,audioEffects:{...(patch.audioEffects??this.snapshot.audioEffects),eqApplied:actual?.eqApplied===true,levelingApplied:actual?.levelingApplied===true,levelingGainDb:appliedLevelingGain(actual)}};
    }
    // `ended` belongs to one session: a new start or another session clears it.
    if (!("ended" in patch) && this.snapshot.ended && (patch.phase === "starting" || ("session" in patch && patch.session?.id !== this.snapshot.session?.id)))
      patch = { ...patch, ended: undefined };
    this.snapshot = Object.freeze({
      ...this.snapshot,
      ...patch,
      revision: this.snapshot.revision + 1,
      cleanupPending: this.cleanup.size + this.creates.size,
    });
    this.adapter?.apply(this.snapshot);
    for (const listener of this.listeners) listener();
  }
  private presentationAudioPreference?:string;
  /** Same session and occurrence; only committed transport generation changes.
   * Current intent, speed (adapter-owned), pending seek and source position win
   * over the older position used to prepare the replacement. */
  installSubtitlePresentation(p:import('./subtitles.ts').SubtitlePresentation):boolean {
    const before=this.snapshot,session=before.session;
    if(!session||p.sessionId!==session.id||p.generation<session.generation||!['starting','ready'].includes(before.phase))return false;
    if(p.generation===session.generation)return p.streamUrl===session.streamUrl&&p.mode===session.mode;
    if(!Number.isSafeInteger(p.generation)||!/^\/v1\/media\/[A-Za-z0-9_-]+(?:\/master\.m3u8|\/subtitle-video\/[A-Za-z0-9_-]+\/master\.m3u8)?$/.test(p.streamUrl))return false;
    const position=before.pendingSeek?.positionSeconds??before.positionSeconds;
    const oldPlan=before.audio.plan;
    this.presentationAudioPreference=before.audio.pending?.renditionId??before.audio.observedRenditionId??undefined;
    this.clearStartup();this.bookmarkProtected=true;this.hasObserved=false;
    const audio=this.audioSelection.reset();
    this.requestSeek(position,{session:{...session,generation:p.generation,streamUrl:p.streamUrl,mode:p.mode,resumeSeconds:position},phase:'starting',audio,error:undefined});
    if(oldPlan&&p.mode==='hls')this.audioSelection.install(before.intentId,{...oldPlan,generation:p.generation});
    return true;
  }
  /**
   * The same session re-planned on the server (a quality, version or audio change): its new
   * presentation replaces the old one in place. The queue, the intent and the position stay; the
   * engine loads the new stream where the picture was.
   */
  installPresentation(next:PlaybackSession):boolean {
    const before=this.snapshot,session=before.session;
    if(!session||next.id!==session.id||before.channel||!['starting','ready'].includes(before.phase))return false;
    if(!Number.isSafeInteger(next.generation)||next.generation<=session.generation)return next.generation===session.generation;
    const position=before.pendingSeek?.positionSeconds??(this.hasObserved?before.positionSeconds:session.resumeSeconds);
    this.clearStartup();this.bookmarkProtected=true;this.hasObserved=false;
    const intentId=before.intentId;
    this.requestSeek(position,{session:{...next,duration:next.duration||session.duration,resumeSeconds:position},phase:'starting',audio:this.audioSelection.reset(),audioTracks:before.audioTracks?Object.freeze({...before.audioTracks,selected:before.audioTracks.pending??before.audioTracks.selected,pending:undefined}):undefined,error:undefined,recovering:undefined,
      audioEffects:{...before.audioEffects,rendering:renderable(next)&&(audioEffectsEnabled(before.audioEffects.settings)||this.deviceAudioProcessing),prepared:undefined,committed:undefined}});
    this.audioPlanDiagnostic(next);
    this.armStartup(()=>{this.reportStalledStart(intentId);this.fail(intentId,'Playback did not become ready. Check the server and try again.');});
    return true;
  }
  /**
   * The engine is still receiving media for the start or seek in progress. A slow link or a
   * server converting slower than real time is not a failure: the start and seek windows begin
   * again, and only run out after a full window with nothing arriving at all.
   */
  engineProgress(intentId:number){
    if(intentId!==this.snapshot.intentId)return;
    this.startupRearm?.();this.seekRearm?.();
  }
  /** ListeningService is the sole policy producer; render preferences join the
   * same snapshot before crossing the native boundary. Identical observations
   * never reset native deadlines or native-only remote-control activity. */
  setListeningPolicy(value:ListeningPolicy){this.listeningPolicy=value;return this.syncListeningPolicy();}
  private syncListeningPolicy():Promise<boolean>{
    const native=this.nativeV1,original=Object.freeze({sessionId:this.snapshot.session?.id,generation:this.snapshot.session?.generation,run:this.snapshot.playbackRun,owner:this.snapshot.nativeListening?.owner});
    const originalSource=()=>this.snapshot.session?.id===original.sessionId&&this.snapshot.session?.generation===original.generation&&this.snapshot.playbackRun===original.run&&this.snapshot.nativeListening?.owner===original.owner;
    if(!this.listeningPolicy||!native)return Promise.resolve(true);
    if(!native.configure)return Promise.resolve(this.listeningPolicy.timer===null);
    const value={...this.listeningPolicy,effects:this.snapshot.audioEffects.settings},key=JSON.stringify(value);
    if(key===this.nativePolicyKey)return Promise.resolve(true);
    if(this.nativePolicyPending?.key===key&&this.nativePolicyPending.native===native)return this.nativePolicyPending.promise;
    this.nativePolicyKey='';
    const revision=++this.nativePolicyRevision;
    const promise=Promise.resolve().then(()=>native.configure!({...value,revision})).then(accepted=>{
      if(accepted!==true)throw new Error('Native listening policy was not accepted');
      if(revision!==this.nativePolicyRevision||this.nativeV1!==native)return false;
      this.nativePolicyKey=key;return true;
    }).catch(()=>{
      // A failed bridge policy must not leave audible playback behind a timer
      // the UI assumes is armed. A late error cannot pause a different owner.
      if(revision!==this.nativePolicyRevision||this.nativeV1!==native)return false;
      this.nativePolicyKey='';if(originalSource()){this.pause();this.listeningNotice('listening_policy_failed');}return false;
    }).finally(()=>{if(this.nativePolicyPending?.revision===revision)this.nativePolicyPending=undefined;});
    this.nativePolicyPending={key,native,revision,promise};return promise;
  }
  isNativeListening(){return !!this.snapshot.nativeListening||!!this.nativeV1;}
  async setListeningDeadline(kind:'sleep'|'passout',minutes:number|null){
    if(minutes!==null&&(!Number.isFinite(minutes)||minutes<=0||minutes>1440))throw new Error('Choose a listening timer between 1 minute and 24 hours.');
    const key=kind==='sleep'?'sleepAtMs':'passoutAtMs',value={...(this.snapshot.listeningDeadlines??noListeningDeadlines),[key]:minutes===null?null:Date.now()+Math.round(minutes*60000)};
    if(this.nativeV1){await this.nativeV1.deadlines(value);return;}
    this.listeningClock.set(value);
    this.update({listeningDeadlines:value});
  }
  checkListeningDeadlines(){if(!this.nativeV1)this.listeningClock.check();}
  systemListeningAction(action:'next'|'previous'){return this.nativeV1?.advance(action)??Promise.resolve();}
  setPlaybackRate(rate:number){
    if(!Number.isFinite(rate)||rate<.25||rate>4)return;
    // AVAudioUnitTimePitch is controlled by the native owner. A JS seek here
    // would duplicate its intent and restart a PCM window at the old epoch.
    if(this.nativeV1){void this.nativeV1.action('rate',rate).catch(()=>{});return;}
    // The speed already in use: nothing to restart (a new session re-applies the held speed).
    if(rate===this.snapshot.audioEffects.rate)return;
    // Where the listener is, or is about to be: a start or seek still on its way (a resume
    // part-way into a book) keeps its target rather than going back to the last reported position.
    const at=this.snapshot.pendingSeek?.positionSeconds??this.snapshot.positionSeconds;
    this.queue?.seekRetiringAudio?.(at);this.queue?.invalidateAudioPreparation?.();
    this.update({audioEffects:{...this.snapshot.audioEffects,rate}});
    if(this.snapshot.audioEffects.rendering&&this.snapshot.session)this.requestSeek(at);
  }
  /** The native v1 owner re-reads its session (the app returned to the foreground). */
  refreshPlaybackAuthority(){return this.nativeV1?.refresh()??Promise.resolve();}
  installAudioPlan(intentId:number,plan:AudioPlan){return this.audioSelection.install(intentId,plan);}
  invalidateAudioPlan(intentId:number,reason:string){this.audioSelection.invalidate(intentId,reason);}
  audioUnavailable(intentId:number,reason:string){this.audioSelection.unavailable(intentId,reason);}
  attachAudioEngine(binding:AudioBinding){return this.audioSelection.attach(binding);}
  detachAudioEngine(binding:AudioBinding,epoch:number){this.audioSelection.detach(binding,epoch);}
  audioMapped(binding:AudioBinding,epoch:number,ids:readonly string[]){const mapped=this.audioSelection.mapped(binding,epoch,ids);const preferred=this.presentationAudioPreference;if(mapped&&preferred&&ids.includes(preferred)){this.presentationAudioPreference=undefined;this.audioSelection.select(preferred);}return mapped;}
  selectAudio(id:string){return this.audioSelection.select(id);}
  /** The session's audio tracks, for a stream that carries one track at a time (an original, a
   * repackaged or converted stream without alternate renditions). Choosing one asks for the title
   * again from the same position with that track named; the server picks the delivery that reaches
   * it, which may differ from the current one. */
  installAudioTracks(intentId:number,tracks:readonly AudioTrackOffer[],selected:number|null){
    if(intentId!==this.snapshot.intentId||!this.snapshot.session)return;
    const clean=tracks.filter(t=>Number.isInteger(t.streamIndex)&&t.streamIndex>=0&&t.streamIndex<=65535).slice(0,64);
    // The track this play asked for by name is the one in use; otherwise the caller's (the file's default).
    const choice=this.lastPlay?.itemId===this.snapshot.itemId?this.lastPlay?.choices?.audioTrackId:undefined;
    const asked=this.lastPlay?.itemId===this.snapshot.itemId?(choice&&/^a\d{1,4}$/.test(choice)?Number(choice.slice(1)):this.lastPlay?.audioStream):undefined;
    if(asked!==undefined&&clean.some(t=>t.streamIndex===asked))selected=asked;
    this.update({audioTracks:clean.length>1?Object.freeze({tracks:Object.freeze(clean.map(t=>Object.freeze({...t}))),selected}):undefined});
  }
  selectAudioTrack(streamIndex:number):Promise<void>{
    const tracks=this.snapshot.audioTracks,itemId=this.snapshot.itemId,last=this.lastPlay;
    if(!tracks||!itemId||last?.local||tracks.pending!==undefined||tracks.selected===streamIndex||!tracks.tracks.some(t=>t.streamIndex===streamIndex))return Promise.resolve();
    const position=this.snapshot.pendingSeek?.positionSeconds??this.snapshot.positionSeconds;
    this.update({audioTracks:Object.freeze({...tracks,pending:streamIndex})});
    // The live session is re-planned with that track (same session, same queue entry).
    if(this.queue?.changePlayback&&this.snapshot.session&&['starting','ready'].includes(this.snapshot.phase)){
      const intentId=this.snapshot.intentId;
      return this.changePlayback({audioTrackId:'a'+streamIndex}).then(()=>{
        const now=this.snapshot.audioTracks;
        if(this.snapshot.intentId===intentId&&now?.pending===streamIndex)this.update({audioTracks:Object.freeze({...now,selected:streamIndex,pending:undefined})});
      },error=>{
        const now=this.snapshot.audioTracks;
        if(this.snapshot.intentId===intentId&&now?.pending===streamIndex)this.update({audioTracks:Object.freeze({...now,pending:undefined})});
        throw error;
      });
    }
    return this.play(itemId,position,undefined,last?.itemId===itemId?last.quality:undefined,streamIndex,last?.itemId===itemId?last.choices:undefined);
  }
  /**
   * Change the quality, version or audio track of what is playing. The session is re-planned on
   * the server and stays the same session on the same queue entry, so Up next is untouched. The
   * choices are remembered for a replay or an automatic retry of this title.
   */
  /** Whether a change of quality, version or track can be made on the playing session itself
   * (`changePlayback` does nothing otherwise: a channel, a downloaded file, nothing playing). */
  changesInPlace():boolean{
    return !!this.snapshot.itemId&&!!this.snapshot.session&&!this.snapshot.channel&&!this.lastPlay?.local&&!!this.queue?.changePlayback;
  }
  changePlayback(choices:PlaybackChoices):Promise<void>{
    const itemId=this.snapshot.itemId,session=this.snapshot.session;
    if(!itemId||!session||this.snapshot.channel||this.lastPlay?.local||!this.queue?.changePlayback)return Promise.resolve();
    const {replan: _replan, ...remembered}=choices;
    if (choices.subtitleTrackId !== undefined && choices.subtitleDelivery === undefined && this.lastPlay?.choices) {
      const {subtitleDelivery: _oldDelivery, subtitleOffsetUs: _oldExactOffset, ...previous} = this.lastPlay.choices;
      this.lastPlay = {...this.lastPlay, choices: previous};
    }
    this.lastPlay={...(this.lastPlay?.itemId===itemId?this.lastPlay:{itemId}),choices:{...(this.lastPlay?.itemId===itemId?this.lastPlay.choices:undefined),...remembered}};
    return this.queue.changePlayback(choices);
  }
  /** The choices the current title was started or last changed with. */
  playbackChoices():PlaybackChoices{return this.lastPlay?.itemId===this.snapshot.itemId?this.lastPlay?.choices??{}:{};}
  audioObserved(binding:AudioBinding,epoch:number,sequence:number,id:string|null){return this.audioSelection.observed(binding,epoch,sequence,id);}
  audioApplied(binding:AudioBinding,epoch:number,revision:number,id:string){return this.audioSelection.applied(binding,epoch,revision,id);}
  audioRenditionUnavailable(binding:AudioBinding,epoch:number,id:string){return this.audioSelection.renditionUnavailable(binding,epoch,id);}
  audioFailed(binding:AudioBinding,epoch:number,revision:number,reason:string,unavailable=false){return this.audioSelection.failed(binding,epoch,revision,reason,unavailable);}
  /** The device's cached effects (the queue journal): used only until the server's music
   * preferences have been adopted, and offline (X-02). */
  restoreAudioEffects(settings:AudioEffectsSettings){if(this.effectsFromServer)return;this.update({audioEffects:{...this.snapshot.audioEffects,settings:parseAudioEffects(settings)}});this.syncListeningPolicy();}
  /** X-02: the viewer's music preferences from the server (`musicAudioEffects`), applied on
   * sign-in, profile selection and every preference change. They win over the device's cached
   * copy, which they also refresh. The same settings again change nothing. */
  adoptAudioEffectsPreference(settings:AudioEffectsSettings){
    settings=parseAudioEffects(settings);this.effectsFromServer=true;
    const current=this.snapshot.audioEffects.settings;
    // This setting belongs to one app/device, never the profile registry.
    settings={...settings,...(current.sampleRateMatching?{sampleRateMatching:current.sampleRateMatching}:{})};
    if(current.gapless===settings.gapless&&current.crossfadeSeconds===settings.crossfadeSeconds&&current.normalization===settings.normalization&&current.neverBoost===settings.neverBoost&&current.smoothTransitions===settings.smoothTransitions)return;
    this.setAudioEffects(settings);
  }
  /** Local settings owner supplies the same device value on every output. No persistence here. */
  setDeviceSampleRateMatching(value:'smart'|'strict'|'off'){
    if(!['smart','strict','off'].includes(value))throw new Error('Invalid sample-rate matching setting.');
    const effects=this.snapshot.audioEffects;
    if(effects.settings.sampleRateMatching===value)return;
    this.update({audioEffects:{...effects,settings:{...effects.settings,sampleRateMatching:value}}});
    this.syncListeningPolicy();
  }
  setAudioEffects(settings:AudioEffectsSettings){
    settings=parseAudioEffects(settings);if(!this.nativeV1){this.queue?.seekRetiringAudio?.(this.snapshot.positionSeconds);this.queue?.invalidateAudioPreparation?.();}
    const current=this.snapshot,effects=current.audioEffects;
    const enable=renderable(current.session)&&(audioEffectsEnabled(settings)||this.deviceAudioProcessing);
    const sameNormalization=effects.settings.normalization===settings.normalization;
    this.update({audioEffects:{...effects,settings,rendering:effects.rendering||enable,levelingApplied:sameNormalization&&effects.levelingApplied,levelingGainDb:sameNormalization&&effects.levelingApplied?effects.levelingGainDb:undefined}});
    this.syncListeningPolicy();
    if(enable&&!effects.rendering&&current.session&&!this.nativeV1)this.requestSeek(current.positionSeconds);
    void this.queue?.saveAudioEffects(settings);
  }
  /** The platform engine selects local processing without rewriting the viewer's music settings. */
  setDeviceAudioProcessing(enabled:boolean){
    if(this.deviceAudioProcessing===enabled)return;
    this.deviceAudioProcessing=enabled;
    const current=this.snapshot,effects=current.audioEffects;
    const rendering=renderable(current.session)&&(enabled||audioEffectsEnabled(effects.settings));
    if(rendering===effects.rendering)return;
    this.queue?.invalidateAudioPreparation?.();
    const audioEffects={...effects,rendering,prepared:undefined,committed:undefined};
    if(current.session)this.requestSeek(current.pendingSeek?.positionSeconds??current.positionSeconds,{audioEffects});
    else this.update({audioEffects});
  }
  audioRenderStatus(effectiveRate:number,waiting:boolean){
    if(!Number.isFinite(effectiveRate)||effectiveRate<0||effectiveRate>4)return;
    const effects=this.snapshot.audioEffects;
    if(!effects.rendering||effects.effectiveRate===effectiveRate&&effects.waiting===waiting)return;
    this.update({audioEffects:{...effects,effectiveRate,waiting}});
  }
  /** A lasting engine fallback for this song, read by the signal path; never a notice.
   * The caller passes the deck it observed, and the write is refused unless that exact
   * session, generation, item and intent is still the current, live one. */
  listeningFallback(kind:'processing'|'eq',captured:Readonly<{sessionId?:string;generation?:number;itemId?:string;intentId:number}>){
    const live=this.snapshot;
    if(!captured.sessionId||live.phase!=='ready'||live.error)return;
    if(!live.session||live.session.id!==captured.sessionId||live.session.generation!==captured.generation)return;
    if(live.itemId!==captured.itemId||live.intentId!==captured.intentId)return;
    if(live.musicFallback!==kind)this.update({musicFallback:kind});
  }
  listeningNotice(code:ListeningNoticeCode){
    if(code!=='audio_autoplay_blocked'&&code!=='listening_policy_failed')return;
    if(this.snapshot.intent==='paused'&&this.snapshot.phase!=='error'&&this.snapshot.phase!=='idle'&&this.snapshot.listeningNotice!==code)this.update({listeningNotice:code});
  }
  /** Called only after filters/gain have actually been installed or removed for this song. */
  audioProcessingApplied(binding:AudioProcessingBinding,facts:AudioProcessingApplied):boolean{
    const s=this.snapshot,amount=facts.levelingGainDb;
    if(!s.session||this.nativeV1||s.phase==='idle'||s.phase==='error'||s.phase==='ended'||s.intentId!==binding.intentId||s.session.id!==binding.sessionId||s.session.generation!==binding.generation||typeof facts.eqApplied!=='boolean'||typeof facts.levelingApplied!=='boolean'||amount!==undefined&&(typeof amount!=='number'||!Number.isFinite(amount)||amount<-60||amount>30))return false;
    const levelingGainDb=facts.levelingApplied&&amount!==undefined?amount:undefined;
    if(s.audioEffects.eqApplied!==facts.eqApplied||s.audioEffects.levelingApplied!==facts.levelingApplied||s.audioEffects.levelingGainDb!==levelingGainDb)this.update({audioEffects:{...s.audioEffects,eqApplied:facts.eqApplied,levelingApplied:facts.levelingApplied,levelingGainDb}});
    return true;
  }
  audioDiagnostic(code:string,detail?:string){
    if(!/^[a-z][a-z0-9_]{0,95}$/.test(code))return;
    const safe=detail?.replace(/[\x00-\x1f\x7f]/g,' ').slice(0,1024);
    this.options.audioDiagnostic?.(code,safe);
    if(this.options.diagnostics)console.debug('[audio]',code,safe??'');
  }
  private audioPlanDiagnostic(session:PlaybackSession){if(session.audio?.reason)this.audioDiagnostic('audio_plan',session.audio.reason);}
  prepareAudio(prepared:PreparedAudio){this.update({audioEffects:{...this.snapshot.audioEffects,prepared}});}
  clearPreparedAudio(){const e=this.snapshot.audioEffects;if(e.prepared||e.committed)this.update({audioEffects:{...e,prepared:undefined,committed:undefined}});}
  audioPrepared(token:string){this.queue?.audioPrepared(token);}
  audioCommit(token:string){void this.queue?.commitAudio(token);}
  audioBoundary(token:string,position:number){void this.queue?.audioBoundary(token,position);}
  audioPreparationFailed(){this.queue?.discardAudio();}
  audioRenderFailed(message:string){this.queue?.audioRenderFailed(message);}
  /** A failed optional decoder hands the same session to the platform player. */
  fallbackAudioRender():boolean {
    const current=this.snapshot;
    if(!current.session||!current.audioEffects.rendering||!current.session.streamUrl||this.nativeV1)return false;
    this.queue?.invalidateAudioPreparation?.();this.bookmarkProtected=true;
    this.requestSeek(current.pendingSeek?.positionSeconds??current.positionSeconds,{audioEffects:{...current.audioEffects,rendering:false,eqApplied:false,levelingApplied:false,levelingGainDb:undefined,prepared:undefined,committed:undefined}});
    return true;
  }
  async commitAudioAuthority(token:string,itemId:string,session:PlaybackSession,expectedIntent:number):Promise<boolean>{
    if(this.snapshot.intentId!==expectedIntent)return false;
    // The server has committed the next session (§18.3); the bridge reports its timeline from the
    // boundary on. Buffered outgoing samples may still drain, but they write no progress.
    this.bookmarkProtected=true;
    this.update({audioEffects:{...this.snapshot.audioEffects,committed:{token,itemId,session}}});return true;
  }
  adoptAudioBoundary(token:string,position:number):boolean{
    const next=this.snapshot.audioEffects.committed;if(!next||next.token!==token||!Number.isFinite(position)||position<0)return false;
    this.clearStartup();this.clearSeek();this.hasObserved=true;this.bookmarkProtected=false;this.sequence=0;this.lastProgress=position;
    this.update({intentId:this.snapshot.intentId+1,session:next.session,itemId:next.itemId,phase:'ready',positionSeconds:position,duration:next.session.duration,pendingSeek:undefined,failedSeek:undefined,seekError:undefined,audio:this.audioSelection.reset(),audioEffects:{...this.snapshot.audioEffects,prepared:undefined,committed:undefined,rendering:true}});
    this.checkpoint(this.snapshot.intent);void this.persist();return true;
  }
  private clearStartup() {
    this.startupRearm = undefined;
    this.startupWait?.();
    this.startupWait = undefined;
  }
  /** Both media deadlines count foreground time, with the same progress-based rearm. */
  private visibleDeadline(ms: number, expire: () => void) {
    const visibility = this.options.pageVisibility, now = this.options.now ?? Date.now;
    let timer: ReturnType<typeof setTimeout> | undefined, off: (() => void) | undefined;
    let remaining = ms, started = 0, closed = false;
    const pause = () => {
      if (timer === undefined) return;
      clearTimeout(timer); timer = undefined;
      remaining = Math.max(0, remaining - Math.max(0, now() - started));
    };
    const clear = () => { closed = true; pause(); off?.(); off = undefined; };
    const arm = () => {
      if (closed || timer !== undefined || visibility?.hidden()) return;
      started = now();
      timer = setTimeout(() => {
        timer = undefined;
        // Older adapters expose only onVisible. A timer firing while hidden waits for
        // a fresh visible window; adapters with onChange paused it at backgrounding.
        if (visibility?.hidden()) { remaining = ms; return; }
        clear(); expire();
      }, remaining);
      (timer as any)?.unref?.();
    };
    const rearm = () => { if (!closed) { pause(); remaining = ms; arm(); } };
    off = visibility?.onChange
      ? visibility.onChange(() => { if (visibility.hidden()) pause(); else arm(); })
      : visibility?.onVisible(rearm);
    arm();
    return {clear, rearm};
  }
  /** The start and its foreground deadline retire together on readiness or replacement. */
  private armStartup(expire: () => void) {
    this.clearStartup();
    const deadline = this.visibleDeadline(this.options.startupTimeoutMs ?? 20000, expire);
    this.startupWait = deadline.clear;
    this.startupRearm = deadline.rearm;
  }
  /** Channel selection uses this same service/adapter owner, but never a VOD
   * create, persisted catalog placeholder, resume checkpoint or chapter query. */
  async playChannel(reference:ChannelReference,options:ChannelStartOptions={}):Promise<void> {
    const state=options.state??'playing';
    if(state!=='playing'&&state!=='paused')throw new Error('invalid_channel_start_state');
    if(options.signal?.aborted||options.isCurrent?.()===false)throw Object.assign(new Error('cancelled'),{name:'AbortError'});
    const oldVOD=this.snapshot.channel?undefined:this.snapshot.session?.id;
    this.leave();this.channelReference=reference;this.channelWantedState=state;
    const intentId=this.snapshot.intentId;
    this.channelSeekId=undefined;this.channelObservedSeek=undefined;this.channelSubmittedSeek=undefined;
    this.update({channel:reference,phase:'starting',intent:state,itemId:reference.channelId,playbackRun:this.snapshot.playbackRun+1});
    if(!this.channels){this.update({phase:'error',error:'Channel playback is not configured on this client.'});return;}
    // Release the exact prior VOD before live admission. This is resource
    // cleanup, not a predicted capacity check or a profile-wide singleton.
    if(oldVOD){try{await this.persist();await this.tryCleanup(oldVOD);}catch{
      if(this.snapshot.intentId===intentId)this.update({phase:'error',error:'The previous playback could not be stopped. Retry this channel when the server reconnects.'});return;
    }}
    if(this.snapshot.intentId!==intentId||!this.snapshot.channel||options.signal?.aborted||options.isCurrent?.()===false)return;
    await this.channels.open(reference,{
      state:value=>{if(this.snapshot.intentId===intentId&&this.snapshot.channel)this.applyChannel(value);},
      preparation:value=>{if(this.snapshot.intentId===intentId&&this.snapshot.channel)this.reportStartPreparation(intentId,value);},
      error:(message,terminal)=>{if(this.snapshot.intentId!==intentId||!this.snapshot.channel)return;this.update({error:message,...(terminal?{phase:'error' as const,session:undefined,pendingSeek:undefined}:{} )});}
    },{...options,state});
  }
  /** Channels on v1 sessions (spec §18.6): the apps switch to this when the server offers them. */
  useChannelPlayback(control:ChannelPlayback){if(this.snapshot.channel)this.leave();this.channels=control;}
  private applyChannel(state:ChannelView){
    const linear=state.linear,m=linear.media,seek=linear.desired.seek;
    // B-11.27: the owner lets recordings take a watched tuner and this one's moment has come (or
    // the server already gave it away). Stop here, once, and say why; nothing retries.
    if(tunerHandedOver(m,this.options.now?.()??Date.now())){this.handTunerOver(linear);return;}
    const changed=this.snapshot.session?.generation!==Number(m.bufferGeneration);
    let patch:Partial<PlaybackSnapshot>={linear,channel:linear.channel,linearStalled:state.status==='recoverable',intent:this.channelWantedState==='paused'?'paused':linear.desired.state,error:m.errorCode?channelMessage(m.errorCode):undefined};
    // No engine attachment until an ordered, server-resolved join target exists.
    if(m.streamUrl&&seek&&seek.bufferGeneration===m.bufferGeneration&&seek.positionUs!==null){
      const target=channelSeconds(seek.positionUs);
      if(!this.snapshot.session||changed){
        const generation=Number(m.bufferGeneration);if(!Number.isSafeInteger(generation)){this.fail(this.snapshot.intentId,'Channel generation is not supported.');return;}
        patch.session={id:state.playbackId,generation,streamUrl:m.streamUrl,mode:'hls',duration:0,resumeSeconds:target};
        patch.phase='starting';patch.duration=0;this.channelSeekId=undefined;this.channelObservedSeek=undefined;this.channelSubmittedSeek=undefined;
      }else if(this.snapshot.phase==='error')patch.phase='error';
      if(seek.seekCommandId!==this.channelSeekId){
        this.clearSeek();this.channelSeekId=seek.seekCommandId;this.channelSubmittedSeek=undefined;
        const revision=this.snapshot.seekRevision+1,intent=this.snapshot.intentId;
        patch.seekRevision=revision;patch.pendingSeek=Object.freeze({revision,positionSeconds:target});patch.failedSeek=undefined;patch.seekError=undefined;
        const deadline=this.visibleDeadline(this.options.seekTimeoutMs??15000,()=>this.seekFailed(intent,revision,'That position is no longer available. Choose Go Live or another position.'));
        this.seekWait=deadline.clear;
      }
      if(linear.acknowledgedSeekCommandId===seek.seekCommandId&&this.channelSubmittedSeek===seek.seekCommandId&&this.channelObservedSeek!==seek.seekCommandId&&!linear.seekError){
        this.channelObservedSeek=seek.seekCommandId;this.clearSeek();patch.pendingSeek=undefined;patch.seekError=undefined;patch.failedSeek=undefined;
        if(linear.confirmedPositionUs!==null)patch.positionSeconds=channelSeconds(linear.confirmedPositionUs);
      }
    }else if(changed&&this.snapshot.session){
      this.clearSeek();patch.session=undefined;patch.pendingSeek=undefined;patch.phase='starting';patch.seekError='The retained window changed. Choose Go Live to rejoin.';
    }
    if(linear.seekError){this.clearSeek();patch.failedSeek=patch.pendingSeek??this.snapshot.pendingSeek??this.snapshot.failedSeek;patch.pendingSeek=undefined;patch.seekError=channelMessage(linear.seekError);}
    // Expiry is recovery, not a successful clamped seek. Freeze the engine until
    // an explicit new target is accepted; the rolling producer remains active.
    const observedPosition=patch.positionSeconds??this.snapshot.positionSeconds;
    if(m.streamUrl&&this.channelObservedSeek&&!patch.pendingSeek&&!this.snapshot.pendingSeek&&
       (observedPosition<channelSeconds(m.windowStartUs)||observedPosition>channelSeconds(m.windowEndUs)+1)){
      patch.seekError='That position has left the retained window. Choose Go Live.';
      patch.failedSeek={revision:this.snapshot.seekRevision,positionSeconds:observedPosition};
    }
    this.update(patch);
  }
  private handTunerOver(linear:ChannelView['linear']){
    const intentId=this.snapshot.intentId;
    this.channels?.close();this.channelSeekId=undefined;this.channelObservedSeek=undefined;this.channelSubmittedSeek=undefined;
    this.fail(intentId,sessionEndMessage({reason:TUNER_RESERVED}),{ended:Object.freeze({reason:TUNER_RESERVED}),linear,linearStalled:false,session:undefined});
  }
  goLive(){if(this.snapshot.channel){this.channelWantedState='playing';this.channels?.goLive();}}
  getQueueConnection(){return this.queueConnection;}
  setQueueConnection(connection:{error?:string;retry?:()=>void}){this.queueConnection=connection;this.update({});}
  getQueueController(){return this.queue;}
  installQueue(queue:QueueController){this.queue=queue;this.update({});return()=>{if(this.queue===queue){this.queue=undefined;this.update({});}};}
  /** `fresh`: the viewer started a new playback (a new queue), not the queue moving on. */
  beginQueueIntent(itemId?:string,fresh=false,state:'playing'|'paused'='playing'):number{
    // Live uses another lane and never becomes a queue replacement predecessor.
    if(this.snapshot.channel)this.leave(false);
    const run=fresh?{playbackRun:this.snapshot.playbackRun+1}:{};
    // A parked restore or server-selected start has no new per-title choices to retain.
    if(fresh&&(state==='paused'||itemId===undefined)){this.lastPlay=undefined;this.escalations=0;}
    if(fresh&&this.options.beforeFreshPlayback)this.freshReady=this.options.beforeFreshPlayback().catch(()=>{});
    if(!this.snapshot.session){this.clearStartup();this.update({phase:'starting',intent:state,itemId,error:undefined,preparation:undefined,...run});}
    else this.update({intent:state,error:undefined,preparation:undefined,...run});
    return this.snapshot.intentId;
  }
  /** Presentation only: the queue/channel start retains the actual original
   * intent and clears this status on settlement. It never installs a session. */
  reportStartPreparation(intentId:number,preparation:StartPreparation|undefined):void{
    if(this.snapshot.intentId===intentId)this.update({preparation});
  }
  async prepareQueueIntent(intentId:number):Promise<boolean>{
    if(this.snapshot.intentId!==intentId||this.snapshot.channel)return false;
    const fresh=this.freshReady;this.freshReady=undefined;
    await fresh;
    await this.channels?.recover();
    return this.snapshot.intentId===intentId&&!this.snapshot.channel;
  }
  queueEnded(expectedIntentId:number){if(this.snapshot.intentId===expectedIntentId&&this.snapshot.phase==='ended')this.update({intent:'paused'});}
  queueFailed(itemId:string,message:string){if(!this.snapshot.channel&&!this.snapshot.session)this.update({phase:'error',itemId,intent:'paused',error:message});}
  async adoptQueueSession(itemId:string,session:PlaybackSession,expectedIntentId:number):Promise<boolean>{
    if(this.snapshot.intentId!==expectedIntentId)return false;
    // A title the queue started (Next, a container, the next episode) is the one an engine
    // failure asks for again; one started through `play` keeps the choices it was started with.
    if(this.lastPlay?.itemId!==itemId||this.lastPlay.local){this.lastPlay={itemId};this.escalations=0;}
    const intent=this.snapshot.intent;
    this.leave(false,true);this.sequence=0;this.lastProgress=0;
    const intentId=this.snapshot.intentId;
    this.update({phase:'starting',itemId,intent,error:undefined});
    this.requestSeek(session.resumeSeconds,{session,duration:session.duration,audioEffects:{...this.snapshot.audioEffects,rendering:renderable(session)&&(audioEffectsEnabled(this.snapshot.audioEffects.settings)||this.deviceAudioProcessing),prepared:undefined,committed:undefined}});
    this.audioPlanDiagnostic(session);
    if(this.snapshot.intentId===intentId&&this.snapshot.phase!=='ready'){
      this.armStartup(()=>{this.reportStalledStart(intentId);this.fail(intentId,'The selected queue item did not become ready. Retry playback or choose another item.');});
    }
    await this.persist();return this.snapshot.intentId===intentId&&!this.snapshot.cleanupError;
  }
  /**
   * A v1 audio session the native owner plays (plan §9.3 C3). `start` hands the session over (the
   * owner's first snapshot arrives through `applyNativeQueueEvent`); from then on pause, resume,
   * seek, rate, retry, timers and queue moves go to `native`, and this service never reports the
   * session's timeline or stops it itself.
   */
  async adoptNativeQueueSession(itemId:string,expectedIntentId:number,native:NativeListeningV1Control,start:()=>Promise<void>):Promise<boolean>{
    if(this.snapshot.intentId!==expectedIntentId)return false;
    const intent=this.snapshot.intent;
    const deadlines=this.snapshot.listeningDeadlines??noListeningDeadlines;
    this.leave(false,true);this.sequence=0;this.lastProgress=0;
    this.update({phase:'starting',itemId,intent,error:undefined});
    this.nativeV1=native;
    try{await start();}catch(error){if(this.nativeV1===native){this.nativeV1=undefined;void native.detach(true).catch(()=>{});}throw error;}
    if(this.nativeV1!==native)return false;
    if(this.listeningPolicy){this.nativePolicyKey='';this.syncListeningPolicy();}
    else await native.deadlines(deadlines).catch(()=>{});
    if(this.snapshot.phase!=='ready'&&this.snapshot.phase!=='error'&&this.nativeV1===native){
      this.armStartup(()=>{if(this.nativeV1===native&&this.snapshot.phase==='starting')this.fail(this.snapshot.intentId,'The selected queue item did not become ready. Retry playback or choose another item.');});
    }
    return true;
  }
  /** The native v1 owner's snapshot, with its session in this service's shape. */
  applyNativeQueueEvent(event:NativeListeningV1Event,session:PlaybackSession){
    if(!this.nativeV1)return;
    const before=this.snapshot;
    if(before.nativeListening?.owner===event.owner&&event.sequence<=before.nativeListening.sequence)return;
    const changed=before.session?.id!==session.id||before.session?.generation!==session.generation;
    if(event.ready)this.clearStartup();
    if(event.observed&&!event.seeking){this.clearSeek();this.bookmarkProtected=false;this.hasObserved=true;this.lastProgress=event.positionSeconds;}
    this.update({nativeListening:event,session,itemId:event.session.itemId??before.itemId,
      intentId:event.binding?.intentId??before.intentId,intent:event.intent,
      duration:session.duration,positionSeconds:event.observed?event.positionSeconds:(changed?session.resumeSeconds:before.positionSeconds),
      phase:event.terminal?'error':event.ended?'ended':event.ready?'ready':'starting',
      ...(changed?{audio:this.audioSelection.reset()}:{}),
      ...(event.observed&&!event.seeking?{pendingSeek:undefined,failedSeek:undefined,seekError:undefined}:{}),
      audioEffects:{...this.snapshot.audioEffects,rendering:event.rendering??false,eqApplied:event.eqApplied===true,levelingApplied:event.levelingApplied===true,levelingGainDb:appliedLevelingGain(event),rate:event.rate,effectiveRate:event.effectiveRate,waiting:event.rateWaiting,prepared:undefined,committed:undefined},
      error:event.error??undefined,listeningDeadlines:event.deadlines});
    // The server ended it (an administrator, a transfer, the lease): its reason, never an advance.
    if(event.terminal&&event.end)this.serverEnded(session.id,event.end);
    // A terminal owner acts on nothing more: let go of it, so Retry starts afresh (a new session,
    // handed to a new owner) instead of asking the finished one.
    if(event.terminal){const native=this.nativeV1;this.nativeV1=undefined;void native?.detach(true).catch(()=>{});}
  }
  /** Whether a native v1 owner holds the current session (queue moves then go through it). */
  hasNativeOwner(){return !!this.nativeV1;}
  async confirmQueueEnd(intentId:number):Promise<void>{
    const snapshot=this.snapshot,session=snapshot.session;
    if(snapshot.channel||intentId!==snapshot.intentId||snapshot.phase!=='ended'||!session||this.localSessions.has(session.id)||!this.hasObserved||this.bookmarkProtected)throw new Error('Playback completion is not confirmed.');
    const payload:ProgressCheckpoint={generation:session.generation,sequence:++this.sequence,positionSeconds:snapshot.positionSeconds,state:'ended'};
    this.progressPending.set(session.id,payload);await this.persist();
    await this.bounded(()=>this.api.progressPlayback(session.id,payload));
    if(this.progressPending.get(session.id)===payload)this.progressPending.delete(session.id);await this.persist();
  }
  /** `audioStream` is the absolute stream index of an audio track from the
   * offer. The server may change the delivery mode to reach it (an original whose
   * second track the engine cannot select is repackaged instead). */
  play(itemId: string, startPosition?: number, prepared?: PreparedChoice, quality?: string, audioStream?: number, choices?: PlaybackChoices): Promise<void> {
    if(this.snapshot.listeningNotice)this.update({listeningNotice:undefined});
    if (this.lastPlay?.itemId !== itemId) this.escalations = 0;
    this.lastPlay = { itemId, prepared, quality, audioStream, choices };
    if(this.queue)return this.queue.playItem(itemId,startPosition,prepared,quality,audioStream,choices);
    if(prepared){this.queueFailed(itemId,"Prepared playback requires the device queue. Reconnect it before choosing a version.");return Promise.resolve();}
    if(this.options.queueRequired){if(this.snapshot.channel)this.leave();this.queueFailed(itemId,this.queueConnection.error??'The device queue is still connecting. Retry once it is ready.');return Promise.resolve();}
    this.update({playbackRun:this.snapshot.playbackRun+1});
    return this.startPlayback(itemId, startPosition, "playing");
  }
  /**
   * OS-07: a title stored on this device (a download). It never goes through the device queue and
   * never asks the server for a session: `session` makes the local one (a fresh id each time, also
   * for a retry), and its progress and stop go to this service's PlaybackApi like any other, which
   * keeps them on the device (see the Apple app's offline playback).
   */
  async playLocal(itemId: string, session: () => PlaybackSession, startPosition?: number, context?: PlaybackLocalStartContext): Promise<void> {
    if(!localStartCurrent(context))throw localStartCancelled();
    const requested=context?.initialIntent,intent=requested===undefined?"playing":requested;
    if(intent!=="playing"&&intent!=="paused")throw new Error('invalid_local_start_intent');
    if(this.snapshot.listeningNotice)this.update({listeningNotice:undefined});
    this.escalations = 0;
    const captured=context?Object.freeze({signal:context.signal,isCurrent:context.isCurrent,initialIntent:intent}):undefined;
    this.lastPlay = { itemId, local: session, localContext:captured };
    this.update({ playbackRun: this.snapshot.playbackRun + 1 });
    return this.startLocal(itemId, session, startPosition, intent, captured);
  }
  private async startLocal(itemId: string, make: () => PlaybackSession, startPosition: number | undefined, intent: "playing" | "paused", context?:PlaybackLocalStartContext): Promise<void> {
    if(!localStartCurrent(context))throw localStartCancelled();
    this.endedSeekPosition = undefined;
    this.leave();
    this.failedRecovery = { positionSeconds: startPosition, intent };
    const intentId = this.snapshot.intentId;
    const cancel=()=>{if(this.snapshot.intentId===intentId)this.leave();};
    const check=()=>{
      if(this.snapshot.intentId!==intentId||!localStartCurrent(context)){
        cancel();throw localStartCancelled();
      }
    };
    this.update({ phase: "starting", intent, itemId, error: undefined });
    let detach:()=>void=()=>{},base:PlaybackSession|undefined;
    try {
      check();
      // Recovery belongs to the service, so a canceled row stops waiting without canceling
      // the shared journal read or losing cleanup for an earlier accepted session.
      const recovery=this.startRecovery();
      if(context?.signal){
        const signal=context.signal;
        await new Promise<void>((resolve,reject)=>{
          const abort=()=>{cancel();reject(localStartCancelled());};
          detach=()=>signal.removeEventListener('abort',abort);
          signal.addEventListener('abort',abort,{once:true});
          if(signal.aborted)abort();
          recovery.then(resolve,reject);
        });
      }else await recovery;
      check();
      this.sequence = 0;
      this.lastProgress = 0;
      let session:PlaybackSession;
      try {
        const localOwner=this.api.localPlayback;
        base=make();
        if(base.mode==='local'&&base.protocol===undefined){
          // Capture custody before checking cancellation: even a late maker's actual local
          // resource must retire through its local owner, never a server session route.
          if(this.cleanup.has(base.id)||this.progressPending.has(base.id)||this.localSessions.has(base.id))throw new Error('local_session_identity_reused');
          this.localSessions.set(base.id,localOwner);
        }
        check();
        session={...base,resumeSeconds:startPosition??base.resumeSeconds};
        check();
      } catch {
        // A maker can synchronously retire its caller or start a replacement. Neither a
        // late return nor an exception from that old maker may become the new UI's error.
        check();
        if(base&&this.localSessions.has(base.id)&&this.snapshot.session?.id!==base.id)this.enqueueCleanup(base.id);
        this.clearStartup();
        this.update({phase:"error",intent:"paused",error:serviceText('playback.message.internal_error')});
        return;
      }
      this.armStartup(() => {
        if (this.snapshot.intentId !== intentId) return;
        const session = this.snapshot.session;
        this.clearSeek();
        this.update({ audio: this.audioSelection.reset(), pendingSeek: undefined, failedSeek: undefined, phase: "error", intent: "paused", session: undefined, error: serviceText('playback.message.timeout') });
        if (session) this.enqueueCleanup(session.id);
      });
      check();
      this.requestSeek(session.resumeSeconds, { session, duration: session.duration });
      check();
      this.persist();
    } catch(error) {
      if(base&&this.snapshot.session?.id!==base.id)this.enqueueCleanup(base.id);
      // Existing three-argument callers quietly finish when another Play replaces them.
      if(!context&&this.snapshot.intentId!==intentId&&error instanceof Error&&error.name==='AbortError')return;
      throw error;
    } finally {detach();}
  }
  private startupProbe?: () => Readonly<Record<string, unknown>>;
  /** Development aid (demo, 23 Sep: a start after a Metro reload never produced a frame): the
   * engine adapter says what a start is waiting on (route ready, load command sent, native ready),
   * logged in development builds when the startup timer fails a start. */
  setStartupProbe(probe: (() => Readonly<Record<string, unknown>>) | undefined) { this.startupProbe = probe; }
  private reportStalledStart(intentId: number) {
    if (!this.options.diagnostics || intentId !== this.snapshot.intentId) return;
    const s = this.snapshot;
    let engine: unknown;
    try { engine = this.startupProbe?.(); } catch (e) { engine = String(e); }
    console.warn('[playback] start timed out', JSON.stringify({session: s.session?.id, protocol: s.session?.protocol, phase: s.phase, intent: s.intent, pendingSeek: s.pendingSeek?.positionSeconds, engine}));
  }
  /** Records a marker skip (spec §4.2) against the playing session; the caller seeks. Evidence
   * only: a failure is dropped. `automatic` is for the viewer's `auto` preference on a marker the
   * server marks safe. */
  markerSkipped(markerId: string, mode: 'automatic' | 'manual', positionSeconds: number = this.snapshot.positionSeconds): void {
    const session = this.snapshot.session;
    if (!session || this.localSessions.has(session.id) || this.snapshot.channel || !markerId) return;
    void this.api.markerSkipped?.(session.id, {markerId, mode, positionMs: Math.max(0, Math.round(positionSeconds * 1000))}).catch(() => {});
  }
  /** Where a retry of the current title starts: the target of a start or seek that failed or
   * never settled (a resume point included), else the last observed position. A title that never
   * played retries at its requested start, never at 0:00. */
  recoveryTarget(): number|undefined {
    const s=this.snapshot;
    return this.failedRecovery?.positionSeconds??s.pendingSeek?.positionSeconds??s.failedSeek?.positionSeconds??(this.hasObserved?s.positionSeconds:s.session?.resumeSeconds);
  }
  /** The intent before the current failure paused its error surface. */
  recoveryIntent(): "playing" | "paused" { return this.failedRecovery?.intent ?? this.snapshot.intent; }
  /** Error retry preserves the requested target and intent; ordinary Play starts playing. */
  retry(): Promise<void> {
    if(this.snapshot.channel){
      if(this.snapshot.phase==='error'&&this.channelReference)return this.playChannel(this.channelReference);
      this.channels?.retry();this.update({error:undefined});return Promise.resolve();
    }
    if (this.snapshot.phase !== "error" || !this.snapshot.itemId) return Promise.resolve();
    const recovery = this.failedRecovery ?? { intent: this.snapshot.intent };
    if(this.nativeV1)return this.nativeV1.action('retry',recovery.positionSeconds??this.snapshot.positionSeconds);
    // A local title retries locally; it never belonged to the device queue or the server.
    if(this.lastPlay?.local&&this.lastPlay.itemId===this.snapshot.itemId)return this.startLocal(this.snapshot.itemId, this.lastPlay.local, recovery.positionSeconds??this.snapshot.positionSeconds, recovery.intent, this.lastPlay.localContext);
    if(this.queue||this.options.queueRequired){return this.queue?.check(true)??Promise.resolve();}
    return this.startPlayback(this.snapshot.itemId, recovery.positionSeconds, recovery.intent);
  }
  private async startPlayback(itemId: string, startPosition: number | undefined, intent: "playing" | "paused"): Promise<void> {
    this.endedSeekPosition = undefined;
    this.leave();
    // Preserve the command before any journal, cleanup, create or readiness failure.
    this.failedRecovery = { positionSeconds: startPosition, intent };
    const intentId = this.snapshot.intentId;
    this.update({
      phase: "starting",
      intent,
      itemId,
      error: undefined,
    });
    await this.startRecovery();
    if (this.snapshot.intentId !== intentId) return;
    if (
      this.cleanup.size + this.creates.size >=
        (this.options.maxPendingCleanup ?? 32) ||
      this.snapshot.cleanupError
    ) {
      this.update({
        phase: "error",
        intent: "paused",
        error:
          "Playback cleanup needs to reconnect before another session can start.",
      });
      return;
    }
    this.sequence = 0;
    this.lastProgress = 0;
    let expired = false;
    this.armStartup(() => {
      expired = true;
      if (this.snapshot.intentId === intentId) {
        const session = this.snapshot.session;
        this.clearSeek();
        this.update({
          audio: this.audioSelection.reset(),
          pendingSeek: undefined,
          failedSeek: undefined,
          phase: "error",
          intent: "paused",
          session: undefined,
          error:
            "Playback did not become ready. Check the server and try again.",
        });
        if (session) this.enqueueCleanup(session.id);
      }
    });
    // Persist the request receipt before sending so ambiguous creates can be replayed.
    const requestId = this.requestId();
    this.creates.set(requestId, { itemId, running: true });
    await this.persist();
    if (this.snapshot.cleanupError) {
      this.clearStartup();
      this.update({
        phase: "error",
        intent: "paused",
        error: this.snapshot.cleanupError,
      });
      return;
    }
    if (this.snapshot.intentId !== intentId || expired) {
      this.creates.delete(requestId);
      void this.persist();
      return;
    }
    try {
      const receivedSession = await this.bounded((signal) =>
        this.api.createPlayback(itemId, requestId, signal).then((value) => {
          if (this.snapshot.intentId !== intentId || expired) {
            this.creates.delete(requestId);
            this.enqueueCleanup(value.id);
          }
          return value;
        })
      );
      const session = {
        ...receivedSession,
        // The server owns completion/resume policy, including near-end audiobook
        // bookmarks. Only an explicit caller position (including zero) overrides it.
        resumeSeconds: startPosition ?? receivedSession.resumeSeconds,
      };
      this.creates.delete(requestId);
      if (this.snapshot.intentId !== intentId || expired) {
        this.enqueueCleanup(session.id);
        return;
      }
      this.requestSeek(session.resumeSeconds, {session, duration: session.duration});
      this.persist();
    } catch (error) {
      const pending = this.creates.get(requestId);
      if (pending) pending.running = false;
      if (
        error instanceof ApiError &&
        [400, 401, 403, 404, 409, 422].includes(error.status)
      )
        this.creates.delete(requestId);
      this.persist();
      if (this.snapshot.intentId === intentId && !expired) {
        this.clearStartup();
        this.update({
          phase: "error",
          intent: "paused",
          error:
            error instanceof Error
              ? error.message
              : "Playback could not start.",
        });
      }
    }
  }
  pause() {
    this.nativeResumeRevision++;
    this.queue?.invalidateAudioPreparation?.();
    if(this.nativeV1){void this.nativeV1.action('pause').catch(()=>{});if(this.snapshot.phase!=='idle')this.update({intent:'paused'});return;}
    if(this.snapshot.channel){this.channelWantedState="paused";this.update({intent:"paused"});this.channels?.pause();return;}
    if (this.snapshot.phase !== "idle") {
      if (this.failedRecovery) this.failedRecovery = { ...this.failedRecovery, intent: "paused" };
      if (this.snapshot.phase === "ready") this.clearStartup();
      this.update({ intent: "paused" });
      this.checkpoint("paused");
    }
  }
  resume() {
    this.update({listeningNotice:undefined});
    this.checkListeningDeadlines();
    if(this.nativeV1){
      const native=this.nativeV1,revision=++this.nativeResumeRevision;
      void (this.nativePolicyKey?Promise.resolve(true):this.syncListeningPolicy()).then(ok=>{
        if(ok&&this.nativeV1===native&&revision===this.nativeResumeRevision)return native.action('resume');
      }).catch(()=>{});return;
    }
    if(this.snapshot.channel){
      if(this.snapshot.seekError||this.snapshot.failedSeek){this.update({error:'Choose Go Live or a position in the retained window before resuming.'});return;}
      this.channelWantedState="playing";this.channels?.resume();return;
    }

    if (this.snapshot.phase === "ended" && this.snapshot.itemId) {
      // Replay: the same title again with the choices it was playing with.
      const last = this.lastPlay?.itemId === this.snapshot.itemId && !this.lastPlay.local ? this.lastPlay : undefined;
      void this.play(this.snapshot.itemId, this.endedSeekPosition ?? 0, last?.prepared, last?.quality, last?.audioStream, last?.choices);
      return;
    }
    if (this.snapshot.phase === "starting" || this.snapshot.phase === "ready") {
      if (this.failedRecovery) this.failedRecovery = { ...this.failedRecovery, intent: "playing" };
      if ((this.bookmarkProtected || this.snapshot.failedSeek) && this.snapshot.session && !this.snapshot.pendingSeek) {
        this.requestSeek(this.snapshot.failedSeek?.positionSeconds ?? this.snapshot.session.resumeSeconds, {intent: "playing"});
        return;
      }
      this.update({ intent: "playing" });
    }
  }
  private clearSeek() {
    this.seekWait?.();
    this.seekWait = undefined;
    this.seekRearm = undefined;
  }
  private requestSeek(positionSeconds: number, patch: Partial<PlaybackSnapshot> = {}) {
    this.clearSeek();
    const duration = patch.duration ?? this.snapshot.duration;
    const target = Math.max(0, Math.min(positionSeconds, duration || positionSeconds));
    this.failedRecovery = { positionSeconds: target, intent: patch.intent ?? this.snapshot.intent };
    const revision = this.snapshot.seekRevision + 1, intentId = this.snapshot.intentId;
    const deadline = this.visibleDeadline(this.options.seekTimeoutMs ?? 15000, () => this.seekFailed(intentId, revision, "That position is not available to seek yet. Try again."));
    this.seekWait = deadline.clear;
    this.seekRearm = deadline.rearm;
    this.update({...patch, seekRevision: revision, pendingSeek: Object.freeze({revision, positionSeconds: target}), seekError: undefined, failedSeek: undefined});
  }
  seek(positionSeconds: number) {
    if(this.queue?.seekRetiringAudio?.(positionSeconds))return;
    this.queue?.invalidateAudioPreparation?.();
    if(this.snapshot.channel){this.channels?.seek(positionSeconds);return;}
    if (!this.snapshot.session || !Number.isFinite(positionSeconds) || !["starting", "ready", "ended"].includes(this.snapshot.phase)) return;
    if (this.snapshot.phase === "ended") {
      this.endedSeekPosition = Math.max(0, Math.min(positionSeconds, this.snapshot.duration || positionSeconds));
      return;
    }
    if(this.nativeV1){void this.nativeV1.action('seek',positionSeconds).catch(()=>{});return;}
    this.requestSeek(positionSeconds);
  }
  /** Only a correlated adapter completion acknowledges a seek; ordinary time facts do not. */
  seekApplied(intentId: number, revision: number, observedSeconds: number, state: "playing" | "paused" | "ended"): boolean {
    const pending = this.snapshot.pendingSeek;
    if(this.snapshot.channel){
      if(intentId!==this.snapshot.intentId||!pending||pending.revision!==revision||!Number.isFinite(observedSeconds)||Math.abs(observedSeconds-pending.positionSeconds)>.5||!this.channelSeekId)return false;
      if(this.channelSubmittedSeek!==this.channelSeekId){const seekId=this.channelSeekId;this.channelSubmittedSeek=seekId;void this.channels?.observe(observedSeconds,seekId).then(ok=>{if(!ok&&this.snapshot.intentId===intentId&&this.channelSubmittedSeek===seekId)this.channelSubmittedSeek=undefined;});}
      return true;
    }
    if (intentId !== this.snapshot.intentId || !this.snapshot.session || !pending || pending.revision !== revision || !["starting", "ready"].includes(this.snapshot.phase) || !Number.isFinite(observedSeconds) || observedSeconds < 0 || this.snapshot.duration > 0 && observedSeconds > this.snapshot.duration || Math.abs(observedSeconds-pending.positionSeconds) > 0.5 || !["playing", "paused", "ended"].includes(state)) return false;
    this.clearSeek();
    this.hasObserved = true;
    this.bookmarkProtected = false;
    this.update({positionSeconds: observedSeconds, pendingSeek: undefined, seekError: undefined, failedSeek: undefined});
    this.lastProgress = observedSeconds;
    this.checkpoint(state);
    return true;
  }
  /** Reports that media has stopped arriving and the buffer is empty (or that it is flowing
   * again). Anything short of that is the adapter's business and is never shown. */
  recovering(intentId: number, active: boolean) {
    if (intentId !== this.snapshot.intentId || this.snapshot.phase === "error" || this.snapshot.phase === "idle") return;
    if (!!this.snapshot.recovering !== active) this.update({ recovering: active || undefined });
  }
  seekFailed(intentId: number, revision: number, message: string) {
    if (intentId !== this.snapshot.intentId || !this.snapshot.session || this.snapshot.pendingSeek?.revision !== revision) return;
    this.clearSeek();
    if(this.snapshot.channel)void this.channels?.observe(this.snapshot.positionSeconds,null,'engine_seek_failed');
    const safe = typeof message === "string" && message.trim() && message.length <= 512 && !/[\x00-\x1f\x7f]/.test(message) ? message : "That position could not be reached. Try again.";
    this.update({failedSeek: this.snapshot.pendingSeek, pendingSeek: undefined, seekError: safe, intent: "paused" as const});
  }
  /** A lost engine restores an outstanding seek first, then the last settled observation. */
  engineLost(intentId: number) {
    if(this.snapshot.channel){
      if(intentId===this.snapshot.intentId&&this.snapshot.session&&this.channelObservedSeek)this.channels?.seek(this.snapshot.positionSeconds);
      return;
    }
    if (intentId !== this.snapshot.intentId || !this.snapshot.session || !["starting", "ready"].includes(this.snapshot.phase)) return;
    const target = this.snapshot.pendingSeek?.positionSeconds ?? (this.hasObserved ? this.snapshot.positionSeconds : this.snapshot.session.resumeSeconds);
    this.bookmarkProtected = true;
    this.requestSeek(target);
  }
  ready(intentId: number) {
    if (
      intentId === this.snapshot.intentId &&
      this.snapshot.session &&
      this.snapshot.phase === "starting"
    ) {
      this.clearStartup();
      this.update({ phase: "ready" });
    }
  }
  fact(
    intentId: number,
    positionSeconds: number,
    state: "playing" | "paused" | "ended"
  ) {
    if(this.snapshot.channel){
      if(intentId!==this.snapshot.intentId||!this.snapshot.session||this.snapshot.pendingSeek||this.snapshot.failedSeek||!Number.isFinite(positionSeconds))return;
      if(state==='ended'){this.update({error:'The channel stream ended. Retry or choose Go Live.'});return;}
      this.update({positionSeconds});void this.channels?.observe(positionSeconds);return;
    }
    if (
      intentId !== this.snapshot.intentId ||
      !this.snapshot.session ||
      !!this.snapshot.pendingSeek ||
      !!this.snapshot.failedSeek ||
      !["starting", "ready"].includes(this.snapshot.phase) ||
      !Number.isFinite(positionSeconds) ||
      positionSeconds < 0
    )
      return;
    if (
      state === "playing" &&
      positionSeconds > this.snapshot.session.resumeSeconds + 0.01
    )
      this.clearStartup();
    this.hasObserved = true;
    // An element that has already reported its end keeps reporting it on every
    // later event. Only the first report is a transition; later ones must not
    // pause an intent the queue has since set to playing for the next item.
    if (state === "ended" && this.snapshot.phase === "ended") return;
    if (state === "ended") this.clearSeek();
    this.update({
      positionSeconds,
      ...(state === "ended"
        ? { phase: "ended" as const, intent: "paused" as const, pendingSeek: undefined, audio: this.audioSelection.reset() }
        : {}),
    });
    if (
      state !== "playing" ||
      Math.abs(positionSeconds - this.lastProgress) >= 3
    ) {
      this.lastProgress = positionSeconds;
      this.checkpoint(state);
    }
  }
  /** The engine rejected the stream it was given: a decode error, an unsupported
   * source, a manifest it cannot read. Not the network, and not the server saying
   * no. A capability document can be wrong, so this is not yet a failure the
   * viewer should see: the server is told which route failed, and when there is a
   * route above it the title is asked for again from the same position and plays
   * through that one instead. The viewer sees the recovery state, never an error,
   * unless the last resort has failed too. */
  engineFailed(intentId: number, code: PlayerFailureCode, detail: string, message: string) {
    const session = this.snapshot.session, itemId = this.snapshot.itemId;
    if (intentId !== this.snapshot.intentId || this.snapshot.phase === "error" || this.snapshot.phase === "idle") return;
    // The recovery belongs to the session being played, however it was started (a direct play, a
    // queue's first entry, or a queue's successor): the server re-plans that session in place.
    // Only a server session can be re-planned (a download or a prepared file is what it is). The
    // bound is per session: a re-plan keeps the session, a new title starts a new count.
    if (session && this.escalationSession !== session.id) { this.escalationSession = session.id; this.escalations = 0; }
    if (!session || !itemId || this.snapshot.channel || !this.api.reportPlayerFailure || session.protocol !== 'v1' || this.escalations >= 3) {
      this.fail(intentId, message);
      return;
    }
    const position = this.snapshot.pendingSeek?.positionSeconds ?? (this.hasObserved ? this.snapshot.positionSeconds : session.resumeSeconds ?? 0);
    this.escalations++;
    this.recovering(intentId, true);
    void this.api.reportPlayerFailure(session.id, code, detail, position).then(async result => {
      if (intentId !== this.snapshot.intentId) return;
      if (!result?.escalates || !result.session) { this.fail(intentId, message); return; }
      // The same session, another way: its new presentation replaces the old one in place, so
      // the queue and the position stay.
      const next = {...result.session, duration: result.session.duration || session.duration, resumeSeconds: position};
      if (!this.installPresentation(next)) await this.adoptQueueSession(itemId, next, intentId);
    }).catch(() => { if (intentId === this.snapshot.intentId) this.fail(intentId, message); });
  }
  fail(intentId: number, message: string, extra: Partial<PlaybackSnapshot> = {}) {
    if (intentId === this.snapshot.intentId && this.snapshot.phase !== "idle") {
      if (this.snapshot.phase !== "error") {
        this.failedRecovery = {
          positionSeconds: this.snapshot.pendingSeek?.positionSeconds ?? this.snapshot.failedSeek?.positionSeconds ?? (this.hasObserved ? this.snapshot.positionSeconds : this.snapshot.session?.resumeSeconds ?? this.failedRecovery?.positionSeconds),
          intent: this.snapshot.intent,
        };
      }
      this.clearStartup();
      this.clearSeek();
      this.update({ audio: this.audioSelection.reset(), phase: "error", intent: "paused", error: message, pendingSeek: undefined, seekError: undefined, failedSeek: undefined, recovering: undefined, ...extra });
    }
  }
  leave(notifyQueue=true,preserveListeningDeadlines=false) {
    this.nativeResumeRevision++;this.nativePolicyRevision++;this.nativePolicyKey='';this.nativePolicyPending=undefined;
    if(!preserveListeningDeadlines)this.listeningClock.clear();
    if(notifyQueue)this.queue?.leave();
    const wasChannel=!!this.snapshot.channel;
    // A native v1 owner reports and stops its own session; nothing here writes for it.
    const native=this.nativeV1;this.nativeV1=undefined;
    if(native)void native.detach(true).catch(()=>{});
    this.channels?.close();this.channelReference=undefined;this.channelSeekId=undefined;this.channelObservedSeek=undefined;this.channelSubmittedSeek=undefined;
    this.failedRecovery = undefined;
    this.clearStartup();
    this.clearSeek();
    if(!native)this.checkpoint("paused");
    this.hasObserved = false;
    this.bookmarkProtected = true;
    const old = this.snapshot.session;
    this.update({
      audioEffects:{...this.snapshot.audioEffects,rendering:false,eqApplied:false,levelingApplied:false,levelingGainDb:undefined,effectiveRate:0,waiting:false,prepared:undefined,committed:undefined},
      audio: this.audioSelection.reset(),
      audioTracks: undefined,
      intentId: this.snapshot.intentId + 1,
      phase: "idle",
      intent: "paused",
      itemId: preserveListeningDeadlines?this.snapshot.itemId:undefined,
      nativeListening:undefined,
      listeningDeadlines:preserveListeningDeadlines?this.snapshot.listeningDeadlines:noListeningDeadlines,
      channel: undefined,
      linear: undefined,
      session: undefined,
      pendingSeek: undefined,
      seekError: undefined,
      failedSeek: undefined,
      recovering: undefined,
      positionSeconds: 0,
      duration: 0,
      error: undefined,
    });
    if (old&&!wasChannel&&!native) this.enqueueCleanup(old.id);
  }
  private checkpoint(state: "playing" | "paused" | "ended") {
    if(this.snapshot.channel||this.nativeV1)return;
    const session = this.snapshot.session;
    if (!session || !this.hasObserved || this.bookmarkProtected) return;
    if(this.localSessions.has(session.id)&&!this.localSessions.get(session.id))return;
    this.progressPending.set(session.id, {
      generation: session.generation,
      sequence: ++this.sequence,
      positionSeconds: this.snapshot.positionSeconds,
      state,
    });
    this.persist();
    this.flushProgress();
  }
  private async bounded<T>(
    work: (signal: AbortSignal) => Promise<T>
  ): Promise<T> {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      return await Promise.race([
        work(controller.signal),
        new Promise<T>((_, reject) => {
          timer = setTimeout(() => {
            controller.abort();
            reject(new Error("Operation timed out"));
          }, this.options.operationTimeoutMs ?? 5000);
        }),
      ]);
    } finally {
      if (timer) clearTimeout(timer);
    }
  }
  private flushProgress() {
    for (const [id, payload] of this.progressPending) {
      if (this.progressRunning.has(id) || this.cleanup.has(id)) continue;
      const api=this.localSessions.has(id)?this.localSessions.get(id):this.api;
      if(!api)continue;
      this.progressRunning.add(id);
      void this.bounded(() => api.progressPlayback(id, payload)).then(
        () => {
          if (this.progressPending.get(id) === payload)
            this.progressPending.delete(id);
          this.progressRunning.delete(id);
          this.persist();
          if (
            this.progressPending.get(id) &&
            this.progressPending.get(id) !== payload
          )
            this.flushProgress();
        },
        () => {
          this.progressRunning.delete(id);
          this.persist();
        }
      );
    }
  }
  private enqueueCleanup(id: string) {
    if (!this.cleanup.has(id))
      this.cleanup.set(id, {
        running: false,
        checkpoint: this.progressPending.get(id),
      });
    this.update({});
    void this.persist().then(() => this.tryCleanup(id)).catch(() => {});
  }
  private tryCleanup(id: string): Promise<void> {
    const entry = this.cleanup.get(id);
    if (!entry) return Promise.resolve();
    if (entry.work) return entry.work;
    entry.running = true;
    entry.work = (async () => {
      try {
        const api=this.localSessions.has(id)?this.localSessions.get(id):this.api;
        const checkpoint = this.progressPending.get(id) ?? entry.checkpoint;
        if (checkpoint&&api) {
          try {
            await this.bounded(() => api.progressPlayback(id, checkpoint));
          } catch (error) {
            if (![404, 409, 410].includes(statusOf(error))) throw error;
          }
        }
        try {
          if(api)await this.bounded((signal) => api.stopPlayback(id, signal));
        } catch (error) {
          // Already ended (it played to its end) or already gone: there is nothing left to stop.
          if (![404, 410].includes(statusOf(error))) throw error;
        }
        this.cleanup.delete(id);
        this.progressPending.delete(id);
        this.localSessions.delete(id);
        this.update({});
        await this.persist();
      } catch (error) {
        entry.running = false;
        entry.work = undefined;
        this.update({});
        throw error;
      }
    })();
    // Background leave/retry callers do not await, but an explicit cross-kind
    // handoff awaits the same work and surfaces failure without a second player.
    void entry.work.catch(() => {});
    return entry.work;
  }
  retryCleanup() {
    for (const id of this.cleanup.keys()) this.tryCleanup(id);
    for (const [requestId, entry] of this.creates) {
      if (entry.running) continue;
      entry.running = true;
      void this.bounded((signal) =>
        this.api.createPlayback(entry.itemId, requestId, signal)
      ).then(
        (session) => {
          this.creates.delete(requestId);
          this.enqueueCleanup(session.id);
        },
        () => {
          entry.running = false;
        }
      );
    }
    this.flushProgress();
  }
  private persist(): Promise<void> {
    if (!this.options.journal) return Promise.resolve();
    const records: PlaybackJournalEntry[] = [...this.cleanup].filter(([id])=>!this.localSessions.has(id)).map(
      ([id, value]) => ({
        id,
        checkpoint: this.progressPending.get(id) ?? value.checkpoint,
      })
    );
    for (const [requestId, entry] of this.creates)
      records.push({ request: { requestId, itemId: entry.itemId } });
    // A native v1 owner's session is its own to stop (it holds the timeline and the lease).
    const active = this.nativeV1 ? undefined : this.snapshot.session;
    if (active && !this.localSessions.has(active.id) && !records.some((v) => v.id === active.id))
      records.push({
        id: active.id,
        checkpoint: this.progressPending.get(active.id),
      });
    this.journalTail = this.journalTail
      .then(() => this.options.journal!.save(records))
      .catch(() => {
        this.update({
          cleanupError:
            "Playback recovery could not be saved. Check secure storage.",
        });
      });
    return this.journalTail;
  }
}
