import type {ListAdminSessionsResponse} from '../../../../contracts/src/types.ts';
import type {DashboardAlert, DashboardScan, DashboardTasks, NowPlaying} from '../../../../contracts/src/next/api.ts';
import type {V1Http} from '../../playback-v1/http.ts';

type Immutable<T> = T extends readonly (infer V)[] ? readonly Immutable<V>[] : T extends object ? {readonly [K in keyof T]: Immutable<T[K]>} : T;
/** The rebuilt server's owner summary, `GET /v1/admin/activity` (server-next ActivitySummary). */
export type OwnerActivitySummary = Immutable<{
  observedAt: string; serverName: string;
  // `problem` and `bufferingSince` arrive with server-next #3055, ahead of the generated contract's next regeneration.
  streams: {count: number; converting: number; items: (NowPlaying & {problem?: boolean; bufferingSince?: string})[]};
  scans: DashboardScan[]; tasks: DashboardTasks; alerts: DashboardAlert[]; lastScanAt?: string;
}>;
export type ActivitySummarySession = OwnerActivitySummary['streams']['items'][number];
export type ActivitySummaryScan = OwnerActivitySummary['scans'][number];
export type ActivitySummaryTask = OwnerActivitySummary['tasks']['items'][number];
export type ActivitySummaryAlert = OwnerActivitySummary['alerts'][number];

/** No credentials. The shell advances authorizationEpoch whenever its authority is replaced. */
export type ActivitySummaryScope = Readonly<{
  serverId: string;
  accountId: string;
  profileId: string;
  authority: string;
  role: string;
  sessionFamilyId: string;
  authorizationEpoch: string;
}>;
export type ActivitySummarySource = Readonly<{http: V1Http; scope: ActivitySummaryScope}>;
/** The owning session runtime must notify synchronously on sign-out, demotion or scope replacement. */
export type ActivitySummarySourceStore = Readonly<{
  getSnapshot(): ActivitySummarySource | null;
  subscribe(listener: () => void): () => void;
}>;
export type ActivitySummaryErrorCode = 'request_failed' | 'request_timeout' | 'invalid_activity_summary' | 'permission_changed' | 'not_permitted';
export type ActivitySummarySnapshot = Readonly<{
  phase: 'unavailable' | 'idle' | 'loading' | 'ready' | 'error';
  document?: OwnerActivitySummary;
  error?: Readonly<{code: ActivitySummaryErrorCode; retryAfterMs?: number}>;
}>;
export type ActivitySummaryTimers = Readonly<{
  now(): number;
  setTimer(fn: () => void, ms: number): unknown;
  clearTimer(timer: unknown): void;
}>;

export type DashboardSessionsPage=Immutable<ListAdminSessionsResponse>;
export type DashboardSessionsSnapshot=Readonly<{phase:'unavailable'|'idle'|'loading'|'ready'|'error';document?:DashboardSessionsPage;error?:Readonly<{code:ActivitySummaryErrorCode;retryAfterMs?:number}>}>;
