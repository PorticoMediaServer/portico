import {HandoffController} from '@core/remote-playback.ts';
import {sameFence} from '../../../../packages/player-core/src/index.ts';
import {defaultI18n, type MessageId, type MessageValues} from '@i18n';
import type {HttpLocalApi} from '@core/index.ts';
import type {WebShellPlaybackRuntime} from '../../platform/playback';
import type {PlayOnLinkPort, PlayOnPorticoRemote, PlayOnReceiversPort, PlayOnReceiver} from '../../platform/playback/play-on';
import {receiverLinkOperations} from '@core/query/receiver.ts';
import {repeat} from '@core/timer.ts';

const PLATFORMS: Readonly<Record<string, string>> = {appletv: 'Apple TV', tvos: 'Apple TV', ios: 'iPhone', ipados: 'iPad', androidtv: 'Android TV', firetv: 'Fire TV', android: 'Android', web: 'Web', windows: 'Windows', macos: 'Mac', linux: 'Linux'};

export type WatchReceiverApi = Pick<HttpLocalApi, 'request'>;

/** The account's other open devices, one move of this browser's playback to one of them, and the
 * bar that follows what that device plays afterwards (rule 27) or what another device plays when
 * this browser takes control of it (rule 28). The move counts only once the other device plays
 * the title; this browser keeps playing until then, and stops only the playback run the move
 * started from. */
export class WatchReceivers implements PlayOnReceiversPort {
  private readonly handoff: HandoffController;
  private state: ReturnType<PlayOnReceiversPort['getSnapshot']> = Object.freeze({phase: 'idle' as const, items: Object.freeze([]) as readonly PlayOnReceiver[]});
  private readonly listeners = new Set<() => void>();
  /** The one send in flight; a later run in this browser retires it. */
  private flight?: object;

  constructor(private readonly options: {api: WatchReceiverApi; runtime: WebShellPlaybackRuntime; /** The shell's own catalogue translator, when the host has one. */ t?: (id: MessageId, values?: MessageValues) => string; isCurrent(): boolean; reportError(error: unknown): void}) {
    this.handoff = new HandoffController({api: options.api, isCurrent: () => options.isCurrent()});
    this.handoff.subscribe(this.publish);
    this.detach = options.runtime.subscribe(this.retireOnReplacement);
  }

  private detach?: () => void;
  private guard?: () => boolean;
  /** Reads where the followed device is while the bar shows it. */
  private poll?: ReturnType<typeof repeat>;
  private remote?: PlayOnPorticoRemote;
  private follow(): void {
    const playing = !!this.handoff.getSnapshot().receiverPlaybackId && !this.remote?.stopped;
    if (playing && !this.poll) this.poll = repeat(async () => {if (this.options.isCurrent()) await this.handoff.refreshRemote(); else this.dismiss();},
      {everyMs: 5000, backoff: {baseMs: 10_000, maxMs: 60_000}, immediate: false});
    if (!playing && this.poll) {this.poll.stop(); this.poll = undefined;}
  }
  /** A replaced playback run withdraws the move it started. */
  private retireOnReplacement = (): void => {
    if (this.flight && this.guard && !this.guard()) {this.flight = undefined; this.handoff.cancel();}
  };

  private describe(platform: string): string {
    const label = PLATFORMS[platform.toLowerCase()], t = this.options.t ?? defaultI18n.t;
    return label ? t('next.cast.receiverOn', {platform: label}) : t('next.cast.receiverOnly');
  }

  private publish = (): void => {
    const directory = this.handoff.getSnapshot();
    const items = Object.freeze(directory.receivers.map(receiver => Object.freeze({id: receiver.id, name: receiver.displayName, detail: this.describe(receiver.platform),
      ...(receiver.playing && receiver.playing.state !== 'ended' ? {playing: Object.freeze(receiver.playing.title ? {title: receiver.playing.title} : {})} : {})})));
    const message = directory.phase === 'asking' || directory.phase === 'failed' ? directory.message : undefined;
    const id = directory.receiverPlaybackId, seen = directory.remote;
    // The bar's view: the device's last read; a device that stopped keeps its place for Continue here.
    const keep = this.remote?.stopped && (!id || this.remote.sessionId === id);
    this.remote = keep ? this.remote : id ? Object.freeze({sessionId: id, deviceName: directory.target?.displayName ?? seen?.deviceName ?? '',
      ...(seen?.itemId ? {itemId: seen.itemId} : {}), ...(seen?.title ? {title: seen.title} : {}),
      positionKnown: !!seen, positionSeconds: (seen?.positionMs ?? 0) / 1000, durationSeconds: (seen?.durationMs ?? 0) / 1000,
      paused: seen?.state === 'paused', stopped: seen?.state === 'ended'}) : undefined;
    this.state = Object.freeze({phase: directory.directoryPhase, items, ...(message ? {message} : {}), ...(this.remote ? {remote: this.remote} : {})});
    this.follow();
    for (const listener of [...this.listeners]) listener();
  };

  refreshPresentation = (): void => {if (this.options.isCurrent()) this.publish();};
  getSnapshot = (): ReturnType<PlayOnReceiversPort['getSnapshot']> => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  refresh = (): void => {
    if (!this.options.isCurrent()) return;
    void this.handoff.refresh().catch(this.options.reportError);
  };

  /** True only once the other device plays what was sent. The server has then stopped this
   * browser's session; the local player stops only while the same run is still on screen. */
  async send(id: string): Promise<boolean> {
    const snapshot = this.options.runtime.getSnapshot();
    const fence = snapshot.fence, session = snapshot.session, presentation = snapshot.presentation;
    if (!this.options.isCurrent() || snapshot.disposed || !fence || fence.kind !== 'queue' || !session || !presentation
      || session.id !== fence.sessionId || session.itemId !== fence.itemId || !session.itemId) return false;
    const target = this.handoff.getSnapshot().receivers.find(receiver => receiver.id === id);
    if (!target || this.flight) return false;
    const flight = {};
    const guard = this.guard = () => {
      const live = this.options.runtime.getSnapshot();
      return this.flight === flight && this.options.isCurrent() && !live.disposed && live.authority === snapshot.authority && !!live.fence && sameFence(live.fence, fence);
    };
    this.flight = flight;
    let taken = false;
    try {
      taken = await this.handoff.send(target, {playbackId: session.id, itemId: session.itemId, positionSeconds: presentation.positionSeconds,
        ...(session.presentation.audioTrackId ? {audioTrackId: session.presentation.audioTrackId} : {}),
        subtitleTrackId: session.presentation.subtitle?.trackId ?? '',
        ...(session.queue ? {queueId: session.queue.queueId} : {})}, guard, () => this.options.isCurrent());
    } finally {
      if (this.flight === flight) this.flight = undefined;
    }
    if (!taken) return false;
    const live = this.options.runtime.getSnapshot();
    if (this.options.isCurrent() && !live.disposed && live.authority === snapshot.authority && sameFence(live.fence, fence)) await this.options.runtime.stop(fence);
    return true;
  }

  /** Takes control of what another device plays (rule 28), after its yes; this browser's own
   * playback carries on. */
  async control(id: string): Promise<boolean> {
    const target = this.handoff.getSnapshot().receivers.find(receiver => receiver.id === id);
    if (!target?.playing || !this.options.isCurrent() || this.flight) return false;
    return this.handoff.control(target, () => this.options.isCurrent());
  }
  toggle = (): void => {const remote = this.remote; if (remote && !remote.stopped) void (remote.paused ? this.handoff.play() : this.handoff.pause()).then(this.publish);};
  seek = (seconds: number): void => {if (this.remote && !this.remote.stopped) void this.handoff.seek(seconds).then(this.publish);};
  /** Stops the other device; the bar stays, stopped, with Continue here. */
  stop = (): void => {
    const remote = this.remote;
    if (!remote || remote.stopped) return;
    this.remote = Object.freeze({...remote, stopped: true, paused: true}); this.publish();
    // A refused stop leaves it playing there: the bar shows it playing again.
    void this.handoff.stop().then(stopped => {if (!stopped && this.remote?.sessionId === remote.sessionId) {this.remote = remote; this.publish();}});
  };
  dismiss = (): void => {this.remote = undefined; this.handoff.release(); this.publish();};

  dispose(): void { this.poll?.stop(); this.poll = undefined; this.detach?.(); this.listeners.clear(); this.flight = undefined; this.handoff.dispose(); }
}

/** Signing a television in with the code it shows. The phone only reads the pending
 * request and records its own decision; the TV collects its session in its own request. */
export function watchLinkTv(api: WatchReceiverApi): PlayOnLinkPort {
  return receiverLinkOperations(api);
}
