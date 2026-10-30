import {routeBody as hostedRouteBody} from "../../contracts/src/hosted/routes.ts";
import {routePath as hostedRoutePath} from "../../contracts/src/hosted/routes.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {randomId} from './random-id.ts';
import { ApiError, HttpLocalApi, type HostedSession } from "./index.ts";
import {hostedGate, type HostedGate, type HostedRequestKind} from './hosted-gate.ts';
import {parseAccountSession} from './hosted-security.ts';
export type DeviceAuthorization = {
  deviceCode: string;
  userCode: string;
  verificationUri: string;
  verificationUriComplete: string;
  expiresIn: number;
  expiresAt: string;
  interval: number;
};
export interface DeviceAuthorizationApi {
  readonly gate?: HostedGate;
  create(
    requestId: string,
    deviceName: string,
    platform: string,
    appVersion: string,
    signal?: AbortSignal,
    kind?: HostedRequestKind
  ): Promise<DeviceAuthorization>;
  poll(deviceCode: string, signal?: AbortSignal): Promise<HostedSession>;
  cancel(deviceCode: string, kind?: HostedRequestKind): Promise<void>;
  revoke(session: HostedSession): Promise<void>;
}
export class HttpDeviceAuthorizationApi implements DeviceAuthorizationApi {
  readonly gate: HostedGate;
  private http: HttpLocalApi;
  private baseUrl: string;
  constructor(baseUrl: string) {
    this.baseUrl = baseUrl;
    this.http = new HttpLocalApi(baseUrl);
    this.gate = hostedGate(baseUrl);
  }
  create(
    requestId: string,
    deviceName: string,
    platform: string,
    appVersion: string,
    signal?: AbortSignal,
    kind: HostedRequestKind = 'interactive'
  ) {
    return this.gate.run(kind, () => this.http.request<DeviceAuthorization>(
      hostedRoutePath("device_authorization_create"),
      "POST",
      hostedRouteBody("device_authorization_create", { requestId, deviceName, platform, appVersion }),
      signal
    ));
  }
  poll(deviceCode: string, signal?: AbortSignal) {
    return this.gate.run('automatic', () => this.http.request<unknown>(
      hostedRoutePath("device_authorization_token"),
      "POST",
      hostedRouteBody("device_authorization_token", { deviceCode,sessionMode:'native' }),
      signal
    ).then(value=>parseAccountSession(value,'native')), undefined, ['authorization_pending', 'slow_down']);
  }
  cancel(deviceCode: string, kind: HostedRequestKind = 'interactive') {
    return this.gate.run(kind, () => this.http.request<void>(hostedRoutePath("device_authorization_cancel"), "POST", hostedRouteBody("device_authorization_cancel", {
      deviceCode,
    })));
  }
  revoke(session: HostedSession) {
    return this.gate.run('automatic', () => new HttpLocalApi(this.baseUrl, session.accessToken).request<void>(
      routePath("delete_sessions_current"),
      "DELETE"
    ));
  }
}
export type DeviceAuthorizationSnapshot = {
  phase: "idle" | "creating" | "waiting" | "approved" | "error";
  userCode?: string;
  verificationUri?: string;
  verificationUriComplete?: string;
  expiresAt?: string;
  error?: string;
  session?: HostedSession;
};
/** Device secrets never leave this owner; presentation receives only public code/URI. */
export class DeviceAuthorizationService {
  private state: DeviceAuthorizationSnapshot = { phase: "idle" };
  private listeners = new Set<() => void>();
  private generation = 0;
  private secret?: string;
  private timer?: ReturnType<typeof setTimeout>;
  private controller?: AbortController;
  private interval = 5000;
  private expires = 0;
  private retryAt = 0;
  private failures = 0;
  private api: DeviceAuthorizationApi;
  private timing: { minimumPollMs: number; requestTimeoutMs: number };
  private requestId: () => Promise<string>;
  constructor(
    api: DeviceAuthorizationApi,
    timing = { minimumPollMs: 5000, requestTimeoutMs: 15000 },
    requestId: () => Promise<string> = async () =>
      randomId()
  ) {
    this.api = api;
    this.timing = timing;
    this.requestId = requestId;
  }
  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };
  private update(value: DeviceAuthorizationSnapshot) {
    this.state = value;
    this.listeners.forEach((fn) => fn());
  }
  private async bounded<T>(
    work: (signal: AbortSignal) => Promise<T>
  ): Promise<T> {
    const c = new AbortController();
    this.controller = c;
    let timer: ReturnType<typeof setTimeout>;
    try {
      return await Promise.race([
        work(c.signal),
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => {
            c.abort();
            reject(new Error("The activation request timed out."));
          }, this.timing.requestTimeoutMs);
        }),
      ]);
    } finally {
      clearTimeout(timer!);
      if (this.controller === c) this.controller = undefined;
    }
  }
  async start(deviceName: string, platform: string, appVersion = "0.1.0") {
    this.cancel();
    const generation = this.generation;
    this.update({ phase: "creating" });
    try {
      const requestId = await this.requestId();
      if (generation !== this.generation) return;
      const create = (kind: HostedRequestKind) =>
        this.bounded((signal) =>
          this.api
            .create(requestId, deviceName, platform, appVersion, signal, kind)
            .then((value) => {
              if (generation !== this.generation)
                void this.api.cancel(value.deviceCode, 'automatic').catch(() => {});
              return value;
            })
        );
      let created: DeviceAuthorization;
      for (let attempt = 0; ; attempt++) {
        try {
          created = await create(attempt === 0 ? 'interactive' : 'automatic');
          break;
        } catch (e) {
          if (generation !== this.generation) return;
          if (e instanceof ApiError && !e.retryable && e.status !== 429) throw e;
          if (!this.api.gate && attempt >= 1) throw e;
          if (this.api.gate) {
            const wait = Math.max(Math.min(30000, 5000 * 2 ** Math.min(attempt, 3)), this.api.gate.blockedFor(), e instanceof ApiError ? (e.retryAfterSeconds ?? 0) * 1000 : 0);
            if (!await this.waitForRetry(wait, generation)) return;
          }
        }
      }
      if (generation !== this.generation) return;
      this.secret = created.deviceCode;
      this.failures = 0;
      this.retryAt = 0;
      this.interval = Math.max(
        this.timing.minimumPollMs,
        created.interval * 1000
      );
      this.expires = Date.parse(created.expiresAt);
      if (!Number.isFinite(this.expires) || this.expires <= Date.now())
        throw new Error("This activation code has expired.");
      this.update({
        phase: "waiting",
        userCode: created.userCode,
        verificationUri: created.verificationUri,
        verificationUriComplete: created.verificationUriComplete,
        expiresAt: created.expiresAt,
      });
      this.schedule(generation);
    } catch (e) {
      if (generation === this.generation) {
        ++this.generation;
        const secret = this.secret;
        this.secret = undefined;
        if (secret) void this.api.cancel(secret, 'automatic').catch(() => {});
        this.update({
          phase: "error",
          error: e instanceof Error ? e.message : "Activation is unavailable.",
        });
      }
    }
  }
  private async waitForRetry(delay: number, generation: number): Promise<boolean> {
    const controller = new AbortController();
    this.controller = controller;
    try {
      await new Promise<void>(resolve => {
        const timer = setTimeout(resolve, delay + Math.floor(Math.random() * 1000));
        controller.signal.addEventListener('abort', () => {clearTimeout(timer); resolve();}, {once: true});
      });
      return generation === this.generation;
    } finally {if (this.controller === controller) this.controller = undefined;}
  }
  private schedule(generation: number) {
    const gateWait = this.api.gate?.blockedFor() ?? 0;
    const delay = Math.max(this.interval, this.retryAt - Date.now(), gateWait > 0 ? gateWait + Math.floor(Math.random() * 1000) : 0);
    this.timer = setTimeout(
      () => void this.poll(generation),
      Math.min(delay, Math.max(0, this.expires - Date.now()))
    );
  }
  private async poll(generation: number) {
    if (generation !== this.generation || !this.secret) return;
    if (Date.now() >= this.expires) {
      this.cancel('automatic');
      this.update({
        phase: "error",
        error: "This activation code has expired. Generate a new code.",
      });
      return;
    }
    try {
      const session = await this.bounded((signal) =>
        this.api.poll(this.secret!, signal).then((value) => {
          if (generation !== this.generation)
            void this.api.revoke(value).catch(() => {});
          return value;
        })
      );
      if (generation !== this.generation) return;
      this.update({ phase: "approved", session });
    } catch (e) {
      if (generation !== this.generation) return;
      if (
        e instanceof ApiError &&
        ["access_denied", "expired_token", "invalid_grant"].includes(e.code)
      ) {
        this.cancel('automatic');
        this.update({ phase: "error", error: e.message });
        return;
      }
      const protocol = e instanceof ApiError && ['authorization_pending', 'slow_down'].includes(e.code);
      if (protocol) this.failures = 0;
      if (e instanceof ApiError && e.code === "slow_down")
        this.interval = Math.max(this.interval, Math.min(30000, this.interval + 5000));
      const step = protocol ? 0 : Math.min(30000, 5000 * 2 ** Math.min(3, this.failures++));
      const retry = protocol ? 0 : Math.round(step < 30000 ? step * (1 + Math.random() * 0.2) : step * (1 - Math.random() * 0.2));
      this.retryAt = Date.now() + Math.max(retry, e instanceof ApiError ? (e.retryAfterSeconds ?? 0) * 1000 : 0);
      if (
        e instanceof ApiError &&
        !["authorization_pending", "slow_down"].includes(e.code) &&
        !e.retryable &&
        e.status !== 429
      ) {
        this.cancel('automatic');
        this.update({ phase: "error", error: e.message });
        return;
      }
      this.schedule(generation);
    }
  }
  accept(session: HostedSession): boolean {
    if (this.state.phase !== "approved" || this.state.session !== session) return false;
    this.secret = undefined;
    return true;
  }
  cancel(kind: HostedRequestKind = 'interactive') {
    ++this.generation;
    clearTimeout(this.timer);
    this.controller?.abort();
    const secret = this.secret;
    this.secret = undefined;
    if (secret) void this.api.cancel(secret, kind).catch(() => {});
    this.update({ phase: "idle" });
  }
}
