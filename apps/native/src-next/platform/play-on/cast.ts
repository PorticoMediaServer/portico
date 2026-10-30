import {castOrigin} from '../../../../../packages/client-core/src/connection-policy';
import type {RouteConnection} from '../../../../../packages/client-core/src/route-connection';
import {NativeEventEmitter, NativeModules, Platform} from 'react-native';
import {CastController, sameCastRemote, parseCastEvent, type CastChoices, type CastSnapshot, type CastTransport} from '../../../../../packages/client-core/src/cast';
import type {CastDockIdentity} from '../../kit/dock';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {LocalSession} from '../../../../../packages/client-core/src';
import {sameFence, type DockPlayback, type QueuePlaybackFence, type RemoteDock} from '../../../../../packages/player-core/src';
import {castTransport,validCastAttachment} from './cast-transport';
import {castControllerStorage, castControllerStorageReady} from './cast-controller-storage';
import {castOffered} from './cast-offered';
import type {NativePlaybackAdapter, NativePlaybackRuntimeSnapshot, PlaybackAuthority} from '../playback/types';

type Device = Readonly<{state:string;deviceName:string;deviceId?:string;attachmentId?:string}>;
type Bridge = Readonly<{applicationId?: string; available(): Promise<boolean>; current():Promise<Omit<Device,'state'>|null>;choose(): void}>;
type Source = Readonly<{id: string; adapter: NativePlaybackAdapter; snapshot: NativePlaybackRuntimeSnapshot;
  local: DockPlayback; fence: QueuePlaybackFence; choices: CastChoices}>;
type Report = Readonly<{itemId: string; versionId?: string; positionSeconds: number; durationSeconds: number; paused: boolean}>;
export type NativeCastState = Readonly<{cast: CastSnapshot; source?: Source; resumed?:Readonly<{id:string}>;control?:CastDockIdentity;displayKind?:'music'|'audiobook'|'video';receiver?: Report; title?: string; posterUrl?: string; stopped?:boolean}>;
export type NativeCastOptions = Readonly<{
  api: Readonly<{baseUrl?: string; getRouteConnection?(): RouteConnection | undefined; createUserMutationKey?():Promise<string>; request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;
  session: () => LocalSession | undefined; playback: () => NativePlaybackAdapter | undefined;
  current: () => boolean; deviceName: () => string; reportError: (error: unknown) => void;
}>;
const idle: CastSnapshot = Object.freeze({available: false, phase: 'off', positionSeconds: 0, durationSeconds: 0, paused: false, audioTracks: [], textTracks: []});
const authorityMatches = (a: PlaybackAuthority | undefined, b: PlaybackAuthority | undefined) => !!a && !!b &&
  (['serverId', 'accountId', 'profileId', 'epoch', 'viewerFence'] as const).every(key => a[key] === b[key]);
const sameSession = (a: LocalSession | undefined, b: LocalSession | undefined) => !!a && !!b && a.sessionFamilyId === b.sessionFamilyId &&
  (['serverId', 'accountId', 'profileId', 'authority', 'role'] as const).every(key => a.viewer[key] === b.viewer[key]);
const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});

/** One admitted viewer and one captured source attempt. The installed bridge only
 * transports the shared protocol; it never selects a later source to retire. */
export class NativeCastCaller {
  private controller?: CastController;
  private bridge?: Bridge;
  private emitter?: NativeEventEmitter;
  private readonly listeners = new Set<() => void>();
  private readonly abort = new AbortController();
  private readonly session: LocalSession | undefined;
  private readonly authority: PlaybackAuthority | undefined;
  private readonly origin: string;
  private readonly logical: string;
  private offBridge?: () => void;
  private bridgeGeneration=0;
  private attachmentId?:string;
  private eventOrder=0;
  private acceptedEventOrder=0;
  private readonly attachmentListeners=new Set<()=>void>();
  private offController?: () => void;
  private offPlayback?: () => void;
  private cancelChooser?: () => void;
  private source?: Source;
  private returning?:Readonly<{id:string;generation:number}>;
  private retired = false;
  private cancelling = false;
  private sequence = 0;
  private operation = 0;
  private dockStarted = false;
  private receiver?: Report;
  private idleObserved=false;
  private metadataItem?: string;
  private readonly descriptions=new Map<string,Readonly<{title:string;posterUrl?:string;kind?:'music'|'audiobook'|'video'}>>();
  private state: NativeCastState = Object.freeze({cast: idle});

  constructor(private readonly options: NativeCastOptions) {
    const session=options.session();this.session=session?Object.freeze({...session,viewer:Object.freeze({...session.viewer}),...(session.serverIdentity?{serverIdentity:Object.freeze({...session.serverIdentity})}:{})}):undefined; this.authority = options.playback()?.getSnapshot().authority;
    let logical = '';
    try {const url = new URL(options.api.baseUrl ?? ''); if (!url.username && !url.password) logical = url.origin;} catch { /* unavailable */ }
    this.logical = logical;
    // The receiver is an HTTPS page on the sender's network: the sender's own route when it is
    // secure, otherwise the best secure route the server lists, home first (P2-69).
    const routes = options.api.getRouteConnection?.(), memory = routes?.getMemory();
    const origin = logical ? castOrigin(routes?.getSnapshot().origin, [...(memory?.current?.document.candidates ?? []), {baseUrl: logical, class: 'manual'}], memory?.policy) ?? '' : '';
    this.origin = origin;
    if (Platform.isTV || !origin || !this.session || !this.authority || !NativeModules.PorticoCast) return;
    this.bridge = NativeModules.PorticoCast as Bridge;
    this.emitter = new NativeEventEmitter(NativeModules.PorticoCast);
    const bridgeEvents=this.emitter.addListener('PorticoCastSession',(event:Device)=>{void this.observeDevice(event);});
    this.offBridge=()=>bridgeEvents.remove();
    const original=this.session;
    const controller = new CastController({origin, displayName: options.deviceName(), controllerStorage: castControllerStorage,isCurrent:()=>this.viewerCurrent(),
      ...(original.serverIdentity&&original.deviceId?{controllerContext:{origin,publicKey:original.serverIdentity.publicKey,serverId:original.viewer.serverId,
        authority:original.viewer.authority,accountId:original.viewer.accountId,profileId:original.viewer.profileId,senderDeviceId:original.deviceId,senderFamilyId:original.sessionFamilyId}}:{}),
      describe:async itemId=>{
        const item=await this.request<{id?:unknown;title?:unknown;posterUrl?:unknown;kind?:unknown}>(`/v1/items/${encodeURIComponent(itemId)}`,'GET');
        if(item.id!==itemId||typeof item.title!=='string'||!item.title)return;
        const title=item.title.slice(0,300),kind=item.kind==='song'?'music':item.kind==='audiobook_file'?'audiobook':['movie','episode','extra'].includes(String(item.kind))?'video':undefined;
        this.descriptions.set(itemId,Object.freeze({title,...(kind?{kind}:{}),...(typeof item.posterUrl==='string'&&item.posterUrl.startsWith('/v1/')?{posterUrl:item.posterUrl}:{})}));
        while(this.descriptions.size>4)this.descriptions.delete(this.descriptions.keys().next().value!);
        return title;
      },
      api: {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => this.request<T>(path, method, body, signal),
        ...(options.api.createUserMutationKey ? {createUserMutationKey: async () => {
          if (!this.viewerCurrent()) throw cancelled();
          const key = await options.api.createUserMutationKey!();
          if (!this.viewerCurrent()) throw cancelled();
          return key;
        }} : {})}});
    this.controller = controller;
    this.offController = controller.subscribe(this.changed);
    this.offPlayback = options.playback()?.subscribe(() => {
      if (!this.viewerCurrent()) {this.dispose(); return;}
      // A pending intent resolves through the shared controller's original
      // receipt. Never issue Stop just because its queue header moved first.
      if (this.source && !this.retired && !this.sourceOccurrenceCurrent(this.source) && !this.cancelling) {
        this.cancelling = true; this.cancelChooser?.(); controller.stop();
      }
    });
    const order=++this.eventOrder;
    void castControllerStorageReady.then(()=>this.bridge?.current()).then(device=>{
      if(!device||!this.viewerCurrent())return;
      const generation=this.acceptAttachment(device,order);
      if(generation!==undefined&&!this.cancelChooser)void this.resume(device,generation);
    },()=>{/* Missing SDK session/proof leaves explicit pairing available. */});
    void Promise.all([this.bridge.available(), this.request<{applicationId?: unknown}>('/v1/cast/configuration')]).then(
      ([available, config]) => {if (this.viewerCurrent()) controller.setAvailable(castOffered(available, config.applicationId, this.bridge?.applicationId));},
      () => {if (this.viewerCurrent()) controller.setAvailable(false);});
  }
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = (): NativeCastState => this.state;
  private emit(patch: Partial<NativeCastState>): void {
    if (!this.viewerCurrent()) return;
    this.state = Object.freeze({...this.state, ...patch}); for (const listener of [...this.listeners]) listener();
  }
  private viewerCurrent(): boolean {
    if (this.abort.signal.aborted || !this.options.current() || !sameSession(this.options.session(), this.session) || this.options.session()?.deviceId!==this.session?.deviceId || this.options.session()?.serverIdentity?.publicKey!==this.session?.serverIdentity?.publicKey ||
      !authorityMatches(this.options.playback()?.getSnapshot().authority, this.authority)) return false;
    try {return new URL(this.options.api.baseUrl ?? '').origin === this.logical;} catch {return false;}
  }
  private async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    if (!this.viewerCurrent() || signal?.aborted) throw cancelled();
    const controller = new AbortController(), cancel = () => controller.abort();
    this.abort.signal.addEventListener('abort', cancel, {once: true}); signal?.addEventListener('abort', cancel, {once: true});
    try {const result = await this.options.api.request<T>(path, method, body, controller.signal);
      if (!this.viewerCurrent() || controller.signal.aborted) throw cancelled(); return result;
    } finally {this.abort.signal.removeEventListener('abort', cancel); signal?.removeEventListener('abort', cancel);}
  }
  private sourceOccurrenceCurrent(source: Source): boolean {
    // Root42 changes queue custody/header bindings at acceptance, before this
    // caller necessarily receives its ACK/status receipt. That movement is not
    // replacement of the still-playing original output. Only its exact runtime
    // fence (or original viewer/adapter retirement) can cancel this attempt.
    return this.viewerCurrent() && this.options.playback() === source.adapter &&
      sameFence(source.adapter.getSnapshot().fence, source.fence);
  }
  private precommitSourceCurrent(source: Source): boolean {
    const now = source.adapter.getSnapshot(), queue = now.queue.view?.queue;
    return this.sourceOccurrenceCurrent(source) && now.session?.id === source.fence.sessionId &&
      now.session.presentation.generation === source.fence.generation && !!queue && queue.id === source.choices.queue?.queueId &&
      queue.currentEntryId === source.fence.entryId && queue.currentPlaybackId === source.fence.sessionId && queue.revision === source.choices.source?.queueRevision;
  }
  private acceptAttachment(device:Omit<Device,'state'>,order:number):number|undefined{
    if(!this.viewerCurrent()||!validCastAttachment(device.attachmentId)||order<this.acceptedEventOrder)return;
    this.acceptedEventOrder=order;
    if(this.attachmentId!==device.attachmentId){
      this.attachmentId=device.attachmentId;this.bridgeGeneration++;
      for(const listener of [...this.attachmentListeners])listener();
      if(this.returning&&this.returning.generation!==this.bridgeGeneration){this.returning=undefined;this.receiver=undefined;this.emit({resumed:undefined,control:undefined,receiver:undefined,title:undefined,posterUrl:undefined,displayKind:undefined,stopped:false});}
    }
    return this.bridgeGeneration;
  }
  private async observeDevice(event:Device):Promise<void>{
    if(!this.viewerCurrent()||!validCastAttachment(event.attachmentId))return;
    if(event.state!=='started'){
      if(event.attachmentId!==this.attachmentId)return;
      this.attachmentId=undefined;this.bridgeGeneration++;
      for(const listener of [...this.attachmentListeners])listener();
      return;
    }
    const order=++this.eventOrder;
    try{
      const actual=await this.bridge?.current();
      if(!actual||actual.attachmentId!==event.attachmentId)return;
      const generation=this.acceptAttachment(actual,order);
      if(generation!==undefined&&!this.cancelChooser)void this.resume(actual,generation);
    }catch{/* A replaced attachment is never adopted from a queued event. */}
  }
  private async resume(device:Omit<Device,'state'>,generation:number):Promise<void>{
    const controller=this.controller;
    if(!controller||!device.deviceId||!validCastAttachment(device.attachmentId)||device.attachmentId!==this.attachmentId||!this.viewerCurrent()||this.cancelChooser||this.returning||this.state.cast.transferPending||
      controller.getSnapshot().phase!=='off'||this.source&&!this.retired)return;
    const returning=Object.freeze({id:`cast_return_${++this.operation}`,generation});this.returning=returning;
    try{
      await castControllerStorageReady;
      if(!this.viewerCurrent()||this.returning!==returning||this.bridgeGeneration!==generation){if(this.returning===returning)this.returning=undefined;return;}
      const accepted=await controller.adopt(this.transport(device,generation,()=>this.returning===returning));
      if(!accepted&&this.returning===returning)this.returning=undefined;
    }catch(error){if(this.returning===returning)this.returning=undefined;if(this.viewerCurrent())this.options.reportError(error);}
  }
  start = (): void => {
    const controller = this.controller, adapter = this.options.playback();
    if (!controller || this.returning || !this.bridge || !this.emitter || !adapter || !this.viewerCurrent() || !this.state.cast.available ||
      this.cancelChooser || this.state.cast.transferPending || ['connecting', 'pairing', 'waiting', 'casting'].includes(this.state.cast.phase)) return;
    const snapshot = adapter.getSnapshot();
    if(snapshot.playback.phase!=='ready')return;
    const fence = snapshot.fence, local = snapshot.dock.local, queue = snapshot.queue.view?.queue, session = snapshot.session;
    if (!snapshot.entry || !snapshot.subject || !fence || fence.kind !== 'queue' || !local || !sameFence(local.fence, fence) || !session?.queue || !queue ||
      session.id !== fence.sessionId || session.presentation.generation !== fence.generation || session.itemId !== fence.itemId ||
      queue.id !== session.queue.queueId || session.queue.entryId!==fence.entryId || queue.currentEntryId !== fence.entryId || queue.currentPlaybackId !== session.id) return;
    const source: Source = Object.freeze({id: `cast_${Date.now().toString(36)}_${++this.operation}`, adapter, snapshot, local: Object.freeze({...local}), fence,
      choices: Object.freeze({queue: Object.freeze({...session.queue}), source: Object.freeze({sessionId: session.id, generation: fence.generation,
        queueId: queue.id, entryId: fence.entryId, queueRevision: queue.revision}),
        // V02: the receiver starts the same edition this device is playing.
        ...(session.versionId ? {versionId: session.versionId} : {}),
        // V01: and the audio track chosen here.
        ...(session.presentation.audioTrackId ? {audioTrackId: session.presentation.audioTrackId} : {}),
        ...(snapshot.subtitles?.plan ? {subtitleTrackId: snapshot.subtitles.plan.mode === 'off' ? null : snapshot.subtitles.plan.selected?.id} : {})})});
    this.source = source; this.retired = false; this.cancelling = false; this.dockStarted = false; this.receiver = undefined; this.idleObserved=false;this.metadataItem = fence.itemId;
    const entry = snapshot.entry;
    const meta = Object.freeze({title: entry?.displayTitle ?? entry?.title ?? '', kind: entry?.kind,
      ...(entry?.artist?.name ? {subtitle: entry.artist.name} : {}), ...(entry?.posterUrl ? {artworkPath: entry.posterUrl} : {})});
    this.emit({source, title: meta.title || undefined, posterUrl: entry?.posterUrl, receiver: undefined});
    controller.connecting(meta.title);
    let done = false;
    const finish = () => {if (done) return; done = true; clearTimeout(timer); sub.remove(); if (this.cancelChooser === cancel) this.cancelChooser = undefined;};
    const cancel = () => {finish(); controller.cancelConnecting();};
    const timer = setTimeout(cancel, 90000);
    const failed = (error: unknown) => {if(this.viewerCurrent()&&this.source===source){finish();this.options.reportError(error);controller.cancelConnecting();}};
    const admitDevice = async (actual: Omit<Device,'state'>, order: number) => {
      if(done||this.source!==source)return;
      const generation=this.acceptAttachment(actual,order);if(generation===undefined)return;
      finish();
      if(!this.precommitSourceCurrent(source)){controller.cancelConnecting();return;}
      const device=Object.freeze({...actual,deviceName:String(actual.deviceName||'').slice(0,100)});
      await castControllerStorageReady;
      if(this.source!==source||this.bridgeGeneration!==generation||this.attachmentId!==device.attachmentId||!this.precommitSourceCurrent(source)||this.cancelling){controller.cancelConnecting();return;}
      const position = source.adapter.getSnapshot().playback.positionSeconds;
      return controller.start(this.transport(device,generation,()=>this.source===source),fence.itemId,meta,position,source.choices);
    };
    const sub = this.emitter.addListener('PorticoCastSession',(event:Device)=>{
      if(done||this.source!==source||event.state!=='started'||!validCastAttachment(event.attachmentId))return;
      const order=++this.eventOrder;
      void this.bridge!.current().then(actual=>{
        if(done||this.source!==source||!actual||actual.attachmentId!==event.attachmentId)return;
        return admitDevice(actual,order);
      }).catch(failed);
    });
    this.cancelChooser = cancel;
    // Reusing a connected SDK session does not emit another "started" event.
    // Admit that exact current attachment through the same source checks.
    try {
      const order=++this.eventOrder;
      void this.bridge.current().then(actual=>{
        if(done||this.source!==source)return;
        if(actual&&validCastAttachment(actual.attachmentId))return admitDevice(actual,order);
        this.bridge!.choose();
      }).catch(failed);
    } catch (error) {cancel(); this.options.reportError(error);}
  };
  private transport(device: Omit<Device, 'state'>,generation:number,attempt:()=>boolean): CastTransport {
    const attachmentId=device.attachmentId;
    const current=()=>validCastAttachment(attachmentId)&&this.attachmentId===attachmentId&&this.viewerCurrent()&&attempt()&&this.bridgeGeneration===generation;
    return {deviceName: device.deviceName, ...(device.deviceId ? {deviceId: device.deviceId} : {}),
      send:message=>current()?castTransport.send(message,attachmentId):Promise.reject(cancelled()),
      onMessage:listener=>castTransport.onMessage(raw=>{
        if(!current())return;
        const previous=this.controller?.getSnapshot(),event=parseCastEvent(raw);
        listener(raw);
        const accepted=this.controller?.getSnapshot();
        // Only the shared controller's admitted idle transition is a stopped
        // fact. No raw SDK item, clock or track is published by this adapter.
        if(event?.type==='status'&&!event.itemId&&previous?.phase==='casting'&&accepted?.phase==='off'&&!accepted.disclosureRefused){this.idleObserved=true;this.changed();}
      },attachmentId),
      onSessionEnd:listener=>{const changed=()=>{if(attempt()&&(this.attachmentId!==attachmentId||this.bridgeGeneration!==generation))listener();};this.attachmentListeners.add(changed);return()=>{this.attachmentListeners.delete(changed);};},
      end:stopReceiver=>{if(current())castTransport.end(stopReceiver,attachmentId);}};
  }
  private changed = (): void => {
    if (!this.viewerCurrent() || !this.controller) return;
    const cast = this.controller.getSnapshot();
    if(cast.disclosureRefused){this.receiver=undefined;this.returning=undefined;this.emit({cast,resumed:undefined,control:undefined,receiver:undefined,title:undefined,posterUrl:undefined,displayKind:undefined,stopped:false});return;}
    if(cast.reported)this.receiver=cast.reported;
    else if(cast.remoteStopped||!sameCastRemote(cast.remoteIdentity,this.state.cast.remoteIdentity))this.receiver=undefined;
    const control=cast.remoteIdentity&&validCastAttachment(this.attachmentId)?Object.freeze({kind:'cast-view' as const,attachmentId:this.attachmentId,remote:cast.remoteIdentity}):undefined;
    const returning=this.returning;
    if(returning&&['off','error','replaced'].includes(cast.phase)&&!cast.remoteStopped&&this.state.resumed){
      this.returning=undefined;this.receiver=undefined;this.emit({cast,resumed:undefined,control:undefined,receiver:undefined,title:undefined,posterUrl:undefined,displayKind:undefined,stopped:false});return;
    }
    if(returning&&(cast.phase==='casting'||cast.remoteStopped)&&control){
      const display=cast.itemId?this.descriptions.get(cast.itemId):undefined;
      this.emit({cast,control,resumed:{id:returning.id},receiver:this.receiver,title:cast.title,posterUrl:display?.title===cast.title?display?.posterUrl:undefined,displayKind:display?.title===cast.title?display?.kind:undefined,stopped:cast.remoteStopped===true});
      return;
    }
    const source = this.source, accepted = cast.acceptedSource, expected = source?.choices.source;
    if(cast.phase==='casting'&&this.state.cast.phase!=='casting')this.idleObserved=false;
    const stopped=cast.phase==='off'&&!cast.transferPending&&(cast.remoteStopped||this.cancelling||this.idleObserved);
    const committed = !!source && !!expected && !!accepted && (['sessionId', 'generation', 'queueId', 'entryId', 'queueRevision'] as const).every(key => expected[key] === accepted[key]);
    if (source && committed && !this.retired) {
      this.retired = true;
      // Retire even an originally paused source. The existing transition port
      // checks this captured effect's exact fence before touching the engine.
      // Keep dock state intact: Close would restore a parked bookmark here.
      const now=source.adapter.getSnapshot();
      if(sameFence(now.fence,source.fence)&&!!now.dock.local&&sameFence(now.dock.local.fence,source.fence))
        void source.adapter.onTransition(Object.freeze({state:source.adapter.dock.getSnapshot(),effects:Object.freeze([
          Object.freeze({kind:'stop' as const,playback:Object.freeze({...source.local,playing:false,expanded:false}),playing:false as const})
        ])})).catch(this.options.reportError);
    }
    this.emit({cast,control, receiver: this.receiver,stopped});
    if (!source) return;
    if(cast.phase==='off'&&!this.retired&&!cast.transferPending){
      void source.adapter.dismissRemote?.(source.id).catch(this.options.reportError);
      this.source=undefined;this.emit({source:undefined,receiver:undefined,title:undefined,posterUrl:undefined});return;
    }
    const item = this.receiver?.itemId || cast.itemId || source.fence.itemId;
    if (item !== this.metadataItem) {
      this.metadataItem = item; this.emit({title: undefined, posterUrl: undefined});
      void this.request<{id?: string; title?: string; posterUrl?: string}>(`/v1/items/${encodeURIComponent(item)}`, 'GET').then(value => {
        if (this.source === source && this.metadataItem === item && value.id === item && typeof value.title === 'string')
          this.emit({title: value.title.slice(0, 300), posterUrl: typeof value.posterUrl === 'string' && value.posterUrl.startsWith('/v1/') ? value.posterUrl : undefined});
      }, error => {if (this.viewerCurrent()) this.options.reportError(error);});
    }
    const phase: RemoteDock['phase'] = cast.phase === 'casting' ? 'playing' : cast.phase === 'off' ? stopped?'stopped':'error' : cast.phase;
    if(this.options.playback()!==source.adapter)return;
    const current = source.adapter.getSnapshot(), local = current.dock.local;
    // A newer local title is never retired by a late receipt or status update.
    if (local && !sameFence(local.fence, source.fence)) return;
    if (phase === 'playing' && !this.retired) return;
    const remote: RemoteDock = Object.freeze({id: source.id, kind: 'cast', phase, sequence: String(++this.sequence),
      positionSeconds: this.receiver?.positionSeconds ?? 0, playback: Object.freeze({...source.local, expanded: false,
        playing: phase === 'playing' && this.receiver?.paused === false})});
    const first = !this.dockStarted || committed && phase === 'playing' && current.dock.remote?.phase !== 'playing';
    if (first) this.dockStarted = true;
    void (first ? source.adapter.beginRemote?.(remote) : source.adapter.updateRemote?.(remote))?.catch(this.options.reportError);
  };
  viewIsCurrent=(identity:CastDockIdentity):boolean=>this.viewerCurrent()&&identity.attachmentId===this.attachmentId&&
    sameCastRemote(identity.remote,this.controller?.getSnapshot().remoteIdentity)&&
    (this.controller?.getSnapshot().phase==='casting'||this.controller?.getSnapshot().remoteStopped===true)&&
    (!this.returning||this.returning.generation===this.bridgeGeneration);
  /** Track choices use only the current receiver's offered track ids. The selected
   * track is not inferred: the protocol does not yet report a selected id. */
  selectTrack = (kind: 'audio' | 'subtitles', trackId: number | null, expected: CastDockIdentity): void => {
    const cast = this.state.cast;
    if (!this.controller || !this.viewIsCurrent(expected) || cast.phase !== 'casting' || cast.controlPending
      || !this.receiver || this.receiver.itemId !== cast.itemId) return;
    const tracks = kind === 'audio' ? cast.audioTracks : cast.textTracks;
    if (trackId === null ? kind !== 'subtitles' || !tracks.length : !Number.isSafeInteger(trackId) || !tracks.some(track => track.id === trackId)) return;
    if (kind === 'audio') this.controller.setAudioTrack(trackId!, expected.remote);
    else this.controller.setSubtitleTrack(trackId, expected.remote);
  };
  controls = (type:string,position?:number,expected?:CastDockIdentity,takeover?:CastSnapshot['takeover']):void => {
    if (!this.viewerCurrent() || !this.controller) return;
    const cast = this.state.cast;
    if(expected&&!this.viewIsCurrent(expected))return;
    if(!expected&&(this.returning||cast.remoteIdentity)&&!['allow-takeover','deny-takeover'].includes(type))return;
    const target=expected?.remote;
    switch (type) {
      case 'allow-takeover': this.controller.answerTakeover(true,takeover);return;
      case 'deny-takeover': this.controller.answerTakeover(false,takeover);return;
      case 'stop': this.cancelling = true; this.cancelChooser?.(); this.controller.stop(target); return;
      case 'dismiss-remote': if(this.returning){this.controller.dismiss();this.returning=undefined;this.emit({resumed:undefined,control:undefined,receiver:undefined,title:undefined,posterUrl:undefined,displayKind:undefined,stopped:false});return;}if (cast.transferPending) {this.cancelling = true; this.controller.stop(target); return;}
        this.controller.dismiss(); return;
      case 'pause': if (cast.phase === 'casting') this.controller.pause(target); return;
      case 'play': case 'resume': if (cast.phase === 'casting') this.controller.play(target); return;
      case 'seek': if (cast.phase === 'casting' && this.receiver?.itemId === cast.itemId && position !== undefined && Number.isFinite(position)) this.controller.seek(position,target); return;
    }
  };
  dismiss = async (): Promise<void> => {
    const source=this.source;
    if(!this.viewerCurrent())return;
    if(this.returning){this.controller?.dismiss();this.returning=undefined;this.emit({resumed:undefined,control:undefined,receiver:undefined,title:undefined,posterUrl:undefined,displayKind:undefined,stopped:false});return;}
    if(!source)return;
    if(this.state.cast.transferPending){this.controls('stop');return;}
    this.controller?.dismiss();
    await source.adapter.dismissRemote?.(source.id);
    if(this.source===source){this.source=undefined;this.emit({source:undefined,receiver:undefined,title:undefined,posterUrl:undefined});}
  };
  /** Explicit Continue here starts the one reported leaf through normal admission;
   * it cannot restore or copy the queue whose custody belongs to the receiver. */
  continueHere = async (expected?:CastDockIdentity): Promise<void> => {
    const returning=this.returning,report=this.receiver,adapter=this.options.playback();
    if(returning){
      if(!expected||!adapter||!report||!this.state.cast.remoteStopped||!this.viewIsCurrent(expected)||report.itemId!==expected.remote.occurrence.itemId)return;
      const originalLocal=adapter.getSnapshot();
      const current=()=>this.returning===returning&&this.options.playback()===adapter&&this.receiver===report&&this.state.cast.remoteStopped===true&&this.viewIsCurrent(expected);
      // This is an ordinary explicit new play, not a restored local fence. A
      // newer local run while the item read waits wins before start is issued.
      const localUnchanged=()=>{const now=adapter.getSnapshot();return now.playback.playbackRun===originalLocal.playback.playbackRun&&
        (originalLocal.fence?!!now.fence&&sameFence(now.fence,originalLocal.fence):!now.fence);};
      const actual=await this.bridge?.current();
      if(!actual||actual.attachmentId!==expected.attachmentId||!current())return;
      const item=await this.request<{id?:string;kind?:string;title?:string;libraryId?:string;posterUrl?:string}>(`/v1/items/${encodeURIComponent(report.itemId)}`,'GET');
      if(!current()||!localUnchanged()||item.id!==report.itemId||typeof item.title!=='string'||!['song','audiobook_file','movie','episode','extra'].includes(item.kind??''))return;
      const entry:ContentEntry=Object.freeze({id:item.id,kind:item.kind as ContentEntry['kind'],title:item.title,
        ...(typeof item.libraryId==='string'?{libraryId:item.libraryId}:{}),...(typeof item.posterUrl==='string'&&item.posterUrl.startsWith('/v1/')?{posterUrl:item.posterUrl}:{}),
        playback:Object.freeze({itemId:item.id,startSeconds:report.positionSeconds})});
      const latestAttachment=await this.bridge?.current();
      if(latestAttachment?.attachmentId!==expected.attachmentId||!current()||!localUnchanged())return;
      await adapter.start({entry,target:entry.playback!,startIntent:'explicit',...(report.versionId?{versionId:report.versionId}:{}),lifetime:{signal:this.abort.signal,isCurrent:current}});
      if(current())await this.dismiss();return;
    }

    const source=this.source;
    if(expected&&!this.viewIsCurrent(expected))return;
    if(!source||!report?.itemId||!this.state.stopped||this.state.cast.phase!=='off'||this.state.cast.transferPending||!this.viewerCurrent())return;
    const current=()=>this.source===source&&this.viewerCurrent()&&(!expected||this.viewIsCurrent(expected))&&this.state.cast.phase==='off'&&this.receiver===report&&
      source.adapter.getSnapshot().dock.remote?.id===source.id&&source.adapter.getSnapshot().dock.remote?.phase==='stopped'&&(!source.adapter.getSnapshot().dock.local||sameFence(source.adapter.getSnapshot().dock.local!.fence,source.fence));
    if(!current())return;
    const item=await this.request<{id?:string;kind?:string;title?:string;libraryId?:string;posterUrl?:string}>(`/v1/items/${encodeURIComponent(report.itemId)}`,'GET');
    if(!current()||item.id!==report.itemId||typeof item.title!=='string'||!['song','audiobook_file','movie','episode','extra'].includes(item.kind??''))return;
    const entry:ContentEntry=Object.freeze({id:item.id,kind:item.kind as ContentEntry['kind'],title:item.title,
      ...(typeof item.libraryId==='string'?{libraryId:item.libraryId}:{}),...(typeof item.posterUrl==='string'&&item.posterUrl.startsWith('/v1/')?{posterUrl:item.posterUrl}:{}),
      playback:Object.freeze({itemId:item.id,startSeconds:report.positionSeconds})});
    await source.adapter.start({entry,target:entry.playback!,startIntent:'explicit',...(report.versionId?{versionId:report.versionId}:{}),lifetime:{signal:this.abort.signal,isCurrent:()=>this.source===source&&this.viewerCurrent()&&this.state.cast.phase==='off'&&this.receiver===report}});
    await this.dismiss();
  };
  dispose(): void {
    if (this.abort.signal.aborted) return;
    this.cancelChooser?.(); this.offPlayback?.(); this.offController?.();this.offBridge?.(); this.controller?.dispose(); this.abort.abort();this.descriptions.clear();this.attachmentListeners.clear(); this.listeners.clear();
  }
}
