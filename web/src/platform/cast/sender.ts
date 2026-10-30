import {type CastControllerContext, type CastRemoteIdentity, CastController, sameCastRemote, type CastApi, type CastChoices, type CastMetadata, type CastSnapshot} from '@core/cast/index.ts';
import {castDisplayName, currentCastBrowser, isCastBrowser, loadCastSdk, type CastBrowser, type CastContext, type CastFramework, type CastSession, type CastSessionEvent} from './sdk';
import {castTransportFor, confirmedCastDevice, type ConfirmedCastDevice} from './transport';

export type ConfirmedCastPlayback = Readonly<{itemId: string; versionId?: string; positionSeconds: number; durationSeconds: number; paused: boolean}>;
export type WebCastSnapshot = CastSnapshot & Readonly<{
  device?: ConfirmedCastDevice;
  /** Caller lifetime only; never a server authority or media grant. */
  controlRevision?:number;
  /** The latest receiver status, retained on stop/replacement for the continuation offer.
   * Absent before the receiver reports; a requested seek/start is not a confirmed position. */
  receiver?: ConfirmedCastPlayback;
}>;
export type WebCastSenderOptions = Readonly<{
  api: CastApi;
  controllerContext?:CastControllerContext;
  isCurrent?:()=>boolean;
  serverOrigin: string;
  castApplicationId?: string;
  displayName?: string;
  /** An optional viewer-bound catalogue read for a session rejoined after a page reload. */
  describe?: (itemId: string) => Promise<string | undefined>;
  browser?: CastBrowser;
}>;

const unavailable: WebCastSnapshot = Object.freeze({available: false, phase: 'off', positionSeconds: 0, durationSeconds: 0, paused: false, audioTracks: Object.freeze([]), textTracks: Object.freeze([])});

function secureOrigin(value: string): string | undefined {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password ? url.origin : undefined;
  } catch { return undefined; }
}

/** The web SDK adapter; all pairing, receiver ACKs, status and actions belong to CastController.
 * Its caller owns local playback and only leaves that player after phase becomes `casting`.
 * Create one per viewer/server authority and dispose it when that authority changes. */
export class WebCastSender {
  private readonly browser?: CastBrowser;
  private readonly applicationId: string;
  private readonly controller?: CastController;
  private readonly listeners = new Set<() => void>();
  private readonly retentionListeners = new Set<() => void>();
  private readonly pendingAdoptions = new Set<Readonly<{operation: number}>>();
  private publishedRetention = false;
  private snapshot: WebCastSnapshot = unavailable;
  private device?: ConfirmedCastDevice;
  /** The last complete receiver clock with the exact remote occurrence that reported it; a
   * successor occurrence, generation or controller on the same item is not this clock. */
  private receiver?: Readonly<{identity?: CastRemoteIdentity; clock: ConfirmedCastPlayback}>;
  private context?: CastContext;
  private framework?: CastFramework;
  private preparation?: Promise<boolean>;
  private cancelIdle?: () => void;
  private offController?: () => void;
  private offResume?: () => void;
  private cancelStart?: () => void;
  private operation = 0;
  private pickerBusy = false;
  private pickerOperation?: number;
  private disposed = false;

  constructor(private readonly options: WebCastSenderOptions) {
    this.browser = options.browser ?? currentCastBrowser();
    this.applicationId = options.castApplicationId?.trim() ?? '';
    const origin = secureOrigin(options.serverOrigin);
    if (!origin || !/^[A-Za-z0-9_-]{1,128}$/.test(this.applicationId) || !isCastBrowser(this.browser)) return;
    this.controller = new CastController({api: options.api, origin, controllerContext:options.controllerContext,isCurrent:options.isCurrent,describe:options.describe,displayName: options.displayName ?? castDisplayName(this.browser.navigator.userAgent)});
    this.offController = this.controller.subscribe(() => this.changed());
  }

  getSnapshot = (): WebCastSnapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  /** Actual SDK selection/adoption can precede a published remote presentation. */
  retainsMedia = (): boolean => {
    if (this.disposed || this.options.isCurrent?.() === false) return false;
    return this.pickerBusy && this.pickerOperation === this.operation || [...this.pendingAdoptions].some(pending => pending.operation === this.operation) || this.snapshot.transferPending === true
      || ['connecting', 'pairing', 'waiting', 'casting'].includes(this.snapshot.phase)
      || !!this.receiver && this.snapshot.remoteStopped === true;
  };
  onRetention = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.retentionListeners.add(listener);return () => {this.retentionListeners.delete(listener);};
  };
  private publishRetention(): void {
    const retained = this.retainsMedia();
    if (retained === this.publishedRetention) return;
    this.publishedRetention = retained;
    for (const listener of [...this.retentionListeners]) {
      try {listener();} catch { /* Observation cannot interrupt the original Cast flight. */ }
    }
  }
  private changed(): void {
    if (this.disposed) return;
    const state = this.controller?.getSnapshot() ?? unavailable;
    if(state.disclosureRefused){this.receiver=undefined;this.device=undefined;}
    else if((state.phase==='casting'||state.remoteStopped)&&state.reported)
      this.receiver=Object.freeze({...(state.remoteIdentity?{identity:state.remoteIdentity}:{}),clock:state.reported});
    else if(state.remoteStopped)this.receiver=undefined;
    // Only a complete receiver clock is a continuation fact. An incomplete status of the same
    // occurrence, or an idle status without an item identity, is not a position and cannot
    // erase the last one; stop/replacement retains it, a new start clears it above. A successor
    // occurrence, generation or controller that plays the same item is a different fact: its
    // own clock is unknown until it reports, so the predecessor's clock must go.
    else if(state.remoteIdentity&&this.receiver&&!sameCastRemote(state.remoteIdentity,this.receiver.identity))this.receiver=undefined;
    const retained=this.receiver, receiver=retained?.clock;
    const samePlayback = receiver && (state.phase === 'off' || state.phase === 'replaced'
      || (retained?.identity||state.remoteIdentity ? sameCastRemote(state.remoteIdentity,retained?.identity) : state.itemId === receiver.itemId));
    this.snapshot = Object.freeze({
      ...state,controlRevision:this.operation,
      ...(samePlayback ? {positionSeconds: receiver.positionSeconds, durationSeconds: receiver.durationSeconds} : {}),
      ...(receiver ? {receiver} : {}),
      ...(this.device && state.phase !== 'off' && state.phase !== 'replaced' ? {device: this.device} : {}),
    });
    this.publishRetention();
    for (const listener of [...this.listeners]) listener();
  }

  /** Called by the shell after setup. With no idle API, only player-open prepares Cast. */
  prepareOnIdle(): () => void {
    const browser = this.browser;
    if (this.disposed || !this.controller || this.snapshot.available || !browser?.requestIdleCallback) return () => {};
    if (this.cancelIdle) return this.cancelIdle;
    const handle = browser.requestIdleCallback(() => {
      this.cancelIdle = undefined;
      void this.prepareForPlayer();
    });
    const cancel = () => {
      browser.cancelIdleCallback?.(handle);
      if (this.cancelIdle === cancel) this.cancelIdle = undefined;
    };
    this.cancelIdle = cancel;
    return cancel;
  }

  /** Explicit player-open entry point. It never opens a device picker. */
  prepareForPlayer(): Promise<boolean> {
    this.cancelIdle?.();
    if (this.disposed || !this.controller || !this.browser) return Promise.resolve(false);
    if (this.preparation) return this.preparation;
    const preparation = this.prepareSdk();
    this.preparation = preparation;
    void preparation.then(ready => { if (!ready && this.preparation === preparation) this.preparation = undefined; });
    return preparation;
  }
  private async prepareSdk(): Promise<boolean> {
    const controller = this.controller, browser = this.browser;
    if (!controller || !browser) return false;
    try {
      const framework = await loadCastSdk(browser);
      if (!framework || this.disposed) return false;
      const context = framework.CastContext.getInstance();
      context.setOptions({receiverApplicationId: this.applicationId, autoJoinPolicy: browser.chrome?.cast?.AutoJoinPolicy.ORIGIN_SCOPED ?? 'origin_scoped'});
      this.context = context;
      this.framework = framework;
      const listener = (event: CastSessionEvent) => { if (event.sessionState === framework.SessionState.SESSION_RESUMED) this.adopt(); };
      const type = framework.CastContextEventType.SESSION_STATE_CHANGED;
      context.addEventListener(type, listener);
      this.offResume = () => context.removeEventListener(type, listener);
      controller.setAvailable(true);
      this.adopt();
      return !this.disposed;
    } catch {
      this.offResume?.();
      this.offResume = undefined;
      this.context = undefined;
      this.framework = undefined;
      if (!this.disposed) controller.setAvailable(false);
      return false;
    }
  }

  private adopt(): void {
    const controller = this.controller, context = this.context, framework = this.framework;
    if (this.disposed || this.pickerBusy || !controller || !context || !framework || controller.getSnapshot().phase !== 'off') return;
    try {
      const session = context.getCurrentSession();
      if (!session) return;
      const operation=++this.operation,device=confirmedCastDevice(session);
      this.device=undefined;this.receiver=undefined;
      const transport = this.transport(session,context,framework), pending = {operation};
      this.pendingAdoptions.add(pending);this.publishRetention();
      void controller.adopt(transport,this.options.describe).then(ok=>{
        if(this.disposed||operation!==this.operation)return;
        if(ok)this.device=device;
        this.changed();
      },()=>{/* Failed automatic adoption leaves the receiver alone. */}).finally(()=>{
        this.pendingAdoptions.delete(pending);this.publishRetention();
      });
    } catch { /* A session no longer present cannot be adopted. */ }
  }

  /** Invoke from the device-row click after availability. The SDK picker opens before any await.
   * The result is true only after the shared controller receives the receiver's load ACK. */
  start(itemId: string, meta: CastMetadata, startSeconds: number, choices?: CastChoices, readStart?: () => number | undefined): Promise<boolean> {
    const controller = this.controller, context = this.context, framework = this.framework;
    if (this.disposed || this.pickerBusy || !this.snapshot.available || !controller || !context || !framework) return Promise.resolve(false);
    const operation = ++this.operation;
    this.device = undefined;
    this.receiver = undefined;
    this.pickerBusy = true;this.pickerOperation = operation;
    controller.connecting(meta.title);
    let cancel!: () => void;
    const canceled = new Promise<boolean>(resolve => { cancel = () => resolve(false); });
    this.cancelStart = cancel;
    const opening = this.openPicker(operation, itemId, meta, startSeconds, choices, readStart);
    return Promise.race([opening, canceled]).finally(() => { if (this.cancelStart === cancel) this.cancelStart = undefined; });
  }
  private async openPicker(operation: number, itemId: string, meta: CastMetadata, startSeconds: number, choices?: CastChoices, readStart?: () => number | undefined): Promise<boolean> {
    const controller = this.controller!, context = this.context!, framework = this.framework!;
    try {
      await context.requestSession();
      if (this.disposed || operation !== this.operation) return false;
      const session = context.getCurrentSession();
      if (!session) { controller.cancelConnecting(); return false; }
      this.device = confirmedCastDevice(session);
      const transport = this.transport(session, context, framework);
      // Read last, after the SDK picker has resolved and immediately before shared start.
      // Undefined means the original playback/queue witness retired while the picker was open.
      const position = readStart ? readStart() : startSeconds;
      if (position === undefined || !Number.isFinite(position) || position < 0) {controller.cancelConnecting();return false;}
      return await controller.start(transport, itemId, meta, position, choices);
    } catch {
      if (!this.disposed && operation === this.operation) controller.cancelConnecting();
      return false;
    } finally { this.pickerBusy = false; this.pickerOperation = undefined; this.publishRetention(); }
  }

  private transport(session: CastSession, context: CastContext, framework: CastFramework) {
    // Raw SDK status is untrusted until the shared controller has completed
    // Root50 authority + content validation for this exact transport lifetime.
    return castTransportFor(session, context, framework);
  }

  /** Invalidates pending SDK selection and pairing; a late picker cannot send a new load. */
  cancelConnecting(): void {
    if (this.snapshot.phase === 'connecting' || this.snapshot.phase === 'pairing' || this.snapshot.phase === 'waiting' || this.snapshot.phase === 'error') this.stop();
  }
  play(): void { this.controller?.play(); }
  pause(): void { this.controller?.pause(); }
  toggle(): void { this.controller?.toggle(); }
  seek(positionSeconds: number): void { this.controller?.seek(positionSeconds); }
  setAudioTrack(trackId: number): void { this.controller?.setAudioTrack(trackId); }
  setSubtitleTrack(trackId: number | null): void { this.controller?.setSubtitleTrack(trackId); }
  answerTakeover(allow:boolean,expected?:CastSnapshot['takeover']):void {if(expected)this.controller?.answerTakeover(allow,expected);}
  stop(): void {
    this.operation++;
    this.cancelStart?.();
    this.cancelStart = undefined;
    this.controller?.stop();
    this.publishRetention();
  }
  dismiss(): void { this.operation++;this.controller?.dismiss();this.publishRetention(); }
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.operation++;
    this.cancelStart?.();
    this.cancelStart = undefined;
    this.cancelIdle?.();
    this.offResume?.();
    this.offController?.();
    this.controller?.dispose();
    this.pendingAdoptions.clear();this.publishRetention();this.retentionListeners.clear();
    this.listeners.clear();
  }
}
