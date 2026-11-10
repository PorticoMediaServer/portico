/** Who is signed in to the selected server, as a screen subscribes to it. */
import {parseLocalSession} from './local-session.ts';
import {type LocalSession} from './http.ts';
export type ViewerSnapshot = {
  generation: number;
  phase: "signedOut" | "ready";
  serverUrl?: string;
  session?: LocalSession;
  internetReachable: boolean | null;
  hostedReachable: boolean | null;
};
export class ViewerService {
  private snapshot: ViewerSnapshot = {
    generation: 0,
    phase: "signedOut",
    internetReachable: null,
    hostedReachable: null,
  };
  private listeners = new Set<() => void>();
  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private publish(value: ViewerSnapshot) {
    this.snapshot = Object.freeze(value);
    for (const l of this.listeners) l();
  }
  /** `stored`: a sign-in this device saved earlier, shown before it is verified. Its access token
   * may have expired since, which the restore renews; it is never a reason to refuse it here. */
  select(serverUrl: string, session: LocalSession, options: {stored?: boolean} = {}) {
    const {refreshToken: _refresh, ...checked}=parseLocalSession(session,{},{stored:options.stored});
    this.publish({
      ...this.snapshot,
      generation: this.snapshot.generation + 1,
      phase: "ready",
      serverUrl,
      session:checked,
    });
  }
  /** The same viewer with a renewed access token: no new generation, so nothing viewer-scoped restarts. */
  rotate(session: LocalSession): boolean {
    const current = this.snapshot.session;
    if (!current || current.sessionFamilyId !== session.sessionFamilyId || (['accountId','profileId','serverId','authority'] as const).some(k => current.viewer[k] !== session.viewer[k])) return false;
    if (BigInt(session.tokenGeneration) < BigInt(current.tokenGeneration)) return false;
    const {refreshToken: _refresh, ...checked} = parseLocalSession(session);
    this.publish({ ...this.snapshot, session: checked });
    return true;
  }
  clear() {
    this.publish({
      generation: this.snapshot.generation + 1,
      phase: "signedOut",
      internetReachable: this.snapshot.internetReachable,
      hostedReachable: this.snapshot.hostedReachable,
    });
  }
  setConnectivity(
    internetReachable: boolean | null,
    hostedReachable: boolean | null
  ) {
    this.publish({ ...this.snapshot, internetReachable, hostedReachable });
  }
  isCurrent(generation: number) {
    return this.snapshot.generation === generation;
  }
}
