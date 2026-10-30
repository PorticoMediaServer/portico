import {HandoffController} from '@core/remote-playback.ts';
import {sameFence} from '../../../../packages/player-core/src/index.ts';
import {defaultI18n, type MessageId, type MessageValues} from '@i18n';
import type {HttpLocalApi} from '@core/index.ts';
import type {WebShellPlaybackRuntime} from '../../platform/playback';
import type {PlayOnLinkPort, PlayOnLinkPreview, PlayOnReceiversPort, PlayOnReceiver} from '../../platform/playback/play-on';
import type {PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {installationIdentity} from '@core/installation.ts';
import {castDisplayName} from '../../platform/cast';
import {routeBody, routePath} from '../../../../packages/contracts/src/routes.ts';

const DIRECTORY_PATH = '/v1/receivers';
const ROLLBACK = /\/v1\/handoffs\/[^/]+\/rollback$/;
const PLATFORMS: Readonly<Record<string, string>> = {appletv: 'Apple TV', tvos: 'Apple TV', ios: 'iPhone', ipados: 'iPad', androidtv: 'Android TV', firetv: 'Fire TV', android: 'Android', web: 'Web', windows: 'Windows', macos: 'Mac', linux: 'Linux'};

export type WatchReceiverApi = Pick<HttpLocalApi, 'request'>;

/** The server's receiver directory and one proven handoff. Portico televisions, not a
 * guessed LAN scan: only an online, active receiver the viewer may send to is listed. */
export class WatchReceivers implements PlayOnReceiversPort {
  private readonly handoff: HandoffController;
  private readonly deviceId: string;
  private state: ReturnType<PlayOnReceiversPort['getSnapshot']> = Object.freeze({phase: 'idle' as const, items: Object.freeze([]) as readonly PlayOnReceiver[]});
  private readonly listeners = new Set<() => void>();
  // One synchronous send flight with its own captured run. Every handoff request
  // checks that exact flight before IO, so a run replaced in this browser can never
  // continue to grant, hand off or commit, and no live request is ever unguarded.
  private flight?: Readonly<{guard: Readonly<{fence: PlaybackFence; sessionId: string; itemId: string; authority: unknown}>}>;

  constructor(private readonly options: {api: WatchReceiverApi; runtime: WebShellPlaybackRuntime; /** The shell's own catalogue translator, when the host has one. */ t?: (id: MessageId, values?: MessageValues) => string; isCurrent(): boolean; reportError(error: unknown): void}) {
    const identity = installationIdentity();
    // Without the persisted installation, this browser cannot prove who is asking;
    // the rows stay hidden from a send rather than inventing a device identity.
    this.deviceId = identity?.installationId ?? '';
    this.handoff = new HandoffController({
      api: {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
        const run = (): void => {
          const flight = this.flight;
          if (!flight || !this.options.isCurrent()) throw new DOMException('Playback run retired', 'AbortError');
          const live = this.options.runtime.getSnapshot();
          if (live.disposed || live.authority !== flight.guard.authority || !live.fence || !sameFence(live.fence, flight.guard.fence)) throw new DOMException('Playback run retired', 'AbortError');
        };
        // The directory populates before any send, and an issued handoff may still be
        // rolled back after its flight retired. Neither grants, creates or commits.
        if (path === DIRECTORY_PATH) {
          if (!this.options.isCurrent()) throw new DOMException('Playback run retired', 'AbortError');
          return options.api.request<T>(path, method, body, signal);
        }
        if (ROLLBACK.test(path)) {
          if (!this.options.isCurrent()) throw new DOMException('Playback run retired', 'AbortError');
          return options.api.request<T>(path, method, body, signal);
        }
        // Grant, create, poll and commit belong to one captured flight only.
        run();
        const flight = this.flight;
        const value = await options.api.request<T>(path, method, body, signal);
        // The same flight and the same run must still be current before any result is
        // published or committed downstream.
        if (!flight || this.flight !== flight) throw new DOMException('Playback run retired', 'AbortError');
        run();
        return value;
      }},
      device: {deviceId: this.deviceId, displayName: castDisplayName() || identity?.name || 'Web browser'},
      requestId: () => crypto.randomUUID(),
    });
    this.handoff.subscribe(this.publish);
    this.detach = options.runtime.subscribe(this.retireOnReplacement);
  }

  private detach?: () => void;
  /** A settled replacement retires the captured run and the controller's own rollback runs. */
  private retireOnReplacement = (): void => {
    const flight = this.flight;
    if (!flight) return;
    const live = this.options.runtime.getSnapshot();
    if (!live.disposed && live.authority === flight.guard.authority && live.fence && sameFence(live.fence, flight.guard.fence)) return;
    if (this.flight === flight) this.flight = undefined;
    this.handoff.cancel();
  };

  private describe(platform: string): string {
    const label = PLATFORMS[platform.toLowerCase()], t = this.options.t ?? defaultI18n.t;
    return label ? t('next.cast.receiverOn', {platform: label}) : t('next.cast.receiverOnly');
  }

  private publish = (): void => {
    const directory = this.handoff.getSnapshot();
    const items = Object.freeze(directory.receivers
      .filter(receiver => receiver.state === 'active' && receiver.presence === 'online')
      .map(receiver => Object.freeze({id: receiver.id, name: receiver.displayName, detail: this.describe(receiver.platform)})));
    this.state = Object.freeze({phase: directory.directoryPhase, items});
    for (const listener of [...this.listeners]) listener();
  };

  /** Reproject confirmed rows only; no handoff, directory read or target replacement. */
  refreshPresentation = (): void => {if (this.options.isCurrent()) this.publish();};

  getSnapshot = (): ReturnType<PlayOnReceiversPort['getSnapshot']> => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  refresh = (): void => {
    if (!this.options.isCurrent()) return;
    void this.handoff.refresh().then(this.publish).catch(this.options.reportError);
  };

  /** True only once the television proved it is playing. The local player stops after
   * that proof, and only while the same fenced run is still the one on screen. */
  async send(id: string): Promise<boolean> {
    if (!this.deviceId) return false;
    const snapshot = this.options.runtime.getSnapshot();
    const fence = snapshot.fence, session = snapshot.session, presentation = snapshot.presentation;
    if (!this.options.isCurrent() || snapshot.disposed || !fence || fence.kind !== 'queue' || !session || !presentation
      || session.id !== fence.sessionId || session.itemId !== fence.itemId || !session.queue) return false;
    const target = this.handoff.getSnapshot().receivers.find(receiver => receiver.id === id && receiver.state === 'active' && receiver.presence === 'online');
    if (!target) return false;
    // Claim synchronously: one send at a time, and it owns its captured run.
    if (this.flight) return false;
    const guard = {fence, sessionId: session.id, itemId: session.itemId, authority: snapshot.authority};
    const flight = {guard} as const;
    this.flight = flight;
    let taken = false, owned = false;
    try {
      taken = await this.handoff.send(target, {playbackId: session.id, positionSeconds: presentation.positionSeconds});
    } finally {
      // Clear only this send's own flight; a successor can never inherit or lose its guard,
      // and a flight retired mid-request must not publish its result.
      if (this.flight === flight) { this.flight = undefined; owned = true; }
    }
    if (!taken || !owned) return false;
    const live = this.options.runtime.getSnapshot();
    if (this.options.isCurrent() && !live.disposed && live.authority === snapshot.authority && sameFence(live.fence, fence)) await this.options.runtime.stop(fence);
    return true;
  }

  dispose(): void { this.detach?.(); this.listeners.clear(); this.flight = undefined; this.handoff.cancel(); this.handoff.dispose(); }
}

/** Signing a television in with the code it shows. The phone only reads the pending
 * request and records its own decision; the TV collects its session in its own request. */
export function watchLinkTv(api: WatchReceiverApi): PlayOnLinkPort {
  const text = (value: unknown, max: number): string | undefined => typeof value === 'string' && value.length > 0 && value.length <= max ? value : undefined;
  return {
    async preview(code: string, life?: Readonly<{signal: AbortSignal}>): Promise<PlayOnLinkPreview> {
      const raw = await api.request<{requestId?: unknown; userCode?: unknown; deviceName?: unknown; platform?: unknown}>(
        routePath('post_quick_connect_review'), 'POST', routeBody('post_quick_connect_review', {userCode: code.trim().toUpperCase()}), life?.signal);
      const requestId = text(raw?.requestId, 128), userCode = text(raw?.userCode, 32), deviceName = text(raw?.deviceName, 64);
      if (!requestId || !userCode || !deviceName) throw new Error('quick_connect_unreadable');
      const platform = text(raw?.platform, 32);
      return Object.freeze({requestId, userCode, deviceName, ...(platform ? {platform} : {})});
    },
    async decide(requestId: string, code: string, decision: 'approve' | 'deny', life?: Readonly<{signal: AbortSignal}>): Promise<void> {
      await api.request(routePath('post_quick_connect_decision'), 'POST',
        routeBody('post_quick_connect_decision', {requestId, userCode: code.trim().toUpperCase(), decision}), life?.signal);
    },
  };
}
