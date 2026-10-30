import type {CastChoices, CastMetadata, WebCastSender} from '../cast';
import {sameFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackPresentation, WebPlaybackRuntimeSnapshot} from './types';
import type {WebShellPlaybackRuntime} from './runtime';

/** Structural contract agreed with platform/cast. No SDK, script, receiver or URL logic here. */
export type WebCastPort = Pick<WebCastSender, 'getSnapshot' | 'subscribe' | 'prepareOnIdle' | 'prepareForPlayer' | 'start' | 'cancelConnecting' | 'play' | 'pause' | 'toggle' | 'seek' | 'setAudioTrack' | 'setSubtitleTrack' | 'answerTakeover' | 'stop' | 'dismiss' | 'dispose'> & Partial<Pick<WebCastSender, 'retainsMedia' | 'onRetention'>>;
export type AirPlayPort = Readonly<{
  available(): boolean;
  subscribe(listener: () => void): () => void;
  showPicker(): void;
}>;
export type CastRemotePhase = 'connecting' | 'pairing' | 'waiting' | 'playing' | 'stopped' | 'replaced' | 'error';
export type CastRemoteToken = Readonly<{id: string; sequence: number}>;
export type CastRemoteView = CastRemoteToken & Readonly<{
  phase: CastRemotePhase; confirmed: boolean; itemId?: string; title?: string; deviceName?: string;
  positionKnown: boolean; positionSeconds: number; durationSeconds: number; paused: boolean;
  /** The version the receiver reported playing, correlated to this occurrence. */
  versionId?: string;
  source?: WebPlaybackPresentation; metadata?: CastMetadata;
}>;
/** A Portico television the viewer may send to, named by the server's receiver directory. */
export type PlayOnReceiver = Readonly<{id: string; name: string; detail?: string}>;
/** The receiver directory and one proven handoff. `send` resolves true only after the
 * television has proved it is playing; the caller then stops only the fenced run. */
export type PlayOnReceiversPort = Readonly<{
  getSnapshot(): Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; items: readonly PlayOnReceiver[]}>;
  subscribe(listener: () => void): () => void;
  refresh(): void;
  send(id: string): Promise<boolean>;
}>;
export type PlayOnLinkPreview = Readonly<{requestId: string; userCode: string; deviceName: string; platform?: string}>;
/** Signing a television in with the code it shows: review the device, then allow or deny. */
export type PlayOnLinkPort = Readonly<{
  preview(code: string, life?: Readonly<{signal: AbortSignal}>): Promise<PlayOnLinkPreview>;
  decide(requestId: string, code: string, decision: 'approve' | 'deny', life?: Readonly<{signal: AbortSignal}>): Promise<void>;
}>;
export type PlayOnSnapshot = Readonly<{
  cast: ReturnType<WebCastPort['getSnapshot']>; airPlayAvailable: boolean;
  panel: 'play-on' | 'remote' | 'tracks' | null; remote?: CastRemoteView; disposed: boolean;
}>;
export type PlayOnControllerOptions = Readonly<{
  sender: WebCastPort; airPlay?: AirPlayPort;
  /** The server's Portico television directory; a panel may open for these alone. */
  receivers?: PlayOnReceiversPort;
  runtime: Pick<WebShellPlaybackRuntime, 'getSnapshot' | 'subscribe' | 'stop' | 'play' | 'bindRemoteRetention'>;
  /** The assembly's real viewer/authority lifetime, never a route lifetime. */
  isCurrent(): boolean;
  reportError(error: unknown): void;
  /** Current viewer's actual catalogue metadata; never a fabricated playback session. */
  describeItem?: (itemId: string) => Promise<CastMetadata>;
  now?: () => number; key?: () => string;
  setTimer?: (callback: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
}>;

/** A picker may stay open through seeks, queue edits or source replacement. Only the
 * original occurrence at its original queue revision may supply a fresh start clock. */
export function castSourcePosition(original: WebPlaybackRuntimeSnapshot, latest: WebPlaybackRuntimeSnapshot): number | undefined {
  const fence = original.fence, session = original.session, queue = original.queueView?.queue;
  const now = latest.session, currentQueue = latest.queueView?.queue, presentation = latest.presentation;
  if (fence?.kind !== 'queue' || !session?.queue || !queue || latest.disposed || latest.authority !== original.authority
    || latest.playback.playbackRun !== original.playback.playbackRun || !sameFence(latest.fence, fence) || !sameFence(presentation?.fence, fence)
    || now?.id !== session.id || now.itemId !== fence.itemId || now.presentation.generation !== session.presentation.generation
    || now.presentation.generation !== fence.generation || now.queue?.queueId !== session.queue.queueId || now.queue?.entryId !== fence.entryId
    || currentQueue?.id !== queue.id || currentQueue?.id !== session.queue.queueId || currentQueue?.revision !== queue.revision
    || currentQueue?.currentEntryId !== fence.entryId || currentQueue?.currentPlaybackId !== session.id
    || !presentation || !Number.isFinite(presentation.positionSeconds) || presentation.positionSeconds < 0) return;
  return presentation.positionSeconds;
}

/** UI composition over the canonical sender. Its phase is the only receiver confirmation. */
export class PlayOnController {
  private readonly listeners = new Set<() => void>();
  private readonly detach: (() => void)[] = [];
  private state: PlayOnSnapshot;
  private serial = 0;
  private sequence = 0;
  private attempt?: {id: string; serial: number; source: WebPlaybackPresentation; choices: CastChoices; handedOff: boolean};
  private expiry?: unknown;
  private terminalAt?: number;
  private playerOpen = false;
  private disposed = false;
  private dismissing = false;
  private describedItem?: string;
  private descriptionGeneration=0;
  constructor(private readonly options: PlayOnControllerOptions) {
    this.state = Object.freeze({cast: options.sender.getSnapshot(), airPlayAvailable: options.airPlay?.available() === true, panel: null, disposed: false});
    this.detach.push(options.sender.subscribe(this.senderChanged), options.runtime.subscribe(this.runtimeChanged), options.sender.prepareOnIdle());
    if (options.airPlay) this.detach.push(options.airPlay.subscribe(() => this.publish({airPlayAvailable: options.airPlay!.available()})));
    this.detach.push(options.runtime.bindRemoteRetention({
      retainsMedia: () => !this.disposed && (!!this.state.remote || this.state.cast.transferPending === true || options.sender.retainsMedia?.() === true),
      subscribe: listener => {
        const off = this.subscribe(listener), offSender = options.sender.onRetention?.(listener);
        return () => {off();offSender?.();};
      },
    }));
    this.runtimeChanged(); this.senderChanged();
  }
  getSnapshot = (): PlayOnSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private current(): boolean { return !this.disposed && this.options.isCurrent() && !this.options.runtime.getSnapshot().disposed; }
  private assertCurrent(): void { if (!this.current()) throw new Error('playback_authority_retired'); }
  private publish(patch: Partial<PlayOnSnapshot>): void {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private clearExpiry(): void { if (this.expiry !== undefined) (this.options.clearTimer ?? (timer => clearTimeout(timer as ReturnType<typeof setTimeout>)))(this.expiry); this.expiry = undefined; this.terminalAt = undefined; }
  private terminal(): void {
    if (this.terminalAt !== undefined || this.state.cast.transferPending) return;
    this.terminalAt = (this.options.now ?? Date.now)();
    const id = this.state.remote?.id;
    this.expiry = (this.options.setTimer ?? setTimeout)(() => {
      const remote = this.state.remote;
      if (!this.current() || !remote || remote.id !== id) return;
      this.dismiss(remote);
    }, 15_000);
  }
  private runtimeChanged = (): void => {
    if (!this.current()) { this.dispose(); return; }
    const snapshot = this.options.runtime.getSnapshot();
    const playerOpen = snapshot.presentation?.expanded === true;
    if (playerOpen && !this.playerOpen) {
      void this.options.sender.prepareForPlayer().catch(this.options.reportError);
    }
    this.playerOpen = playerOpen;
    const attempt = this.attempt;
    if (attempt && !attempt.handedOff && !sameFence(snapshot.fence, attempt.source.fence)) {
      ++this.serial; this.attempt = undefined;
      this.options.sender.cancelConnecting();
      this.publish({remote: undefined, panel: null});
    }
  };
  private senderChanged = (): void => {
    if (!this.current() || this.dismissing) return;
    const cast = this.options.sender.getSnapshot(), previous = this.state.remote, attempt = this.attempt;
    const requestedSource=attempt?.choices.source,acceptedSource=cast.acceptedSource;
    const acceptedAttempt=!!requestedSource&&!!acceptedSource&&acceptedSource.sessionId===requestedSource.sessionId&&acceptedSource.generation===requestedSource.generation&&acceptedSource.queueId===requestedSource.queueId&&acceptedSource.entryId===requestedSource.entryId&&acceptedSource.queueRevision===requestedSource.queueRevision;
    // Receipt recovery can arrive after the transport closed. Process its exact
    // source before clearing an unconfirmed/off presentation.
    if(acceptedAttempt&&attempt&&!attempt.handedOff&&sameFence(this.options.runtime.getSnapshot().fence,attempt.source.fence)){
      attempt.handedOff=true;void this.options.runtime.stop(attempt.source.fence).catch(this.options.reportError);
    }
    if(cast.disclosureRefused){
      this.descriptionGeneration++;this.describedItem=undefined;this.clearExpiry();
      // Keep an unresolved Root42 attempt private until its original receipt
      // settles; never keep its title/clock as a redaction fallback.
      if(!cast.transferPending&&(!attempt||attempt.handedOff))this.attempt=undefined;
      this.publish({cast,remote:undefined,panel:null});return;
    }
    // SDK detach/off is not proof that the receiver stopped. Continuation may
    // use only the actual private Stop receipt, not the last playing clock.
    if(cast.phase==='off'&&!cast.remoteStopped&&!cast.transferPending){
      this.descriptionGeneration++;this.describedItem=undefined;this.clearExpiry();
      if(!attempt||attempt.handedOff)this.attempt=undefined;
      this.publish({cast,remote:undefined,panel:null});return;
    }
    const active = cast.phase !== 'off';
    if (!active && !previous && !acceptedAttempt && !cast.transferPending) { this.publish({cast}); return; }
    if (!active && previous && !previous.confirmed && !acceptedAttempt && !cast.transferPending) {
      this.attempt = undefined; this.clearExpiry(); this.publish({cast, remote: undefined, panel: null}); return;
    }
    const phase: CastRemotePhase = cast.phase === 'casting' ? 'playing' : cast.phase === 'off' ? 'stopped' : cast.phase;
    const confirmed = phase === 'playing' || acceptedAttempt || previous?.confirmed === true;
    const itemId = cast.receiver?.itemId ?? cast.itemId ?? previous?.itemId ?? (attempt?.source.fence.kind === 'queue' ? attempt.source.fence.itemId : undefined);
    const receiver = cast.receiver?.itemId === itemId ? cast.receiver : undefined;
    const sourceCandidate = attempt?.source ?? previous?.source;
    const source = sourceCandidate?.fence.kind === 'queue' && sourceCandidate.fence.itemId === itemId ? sourceCandidate : undefined;
    const itemChanged = !!previous?.itemId && !!itemId && previous.itemId !== itemId;
    // Off/replaced can clear the sender's title/device. Keep the last actual receiver position.
    const remote: CastRemoteView = Object.freeze({
      id: attempt?.id ?? previous?.id ?? (this.options.key ?? (() => crypto.randomUUID()))(), sequence: ++this.sequence,
      phase, confirmed, source, itemId, metadata: itemChanged ? undefined : previous?.metadata,
      title: itemChanged ? undefined : previous?.metadata?.title ?? (source || !previous?.itemId ? cast.title : previous?.title), deviceName: cast.device?.name ?? cast.deviceName ?? previous?.deviceName,
      positionKnown: !!receiver, ...(receiver?.versionId ? {versionId: receiver.versionId} : {}), positionSeconds: receiver?.positionSeconds ?? 0, durationSeconds: receiver?.durationSeconds ?? 0, paused: receiver?.paused ?? cast.paused,
    });
    this.publish({cast, remote, ...(phase === 'stopped' || phase === 'replaced' || phase === 'error' ? {panel: null} : {})});
    if (itemId && this.options.describeItem && this.describedItem !== itemId) {
      this.describedItem = itemId;
      const id = remote.id,generation=++this.descriptionGeneration,controlRevision=cast.controlRevision;
      const currentDescription=()=>this.current()&&generation===this.descriptionGeneration&&!this.state.cast.disclosureRefused&&this.state.cast.controlRevision===controlRevision&&this.state.remote?.id===id&&this.state.remote.itemId===itemId;
      void this.options.describeItem(itemId).then(metadata => {
        const latest = this.state.remote;
        if (!latest||!currentDescription()||this.state.cast.title&&this.state.cast.title!==metadata.title) return;
        this.publish({remote: Object.freeze({...latest, title: metadata.title, metadata, sequence: ++this.sequence})});
      }).catch(error=>{if(currentDescription())this.options.reportError(error);});
    }
    if (phase === 'stopped' || phase === 'replaced' || phase === 'error') this.terminal();
    else this.clearExpiry();
    // Only an explicit source attempt hands off local playback. SDK adoption does not stop an
    // unrelated title in this browser. Pairing, waiting and picker cancellation never stop it.
    if (phase === 'playing' && attempt && !attempt.handedOff) {
      const requested=attempt.choices.source,accepted=cast.acceptedSource;
      if(requested&&(!accepted||accepted.sessionId!==requested.sessionId||accepted.generation!==requested.generation||accepted.queueId!==requested.queueId||accepted.entryId!==requested.entryId||accepted.queueRevision!==requested.queueRevision))return;

      if (!sameFence(this.options.runtime.getSnapshot().fence, attempt.source.fence)) { this.options.sender.stop(); return; }
      attempt.handedOff = true;
      void this.options.runtime.stop(attempt.source.fence).catch(this.options.reportError);
    }
  };
  openPlayOn(): void {
    this.assertCurrent();
    if (!this.state.cast.available && !this.state.airPlayAvailable && (this.options.receivers?.getSnapshot().items.length ?? 0) === 0) return;
    this.publish({panel: 'play-on'});
  }
  closePanel(): void { this.assertCurrent(); this.publish({panel: null}); }
  /** Invoked directly by the row's click, preserving the SDK's user activation. */
  startCast(): Promise<boolean> {
    this.assertCurrent();
    const snapshot = this.options.runtime.getSnapshot(), source = snapshot.presentation;
    if (this.state.cast.transferPending || ['connecting', 'pairing', 'waiting'].includes(this.state.cast.phase)) return Promise.resolve(false);
    if (!this.state.cast.available || !source || source.fence.kind !== 'queue' || !snapshot.session?.queue || snapshot.session.itemId !== source.fence.itemId) return Promise.resolve(false);
    this.clearExpiry(); this.describedItem = undefined;
    const item = source.subject.kind === 'channel' ? undefined : source.subject.item;
    if (!item) return Promise.resolve(false);
    const serial = ++this.serial;
    const queue=snapshot.queueView?.queue;
    if(!queue||queue.id!==snapshot.session.queue.queueId||queue.currentEntryId!==source.fence.entryId||queue.currentPlaybackId!==snapshot.session.id)return Promise.resolve(false);
    // Version and audio come from this exact fenced session, never a later unrelated playback.
    const {versionId} = snapshot.session, audioTrackId = snapshot.session.presentation.audioTrackId;
    this.attempt = {id: (this.options.key ?? (() => crypto.randomUUID()))(), serial, source, choices: {
      ...(versionId ? {versionId} : {}), ...(audioTrackId ? {audioTrackId} : {}),
      queue:Object.freeze({...snapshot.session.queue}), source:Object.freeze({sessionId:snapshot.session.id,generation:snapshot.session.presentation.generation,
        queueId:queue.id,entryId:source.fence.entryId,queueRevision:queue.revision})}, handedOff: false};
    this.publish({panel: null, remote: undefined});
    // Display paths and ids only. The platform/controller whitelist never receives media URLs,
    // session grants or sign-in credentials. Only the authenticated bootstrap receives the source session id.
    const meta: CastMetadata = {title: item.show?.title || item.title, subtitle: item.show?.title ? item.title : item.artist ?? item.author,
      kind: source.kind, artworkPath: source.posterUrl ?? source.backdropUrl};
    const started = this.options.sender.start(source.fence.itemId, meta, source.positionSeconds, this.attempt.choices, () => {
      if (!this.current() || serial !== this.serial || this.attempt?.serial !== serial || this.options.sender.getSnapshot().phase !== 'connecting') return;
      return castSourcePosition(snapshot, this.options.runtime.getSnapshot());
    });
    return started.then(ok => this.current() && serial === this.serial && ok && this.options.sender.getSnapshot().phase === 'casting');
  }
  startAirPlay(): void { this.assertCurrent(); if (!this.options.airPlay?.available()) {if (this.state.airPlayAvailable) this.publish({airPlayAvailable: false});return;} this.publish({panel: null}); this.options.airPlay.showPicker(); }
  private requireRemote(token: CastRemoteToken): CastRemoteView | undefined {
    if (!this.current()) return;
    const remote = this.state.remote;
    return remote && token.id === remote.id && token.sequence === remote.sequence ? remote : undefined;
  }
  openRemote(token: CastRemoteToken): void { if (this.requireRemote(token)?.phase === 'playing') this.publish({panel: 'remote'}); }
  openTracks(token: CastRemoteToken): void {
    if (this.requireRemote(token)?.phase === 'playing' && (this.state.cast.audioTracks.length || this.state.cast.textTracks.length)) this.publish({panel: 'tracks'});
  }
  toggle(token: CastRemoteToken): void { if (this.requireRemote(token)?.phase === 'playing') this.options.sender.toggle(); }
  seek(token: CastRemoteToken, seconds: number): void {
    const remote = this.requireRemote(token);
    if (!remote || remote.phase !== 'playing' || !remote.positionKnown || !Number.isFinite(seconds) || seconds < 0) return;
    this.options.sender.seek(remote.durationSeconds > 0 ? Math.min(seconds, remote.durationSeconds) : seconds);
  }
  audio(token: CastRemoteToken, id: number): void { if (this.requireRemote(token)?.phase === 'playing' && this.state.cast.audioTracks.some(track => track.id === id)) this.options.sender.setAudioTrack(id); }
  subtitles(token: CastRemoteToken, id: number | null): void { if (this.requireRemote(token)?.phase === 'playing' && (id === null || this.state.cast.textTracks.some(track => track.id === id))) this.options.sender.setSubtitleTrack(id); }
  stop(token: CastRemoteToken): void {
    const remote = this.requireRemote(token); if (!remote) return; ++this.serial;
    if (!remote.confirmed) { this.options.sender.cancelConnecting(); this.clearExpiry(); this.publish({panel: null}); return; }
    this.options.sender.stop();
  }
  dismiss(token: CastRemoteToken): void {
    if (!this.requireRemote(token)) return;
    if(this.state.cast.transferPending){++this.serial;this.clearExpiry();this.options.sender.stop();this.publish({panel:null});return;}
    ++this.serial; this.attempt = undefined; this.describedItem = undefined; this.clearExpiry(); this.dismissing = true;
    try { this.options.sender.dismiss(); } finally { this.dismissing = false; }
    this.publish({cast: this.options.sender.getSnapshot(), remote: undefined, panel: null}); }
  async continueHere(token: CastRemoteToken): Promise<void> {
    const remote = this.requireRemote(token);
    if (!remote || !remote.confirmed || !remote.positionKnown || remote.phase !== 'stopped' || !remote.itemId) return;
    const position = remote.positionSeconds, itemId = remote.itemId, versionId = remote.versionId;
    this.dismiss(token);
    // Continue the edition the television was actually playing, not the server's default.
    await this.options.runtime.play(itemId, position, versionId ? {versionId} : undefined);
  }
  answerTakeover(allow:boolean,expected?:PlayOnSnapshot['cast']['takeover']):void {this.assertCurrent();if(expected&&expected===this.state.cast.takeover)this.options.sender.answerTakeover(allow,expected);}
  dispose(): void {
    if (this.disposed) return;
    this.clearExpiry(); ++this.serial; this.attempt = undefined; this.describedItem = undefined;
    for (const off of this.detach.splice(0)) off();
    this.options.sender.dispose();
    this.disposed = true;
    this.state = Object.freeze({...this.state, cast: {...this.state.cast, available: false}, airPlayAvailable: false, remote: undefined, panel: null, disposed: true});
    for (const listener of [...this.listeners]) listener();
    this.listeners.clear();
  }
}

/** The same HTML media node owns the browser's AirPlay picker; this does not claim a TV name. */
export function mediaAirPlayPort(video: HTMLVideoElement, userAgent = navigator.userAgent, source?: Readonly<{isCurrent(): boolean; subscribe(listener: () => void): () => void}>): AirPlayPort {
  type WirelessVideo = HTMLVideoElement & {webkitShowPlaybackTargetPicker?: () => void};
  const element = video as WirelessVideo;
  const safari = /Safari\//.test(userAgent) && !/Chrome\/|Chromium\/|Edg\/|OPR\/|CriOS\/|FxiOS\//.test(userAgent);
  const supported = safari && typeof element.webkitShowPlaybackTargetPicker === 'function';
  const available = () => {try {return supported && !element.disableRemotePlayback && source?.isCurrent() === true;} catch {return false;}};
  return {
    available,
    subscribe: listener => {
      if (!supported) return () => {};
      const observer = new MutationObserver(listener);
      observer.observe(element, {attributes: true, attributeFilter: ['disableremoteplayback']});
      element.addEventListener('loadedmetadata', listener);
      element.addEventListener('emptied', listener);
      const offSource = source?.subscribe(listener);
      return () => {offSource?.();observer.disconnect(); element.removeEventListener('loadedmetadata', listener); element.removeEventListener('emptied', listener); };
    },
    showPicker: () => { if (available()) element.webkitShowPlaybackTargetPicker!(); },
  };
}
