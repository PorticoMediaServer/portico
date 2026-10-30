import type {AudioEffectsSettings} from '../audio-effects.ts';
import type {QueueHeader,Session,SessionEnd} from '../playback-v1/types.ts';
import type {AudiobookPreferences} from '../audiobook-preferences.ts';
/** Absolute user deadlines; neither a render nor a lease renewal restarts them. */
export type ListeningDeadlines = Readonly<{sleepAtMs:number|null;passoutAtMs:number|null}>;
export const noListeningDeadlines:ListeningDeadlines=Object.freeze({sleepAtMs:null,passoutAtMs:null});
/** Non-authorizing policy. The native owner applies it to the session it plays; neither policy
 * revisions nor activity send playback commands. */
/** A timer follows this accepted occurrence, not another start of the same catalogue item. */
export type ListeningTimerOccurrence=Readonly<{sessionId:string;generation:number;itemId:string;playbackRun:number;queue:Readonly<{queueId:string;entryId:string}>|null;owner?:string}>;
/** Ordered membership read from the original scoped book timeline, never queue indices. */
export type ListeningBookBoundary=Readonly<{revision:Readonly<{catalog:number;viewer:number}>;items:readonly string[]}>;
export type ListeningPolicy=Readonly<{
 audiobook?:AudiobookPreferences;
 preferences:Readonly<{bookRate:number;passoutMinutes:number}>|null;
 activityRevision:number;activityAtMs:number;timerRevision:number;
 timer:Readonly<{choice:string|number;label:string;wallDeadline?:number;endSeconds?:number;itemId?:string;bookId?:string;sessionId?:string;generation?:number;libraryId?:string;occurrence?:ListeningTimerOccurrence;bookBoundary?:ListeningBookBoundary}>|null;
}>;
export type NativeListeningPolicy=ListeningPolicy&Readonly<{revision:number;effects:AudioEffectsSettings}>;
export type ListeningAction='pause'|'resume'|'seek'|'rate'|'retry'|'next'|'previous';
/** Captured at the caller's original output, never rebuilt after credentials/snapshot awaits.
 * The owner repeats this comparison on its own native queue before accepting a command. */
export type NativeListeningCommandTarget=Readonly<{
 binding:NonNullable<NativeListeningStatus['binding']>;
 commandRevision:number;
 queue:Readonly<{queueId:string;entryId:string}>|null;
}>;
/** What every native listening snapshot says. The player UI, the engine coordinator and the
 * listening policy read only this. */
export type NativeListeningStatus=Readonly<{
 owner:string; sequence:number; intent:'playing'|'paused'; rate:number;
 positionSeconds:number; effectiveRate?:number;supportedRates?:readonly number[];rateWaiting?:boolean;rateError?:string; observed:boolean; ready:boolean; seeking:boolean; ended:boolean;
 error:string|null; terminal:boolean; route:string; deadlines:ListeningDeadlines;
 /** Current-song engine facts. Omission is unreported, never evidence an effect ran. */
 rendering?:boolean;eqApplied?:boolean;levelingApplied?:boolean; /** Actual current-song applied leveling gain in dB, only while levelingApplied is true. */ levelingGainDb?:number;timerStopped?:boolean;expiredTimerRevision?:number;policyRevision?:number;
 commandRevision:number; binding:{lease:string;intentId:number;sessionId:string;generation:number;itemId:string}|null;
}>;
export function listeningDeadline(value:unknown):value is number|null {
 return value===null||typeof value==='number'&&Number.isSafeInteger(value)&&value>0;
}
/** Browser fallback. The monotonic check prevents a clock rollback extending a
 * timer; wall-clock checking on foreground catches suspension/forward changes. */
export class ListeningDeadlineClock {
 private value:ListeningDeadlines=noListeningDeadlines;
 private limits:Partial<Record<keyof ListeningDeadlines,number>>={};
 private timer?:ReturnType<typeof setTimeout>;
 private expire:(kind:'sleepAtMs'|'passoutAtMs')=>void;private wall:()=>number;private mono:()=>number;
 constructor(expire:(kind:'sleepAtMs'|'passoutAtMs')=>void,wall:()=>number=Date.now,mono:()=>number=()=>typeof performance==='undefined'?Date.now():performance.now()){this.expire=expire;this.wall=wall;this.mono=mono;}

 getSnapshot(){return this.value;}
 set(value:ListeningDeadlines){
  if(!listeningDeadline(value.sleepAtMs)||!listeningDeadline(value.passoutAtMs))throw new Error('Invalid listening deadline.');
  for(const key of ['sleepAtMs','passoutAtMs'] as const){if(value[key]!==this.value[key]){if(value[key]===null)delete this.limits[key];else this.limits[key]=this.mono()+Math.max(0,value[key]!-this.wall());}}
  this.value=Object.freeze({...value});this.schedule();
 }
 check(){for(const key of ['sleepAtMs','passoutAtMs'] as const){const at=this.value[key];if(at!==null&&(this.wall()>=at||this.mono()>=(this.limits[key]??Infinity))){this.value=Object.freeze({...this.value,[key]:null});delete this.limits[key];this.expire(key);}}this.schedule();}
 private schedule(){if(this.timer)clearTimeout(this.timer);this.timer=undefined;const pending=Object.values(this.limits);if(pending.length){this.timer=setTimeout(()=>this.check(),Math.max(1,Math.min(1000,...pending.map(at=>at-this.mono()))));(this.timer as any)?.unref?.();}}
 clear(){if(this.timer)clearTimeout(this.timer);this.timer=undefined;this.value=noListeningDeadlines;this.limits={};}
}

// ── Playback v1 (Plan — Client Playback Migration §9.3 C3) ──────────────────────────────
/**
 * The native owner of a v1 audio session (Apple): it reports the session's timeline (and so holds
 * its lease), pauses and resumes it, and moves the queue on (`:advance`, and §18's
 * `:prepare-next`/`:commit-next` for gapless and crossfade) while JS sleeps. JS hands a session
 * over once, with the `seq` it had reached, and from then on only sends intents; it never reports
 * the session's timeline itself. A queue move the owner made shows up in the next snapshot as a
 * `transition`: a new session (and the queue header it came with), which JS adopts as its own.
 */
export type NativeListeningV1Context=Readonly<{
 /** Original client playback run; older hosts may play but cannot arm occurrence-bound end timers without it. */
 playbackRun?:number;
 /** The v1 session (`parseSession`'s shape; it is also the wire shape). */
 session:Session;
 queueId:string|null; queueRevision:string|null;
 durationSeconds:number;
 /** Retired with the timeline: always 0. */
 lastSeq:number;
 /** The observer epoch JS reported under; the owner claims with it (`replaces`) to take over at once. */
 observerEpoch?:string;
 /** How much of the server's 120 s lease is left, as JS last knew it. */
 leaseMs:number;
 /** The server offers §18 queue transitions (`features.queueTransitions`). */
 transitions:boolean;
 intent:'playing'|'paused';
}>;
export type NativeListeningV1Event=NativeListeningStatus&Readonly<{
 /** The session the owner reports for now (after a transition, the new one). */
 session:Session; durationSeconds:number;
 queue:Readonly<{id:string;revision:string}>|null;
 /** The highest timeline `seq` the owner has sent for `session`. */
 lastSeq:number;
 /** Why the server ended the session (terminal events only). */
 end?:SessionEnd;
 /** A queue move the owner made while JS may have slept: the session it left, and the queue
  * header the move returned. Carried until JS acknowledges a later snapshot. */
 transition?:Readonly<{previousSessionId:string;queue:QueueHeader|null}>;
}>;
export type NativeQueueAdvance='next'|'previous'|'entry';
export interface NativeListeningV1Control {
 adopt(context:NativeListeningV1Context,sink:(event:NativeListeningV1Event)=>void):Promise<void>;
 action(action:ListeningAction,value?:number,expected?:NativeListeningCommandTarget):Promise<void>;
 /** A queue move the viewer asked for; the owner sends the `:advance` and plays its session. */
 advance(reason:NativeQueueAdvance,entryId?:string):Promise<void>;
 refresh():Promise<void>;
 deadlines(value:ListeningDeadlines):Promise<void>;
 configure?(policy:NativeListeningPolicy):Promise<boolean>;
 /** Ends native ownership; `stop` also stops the session on the server. */
 detach(stop:boolean):Promise<void>;
 /** The device's event stream says the session changed (an administrator ended it): the owner
  * checks at once rather than at its next timeline report. */
 sessionUpdated?(sessionId:string):Promise<void>;
}
