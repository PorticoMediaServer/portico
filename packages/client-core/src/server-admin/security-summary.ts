import {routePath} from '../../../contracts/src/routes.ts';

export type SecurityCounts = Readonly<{refusedConnections: number; failedSignIns: number; blockedSources: number; newRemoteDevices: number}>;
/** One source the server refuses right now, as its /24 or /48 network; `id` is the unblock handle. */
export type SecurityBlock = Readonly<{id: string; network: string; failedAttempts: number; blockedUntil: string}>;
export type SecuritySummary = Readonly<{last24Hours: SecurityCounts; last7Days: SecurityCounts; blocks: readonly SecurityBlock[]}>;
export type SecuritySummarySnapshot = Readonly<{loading: boolean; summary?: SecuritySummary; failed: boolean; unblocking?: string; unblocked?: string}>;
export type SecuritySummaryApi = Readonly<{request: <T>(path: string, method: string, body?: unknown, signal?: AbortSignal) => Promise<T>}>;

const count = (value: unknown) => typeof value === 'number' && Number.isInteger(value) && value >= 0 ? value : undefined;

function parseCounts(raw: unknown): SecurityCounts | undefined {
  if (!raw || typeof raw !== 'object') return undefined;
  const v = raw as Record<string, unknown>;
  const refused = count(v.refusedConnections), failed = count(v.failedSignIns), blocked = count(v.blockedSources), devices = count(v.newRemoteDevices);
  if (refused === undefined || failed === undefined || blocked === undefined || devices === undefined) return undefined;
  return Object.freeze({refusedConnections: refused, failedSignIns: failed, blockedSources: blocked, newRemoteDevices: devices});
}

/** The checked summary, or undefined when the reply isn't one. Unreadable blocks are left out. */
export function parseSecuritySummary(raw: unknown): SecuritySummary | undefined {
  if (!raw || typeof raw !== 'object') return undefined;
  const v = raw as {counts?: {last24Hours?: unknown; last7Days?: unknown}; blocks?: unknown};
  const day = parseCounts(v.counts?.last24Hours), week = parseCounts(v.counts?.last7Days);
  if (!day || !week || !Array.isArray(v.blocks)) return undefined;
  const blocks = v.blocks.slice(0, 256).flatMap(item => {
    if (!item || typeof item !== 'object') return [];
    const b = item as Record<string, unknown>;
    const failed = count(b.failedAttempts);
    if (typeof b.id !== 'string' || !b.id || b.id.length > 128 || typeof b.network !== 'string' || !b.network || b.network.length > 64
      || failed === undefined || typeof b.blockedUntil !== 'string' || Number.isNaN(Date.parse(b.blockedUntil))) return [];
    return [Object.freeze({id: b.id, network: b.network, failedAttempts: failed, blockedUntil: b.blockedUntil})];
  });
  return Object.freeze({last24Hours: day, last7Days: week, blocks: Object.freeze(blocks)});
}

/** Settings › Remote access › Security (O-169): what the protection refused and
 * blocked, and one action, Unblock. Owner-only, remote page; web, phone and TV. */
export class SecuritySummarySource {
  private snapshot: SecuritySummarySnapshot = Object.freeze({loading: false, failed: false});
  private readonly listeners = new Set<() => void>();
  private generation = 0;
  constructor(private readonly api: SecuritySummaryApi, private readonly isCurrent: () => boolean) {}
  has = (id: string) => id === 'remote.summary' || id === 'remote.blocked';
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.snapshot;
  private publish(next: Partial<SecuritySummarySnapshot>) {this.snapshot = Object.freeze({...this.snapshot, ...next});for (const listener of this.listeners) listener();}
  load = async () => {
    const generation = ++this.generation;
    this.publish({loading: true, failed: false});
    try {
      const summary = parseSecuritySummary(await this.api.request<unknown>(routePath('get_admin_security_summary'), 'GET'));
      if (generation !== this.generation || !this.isCurrent()) return;
      this.publish(summary ? {loading: false, summary} : {loading: false, failed: true});
    } catch {
      if (generation === this.generation && this.isCurrent()) this.publish({loading: false, failed: true});
    }
  };
  /** Lifts one block at once, then reads the summary again. */
  unblock = async (id: string) => {
    if (this.snapshot.unblocking || !this.isCurrent()) return;
    this.publish({unblocking: id, unblocked: undefined, failed: false});
    try {
      await this.api.request<unknown>(routePath('post_admin_security_unblock', {id}), 'POST', {});
      if (!this.isCurrent()) return;
      this.publish({unblocking: undefined, unblocked: id});
      await this.load();
    } catch {
      if (this.isCurrent()) this.publish({unblocking: undefined, failed: true});
    }
  };
}

type Words = Readonly<{t: (id: never, values?: Record<string, string | number>) => string; time: (value: string) => string; dateTime: (value: string) => string}>;
const K = 'next.settingsSpec.server.remote.groups.2.rows.1';

/** The Security row's one line, from the last 7 days (server-remote.json remote.summary). */
export function securitySummaryLine(summary: SecuritySummary, words: Words): string {
  const week = summary.last7Days;
  return words.t(`${K}.text` as never, {refused: week.refusedConnections, failed: week.failedSignIns, blocked: week.blockedSources});
}

/** A blocked network's line: until when (the time alone when it's today), and the failed sign-ins behind it. */
export function securityBlockLine(block: SecurityBlock, words: Words, now = Date.now()): string {
  const until = new Date(block.blockedUntil), today = new Date(now);
  const sameDay = until.getFullYear() === today.getFullYear() && until.getMonth() === today.getMonth() && until.getDate() === today.getDate();
  return words.t(`${K}.children.0.until` as never, {time: sameDay ? words.time(block.blockedUntil) : words.dateTime(block.blockedUntil), count: block.failedAttempts});
}
