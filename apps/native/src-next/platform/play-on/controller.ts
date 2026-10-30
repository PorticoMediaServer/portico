import type {RefObject} from 'react';
import type {View} from 'react-native';
import type {CastDockIdentity} from '../../kit/dock';
import type {CastSnapshot} from '../../../../../packages/client-core/src/cast';
import {playbackControlsService} from '../../../../../packages/client-core/src/playback-controls';
import {HandoffController, type HandoffPhase, type HandoffSnapshot} from '../../../../../packages/client-core/src/remote-playback';
import type {Receiver} from '../../../../../packages/client-core/src/social-playback';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {QueueEntry} from '../../../../../packages/client-core/src/playback-v1/types';
import type {LocalSession} from '../../../../../packages/client-core/src/index';
import {sameFence, type DockPlayback, type PlaybackFence, type RemoteDock} from '../../../../../packages/player-core/src';
import {routePath} from '../../../../../packages/contracts/src/routes';
import {NativeCastCaller, type NativeCastState} from './cast';
import type {NativePlaybackAdapter} from '../playback/types';

/** Play on, natively: the Portico devices this profile may send to, the handoff that
 * moves this phone's playback to one of them, and the remote that follows it after.
 *
 * Every fact is the server's or the receiving device's own: the directory comes from
 * `GET /v1/receivers`, the takeover only counts once the television proves it is
 * playing, and the position and controls the remote shows are read from the playing
 * device through `GET /v1/playback/controls`. Cast uses the installed native
 * transport and the shared admitted queue-transfer controller.
 *
 * One send is one attempt with its own connection id: the initiating dock playback,
 * fence, authority and session are captured before any await and checked again before
 * every write and after every answer. A control from a retained attempt can never act
 * on a later one, and a late poll can never publish over a successor. */

export type PlayOnRemote = Readonly<{
  id: string;
  device: string;
  title?: string;
  itemId?: string;
  /** Captured display facts belong only to this item, never a later local title. */
  entry?: ContentEntry;
  /** The receiving session's own next entry, when the controls report supplies it. */
  next?: QueueEntry;
  positionSeconds: number;
  positionKnown: boolean;
  durationSeconds: number;
  playing: boolean;
  stopped: boolean;
  /** Only what the playing device's own report allows; nothing before the first report. */
  allows: Readonly<Record<'pause' | 'resume' | 'seek' | 'stop', boolean>>;
}>;

export type PlayOnSnapshot = Readonly<{
  open: boolean;
  /** The actual opener, measured by the tablet popover and used for focus return. */
  anchorRef?: RefObject<View | null>;
  cast?: NativeCastState;
  castRemoteOpen?: boolean;
  receivers: readonly Receiver[];
  directoryPhase: HandoffSnapshot['directoryPhase'];
  directoryError?: string;
  phase: HandoffPhase;
  message?: string;
  target?: Receiver;
  sending: boolean;
  remote?: PlayOnRemote;
  /** The full remote is open (the dock's bar opened it). */
  remoteOpen: boolean;
  /** A failed attempt's text stays on the sheet until the next one. */
  error?: string;
}>;

type Options = Readonly<{
  api: Readonly<{baseUrl?: string; createUserMutationKey?():Promise<string>; request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>;
    requestRaw?(path: string, method: string, options?: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>): Promise<{status: number; headers: Record<string, string>; body?: unknown}>}>;
  session: () => LocalSession | undefined;
  deviceName: () => string;
  playback: () => NativePlaybackAdapter | undefined;
  current: () => boolean;
  reportError: (error: unknown) => void;
}>;

const POLL_MS = 5000;
const STOPPED_CLEAR_MS = 15_000;
const wire = (value: string) => value.replace(/[^A-Za-z0-9_-]/g, '_').slice(0, 96);
const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
const NONE = Object.freeze({pause: false, resume: false, seek: false, stop: false});

export class NativePlayOn {
  private readonly options: Options;
  private readonly listeners = new Set<() => void>();
  private controller?: HandoffController;
  private timer?: ReturnType<typeof setInterval>;
  private sequence = 0;
  private attempt = 0;
  private connection?: string;
  private fence?: PlaybackFence;
  private local?: DockPlayback;
  private receiverPlaybackId?: string;
  /** The immutable original send lease: the session, authority and local fence the
   * handoff was started from. Every handoff IO re-checks it; a replaced playback run
   * (same authority, new fence) retires the attempt. */
  private lease?: Readonly<{session: LocalSession; authority: NonNullable<ReturnType<NativePlaybackAdapter['getSnapshot']>['authority']>; fence: PlaybackFence}>;
  private polling = false;
  private disposed = false;
  private state: PlayOnSnapshot = Object.freeze({open: false, receivers: [], directoryPhase: 'idle', phase: 'idle', sending: false, remoteOpen: false});

  readonly cast: NativeCastCaller;
  private offCast?: () => void;
  private off?: () => void;
  constructor(options: Options) {
    this.options = options;
    this.cast = new NativeCastCaller(options);
    this.offCast = this.cast.subscribe(() => {const cast=this.cast.getSnapshot(),began=cast.cast.phase==='casting'&&this.state.cast?.cast.phase!=='casting';
      this.publish({cast,...(began?{open:false,castRemoteOpen:!cast.resumed}:{})});});
    this.state=Object.freeze({...this.state,cast:this.cast.getSnapshot()});
    // Another surface may end the remote first (Continue here on the bar, Dismiss,
    // or the dock's own expiry); the controller follows the dock, never the reverse.
    this.off = options.playback()?.subscribe(() => {
      const current = options.playback()?.getSnapshot(), remote = current?.dock.remote;
      // A new local playback run (same authority, a different fence) while the handoff
      // is still being asked/prepared retires it: a successor's session is never borrowed.
      if (this.state.sending && this.lease && current?.fence && !sameFence(current.fence, this.lease.fence)) {
        this.attempt++; this.controller?.cancel(); this.lease = undefined;
        if (this.state.remote) this.clearRemote(); else this.publish({sending: false, phase: 'failed', error: 'playOn.cancelled'});
        return;
      }
      if (this.state.remote && (!remote || remote.id !== this.state.remote.id)) this.clearRemote();
    });
  }

  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = (): PlayOnSnapshot => this.state;
  private publish(patch: Partial<PlayOnSnapshot>): void {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  /** True only for the attempt that is still the one on screen. */
  private bound(attempt: number, connection?: string, receiverPlaybackId?: string): boolean {
    return !this.disposed && this.attempt === attempt && this.options.current()
      && (connection === undefined || this.connection === connection)
      && (receiverPlaybackId === undefined || this.receiverPlaybackId === receiverPlaybackId);
  }

  private handoff(): HandoffController | undefined {
    if (this.disposed || !this.options.current()) return undefined;
    return this.controller;
  }

  /** The directory belongs to the app, not to one attempt: its answers always fill the sheet. */
  private follow(snapshot: HandoffSnapshot): void {
    const patch: Partial<PlayOnSnapshot> = {receivers: snapshot.receivers, directoryPhase: snapshot.directoryPhase,
      ...(snapshot.directoryError ? {directoryError: snapshot.directoryError} : {}), phase: snapshot.phase,
      ...(snapshot.message ? {message: snapshot.message} : {}), ...(snapshot.target ? {target: snapshot.target} : {})};
    this.publish(patch);
  }

  open = (anchorRef?: RefObject<View | null>): boolean => {
    if (this.disposed || !this.options.current()) return false;
    const session = this.options.session();
    if (!this.controller && session?.deviceId) {
      const controller = new HandoffController({
        api: {request: (path, method, body, signal) => this.request(path, method, body, signal),
          ...(this.options.api.requestRaw ? {requestRaw: (path, method, options) => this.requestRaw(path, method, options)} : {})},
        device: {deviceId: session.deviceId, displayName: this.options.deviceName()},
        requestId: () => `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`,
      });
      const stop = controller.subscribe(() => this.follow(controller.getSnapshot()));
      this.controller = controller;
      this.offController = stop;
    }
    if (!this.controller) return false;
    this.publish({open: true, anchorRef, error: undefined});
    void this.controller.refresh().catch(error => {if (this.options.current()) this.options.reportError(error);});
    return true;
  };
  private offController?: () => void;

  close = (): void => {
    if (!this.state.open) return;
    this.publish({open: false});
    if (this.state.phase !== 'done') this.controller?.cancel();
    const cast=this.cast.getSnapshot().cast;if(['connecting','pairing','waiting'].includes(cast.phase)||cast.transferPending)this.cast.controls('stop');
  };

  /** A request from the attempt that started it: the identity captured at send is checked
   * before and after every call, so a replaced authority/session cannot continue one. */
  private async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    // The receiver directory is a read of who exists, not a handoff: it answers before
    // any send exists, so it is guarded by the current session and authority captured at
    // this request. Every actual handoff call below still requires the immutable lease.
    if (method === undefined && path === routePath('social_get_v1_receivers')) {
      const session = this.options.session(), authority = this.options.playback()?.getSnapshot().authority;
      const same = () => this.options.current() && !!session && this.options.session() === session
        && sameAuthority(this.options.playback()?.getSnapshot().authority, authority);
      if (!same()) throw cancelled();
      const value = await this.options.api.request<T>(path, method, body, signal);
      if (!same()) throw cancelled();
      return value;
    }
    const attempt = this.attempt, connection = this.connection, lease = this.lease;
    const same = () => this.bound(attempt, connection) && !!lease && this.options.session() === lease.session
      && sameAuthority(this.options.playback()?.getSnapshot().authority, lease.authority)
      && !!this.options.playback()?.getSnapshot().fence && sameFence(this.options.playback()!.getSnapshot().fence!, lease.fence);
    if (!same()) throw cancelled();
    const value = await this.options.api.request<T>(path, method, body, signal);
    if (!same()) throw cancelled();
    return value;
  }

  /** The session-control transport (SessionsClient builds on requestRaw): the same
   * captured identity guard, so a pause/seek from an old attempt cannot reach a new one. */
  private async requestRaw(path: string, method: string, options?: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>)
    : Promise<{status: number; headers: Record<string, string>; body?: unknown}> {
    const attempt = this.attempt, connection = this.connection, lease = this.lease;
    // The remote control follows a committed handoff, where the local player has already
    // retired; its fence is not required, but the session and authority never change.
    const same = () => this.bound(attempt, connection) && !!lease && this.options.session() === lease.session
      && sameAuthority(this.options.playback()?.getSnapshot().authority, lease.authority);
    if (!same() || !this.options.api.requestRaw) throw cancelled();
    const value = await this.options.api.requestRaw(path, method, options);
    if (!same()) throw cancelled();
    return value;
  }

  /** Moves this phone's playback to the receiver. The statement of what is playing is
   * this device's own, captured before the handoff starts; a replacement dock playback
   * is never stopped and its title is never restored. */
  startCast = (): void => {if(!this.state.sending&&!this.state.remote){this.cast.start();this.publish({castRemoteOpen:false});}};
  openCastRemote = (): void => {this.publish({castRemoteOpen:true,open:false});};
  closeCastRemote = (): void => {this.publish({castRemoteOpen:false});};
  castCommand=(type:string,position?:number,expected?:CastDockIdentity,takeover?:CastSnapshot['takeover']):void=>{this.cast.controls(type,position,expected,takeover);};

  send = async (receiver: Receiver): Promise<void> => {
    if(this.state.cast?.cast.transferPending||['connecting','pairing','waiting','casting'].includes(this.state.cast?.cast.phase??''))return;
    const playback = this.options.playback(), session = this.options.session();
    if (!playback || !session) return;
    const snapshot = playback.getSnapshot(), fence = snapshot.fence, local = snapshot.dock.local;
    if (!fence || fence.kind !== 'queue' || snapshot.playback.phase !== 'ready' || !local || !sameFence(local.fence, fence)) return;
    if (!this.open(this.state.anchorRef)) return;
    const attempt = ++this.attempt;
    const connection = `playon_${wire(receiver.id)}_${attempt.toString(36)}${Date.now().toString(36)}`;
    this.stopPolling();
    this.lease = Object.freeze({session, authority: snapshot.authority, fence});
    this.connection = connection; this.fence = fence; this.local = Object.freeze({...local, playing: false, expanded: false});
    this.receiverPlaybackId = undefined;
    this.publish({sending: true, message: undefined, error: undefined, phase: 'asking', target: receiver});
    const playing = await this.controller!.send(receiver, {playbackId: fence.sessionId, positionSeconds: snapshot.playback.positionSeconds}).catch(error => {
      if (this.bound(attempt, connection)) this.options.reportError(error);
      return false;
    });
    // A stale predecessor belongs to an older attempt; it must not cancel the new one.
    if (!this.bound(attempt, connection)) return;
    const now = playback.getSnapshot(), stillLocal = now.dock.local;
    if (!playing || !stillLocal || !sameFence(stillLocal.fence, fence)) {
      this.publish({sending: false, phase: playing ? 'idle' : 'failed', open: true,
        error: playing ? undefined : this.state.message ?? 'playOn.notReachable'});
      return;
    }
    this.receiverPlaybackId = this.controller!.getSnapshot().receiverPlaybackId;
    const entry = snapshot.entry?.id === fence.itemId ? snapshot.entry : undefined;
    this.publish({open: false, sending: false, message: this.controller!.getSnapshot().message, target: receiver, error: undefined,
      remote: {id: connection, device: receiver.displayName, title: entry?.displayTitle ?? entry?.title, itemId: fence.itemId,
        entry,
        positionSeconds: snapshot.playback.positionSeconds, durationSeconds: snapshot.playback.duration,
        positionKnown: false, playing: true, stopped: false, allows: NONE}});
    await this.push('playing', true, attempt, connection);
    this.startPolling(attempt, connection);
  };

  /** The dock's bar for this attempt only. The captured initiating playback is the
   * occupant's identity; nothing here reads a later dock.local. */
  private async push(phase: RemoteDock['phase'], initial: boolean, attempt = this.attempt, connection = this.connection): Promise<void> {
    const adapter = this.options.playback(), fence = this.fence, local = this.local, remote = this.state.remote;
    if (!adapter || !fence || !local || !remote || !this.bound(attempt, connection)) return;
    const value: RemoteDock = Object.freeze({id: remote.id, kind: 'play-on', phase, sequence: String(++this.sequence),
      positionSeconds: Math.max(0, remote.positionSeconds), playback: Object.freeze({...local, positionSeconds: Math.max(0, remote.positionSeconds), playing: remote.playing, expanded: false})});
    try {
      if (initial) await adapter.beginRemote?.(value);
      else await adapter.updateRemote?.(value);
    } catch (error) {if (this.bound(attempt, connection)) this.options.reportError(error);}
  }

  private startPolling(attempt: number, connection: string): void {
    this.stopPolling();
    this.timer = setInterval(() => {void this.poll(attempt, connection);}, POLL_MS);
    void this.poll(attempt, connection);
  }
  private stopPolling(): void {
    if (this.timer === undefined) return;
    clearInterval(this.timer); this.timer = undefined;
  }

  /** The playing device's own account of itself. One poll at a time, fenced by the
   * attempt and the receiver playback id it started with. */
  private async poll(attempt: number, connection: string | undefined): Promise<void> {
    const controller = this.controller, sessionId = this.receiverPlaybackId;
    if (!connection || this.polling || !controller || !sessionId || !this.bound(attempt, connection, sessionId)) return;
    this.polling = true;
    try {
      await controller.refreshRemote();
      if (!this.bound(attempt, connection, sessionId)) return;
      const page = await playbackControlsService(<T,>(path: string, method?: string, body?: unknown) => this.options.api.request<T>(path, method, body)).remote();
      if (!this.bound(attempt, connection, sessionId)) return;
      const report = page.items.find(item => item.sessionId === sessionId);
      const current = this.state.remote;
      if (!report || !current) {
        // No report is not a grant: every control stays off until the device answers.
        if (current) this.publish({remote: {...current, positionKnown: false, next: undefined, allows: NONE}});
        return;
      }
      // An unavailable read is not an ended session: only its own report can say ended.
      const stopped = report.state === 'ended';
      const allows = {pause: report.actions.includes('pause'), resume: report.actions.includes('resume'),
        seek: report.actions.includes('seek'), stop: report.actions.includes('stop')};
      this.publish({remote: {...current, positionSeconds: Math.max(0, report.positionMs / 1000),
        positionKnown: true, itemId: report.item?.id, entry: report.item?.id === current.entry?.id ? current.entry : undefined,
        next: report.next?.available ? report.next : undefined,
        durationSeconds: report.durationMs ? report.durationMs / 1000 : report.item?.id === current.itemId ? current.durationSeconds : 0,
        playing: report.state === 'playing' && !stopped, stopped, allows, title: report.item?.title,
        ...(report.device.name ? {device: report.device.name} : {})}});
      if (stopped) {this.stopPolling(); await this.push('stopped', false, attempt, connection);}
      else await this.push('playing', false, attempt, connection);
    } catch (error) {
      const current = this.state.remote;
      if (this.bound(attempt, connection, sessionId)) {
        if (current) this.publish({remote: {...current, positionKnown: false, next: undefined, allows: NONE}});
        this.options.reportError(error);
      }
    } finally {this.polling = false;}
  }

  private remoteBound(remoteId?: string, action?: keyof PlayOnRemote['allows']): boolean {
    const remote = this.state.remote;
    return !!remoteId && remoteId === this.connection && !this.disposed && this.options.current()
      && (!action || !!remote && !remote.stopped && remote.allows[action]);
  }
  pause = async (remoteId?: string): Promise<void> => {const controller = this.controller; if (!controller || !this.remoteBound(remoteId, 'pause')) return;
    const attempt = this.attempt, connection = this.connection, sessionId = this.receiverPlaybackId;
    await controller.pause().catch(() => {});
    if (this.bound(attempt, connection, sessionId)) await this.poll(attempt, connection);};
  resume = async (remoteId?: string): Promise<void> => {const controller = this.controller; if (!controller || !this.remoteBound(remoteId, 'resume')) return;
    const attempt = this.attempt, connection = this.connection, sessionId = this.receiverPlaybackId;
    await controller.play().catch(() => {});
    if (this.bound(attempt, connection, sessionId)) await this.poll(attempt, connection);};
  startOver = async (remoteId?: string): Promise<void> => {const controller = this.controller; if (!controller || !this.remoteBound(remoteId, 'seek')) return;
    const attempt = this.attempt, connection = this.connection, sessionId = this.receiverPlaybackId;
    await controller.seek(0).catch(() => {});
    if (this.bound(attempt, connection, sessionId)) await this.poll(attempt, connection);};
  skip = async (deltaSeconds: number, remoteId?: string): Promise<void> => {
    const controller = this.controller, remote = this.state.remote;
    if (!controller || !remote || !this.remoteBound(remoteId, 'seek') || !Number.isFinite(deltaSeconds)) return;
    const attempt = this.attempt, connection = this.connection, sessionId = this.receiverPlaybackId;
    const target = Math.max(0, Math.min(remote.durationSeconds || Number.POSITIVE_INFINITY, remote.positionSeconds + deltaSeconds));
    await controller.seek(target).catch(() => {});
    if (this.bound(attempt, connection, sessionId)) await this.poll(attempt, connection);
  };
  stop = async (remoteId?: string): Promise<void> => {
    const controller = this.controller;
    if (!controller || !this.remoteBound(remoteId)) return;
    // While it is still asking or preparing, Stop cancels the request: nothing has moved.
    if (controller.getSnapshot().phase !== 'done') {this.attempt++; controller.cancel(); this.publish({sending: false, phase: 'idle', message: undefined}); return;}
    if (!this.remoteBound(remoteId, 'stop')) return;
    const attempt = this.attempt, connection = this.connection, sessionId = this.receiverPlaybackId;
    const accepted = await controller.stop().catch(() => false);
    if (!this.bound(attempt, connection, sessionId)) return;
    // A refused stop leaves the title where it is: no stopped bar and no Continue here.
    if (!accepted) return;
    this.stopPolling();
    const current = this.state.remote;
    if (current) this.publish({remote: {...current, playing: false, stopped: true, allows: NONE}});
    await this.push('stopped', false, attempt, connection);
    setTimeout(() => {if (this.bound(attempt, connection) && this.state.remote?.stopped) void this.dismiss(connection);}, STOPPED_CLEAR_MS);
  };
  /** Brings the title back to this phone where the television stopped. */
  continueHere = async (remoteId?: string): Promise<void> => {
    const adapter = this.options.playback(), current = this.state.remote;
    if (!adapter || !current || !this.remoteBound(remoteId)) return;
    const attempt = this.attempt, connection = this.connection;
    try {await adapter.continueHere?.(current.id);} catch (error) {if (this.options.current()) this.options.reportError(error);}
    // A later send while the restore was in flight keeps its own remote.
    if (this.bound(attempt, connection) && this.state.remote?.id === current.id) this.clearRemote();
  };
  dismiss = async (remoteId?: string): Promise<void> => {
    const adapter = this.options.playback(), current = this.state.remote;
    if (!current || !this.remoteBound(remoteId ?? current.id)) return;
    this.clearRemote();
    if (adapter) {try {await adapter.dismissRemote?.(current.id);} catch (error) {if (this.options.current()) this.options.reportError(error);}}
  };
  openRemote = (): void => {if (this.state.remote) this.publish({remoteOpen: true, open: false});};
  closeRemote = (): void => {if (this.state.remoteOpen) this.publish({remoteOpen: false});};
  private clearRemote(): void {
    this.attempt++;
    this.stopPolling();
    this.connection = undefined; this.receiverPlaybackId = undefined; this.fence = undefined; this.local = undefined; this.lease = undefined;
    if (this.state.remoteOpen) this.publish({remoteOpen: false});
    if (this.state.remote) this.publish({remote: undefined});
  }
  /** The dock's remote commands, always bound to the bar's own remote id. */
  command = async (type:string,positionSeconds?:number,remoteId?:string,expected?:CastDockIdentity):Promise<void> => {
    const cast=this.cast.getSnapshot();
    if(remoteId&&(cast.source?.id===remoteId||cast.resumed?.id===remoteId)){
      if(cast.resumed&&!expected||expected&&!this.cast.viewIsCurrent(expected))return;
      if(type==='open-remote')return this.openCastRemote();
      if(type==='continue-here')return this.cast.continueHere(expected);
      if(type==='dismiss-remote')return this.cast.dismiss();
      this.cast.controls(type,positionSeconds,expected);return;
    }
    if(expected)return; // A retained Cast view never falls through to another remote.
    switch (type) {
      case 'pause': return this.pause(remoteId);
      case 'play': case 'resume': return this.resume(remoteId);
      case 'seek': {
        const controller = this.controller;
        if (controller && this.remoteBound(remoteId, 'seek') && positionSeconds !== undefined && Number.isFinite(positionSeconds)) {
          const attempt = this.attempt, connection = this.connection, sessionId = this.receiverPlaybackId;
          const duration = this.state.remote?.durationSeconds;
          await controller.seek(Math.max(0, Math.min(duration || Number.POSITIVE_INFINITY, positionSeconds))).catch(() => {});
          if (this.bound(attempt, connection, sessionId)) await this.poll(attempt, connection);
        }
        return;
      }
      case 'open-remote': return this.openRemote();
      case 'stop': return this.stop(remoteId);
      case 'continue-here': return this.continueHere(remoteId);
      case 'dismiss-remote': return this.dismiss(remoteId);
      default: return;
    }
  };
  dispose(): void {
    this.disposed = true;
    this.stopPolling();
    this.offCast?.();this.offCast=undefined;this.cast.dispose();
    this.off?.(); this.off = undefined;
    this.offController?.(); this.offController = undefined;
    this.controller?.dispose(); this.controller = undefined;
    this.listeners.clear();
  }
}

const sameAuthority = (a: Readonly<{serverId: string; accountId: string; profileId: string; epoch: string}> | undefined,
  b: Readonly<{serverId: string; accountId: string; profileId: string; epoch: string}> | undefined): boolean =>
  !!a && !!b && a.serverId === b.serverId && a.accountId === b.accountId && a.profileId === b.profileId && a.epoch === b.epoch;
