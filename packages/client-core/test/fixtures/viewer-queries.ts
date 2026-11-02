/**
 * Tests give an owner that reads through the viewer's cache (DetailService and its users) a cache
 * of its own, built from the test's own fake requester and scope, so every read still reaches the
 * fake. Nothing is ever collected on a timer, so a test that does not dispose it cannot hang.
 */
import {FreshnessHub} from '../../src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries, type ViewerRequester} from '../../src/query/viewer-queries.ts';

type Scope = Readonly<{serverId: string; viewerId?: string; authority?: string; accountId?: string; profileId?: string}>;

export function testQueries(api: ViewerRequester, scope: Scope): ViewerQueries {
  const viewerId = scope.viewerId ?? JSON.stringify([scope.authority, scope.accountId, scope.profileId]);
  return createViewerQueries({scope: {serverId: scope.serverId, viewerId}, hub: new FreshnessHub(), api, gcTimeMs: Infinity});
}

/** The same options with a cache over their own `api` and `scope` (or `context.scope`). */
export function withQueries<T extends {api: ViewerRequester}>(options: T): T & {queries: ViewerQueries} {
  const o = options as T & {scope?: Scope; context?: {scope?: Scope}; queries?: ViewerQueries};
  if (o.queries) return o as T & {queries: ViewerQueries};
  const scope = o.scope ?? o.context?.scope;
  if (!scope) throw new Error('withQueries: no scope');
  const queries = testQueries(o.api, scope);
  // In place, so a test that changes its options later still reaches the owner; a frozen input is copied with its getters.
  if (Object.isExtensible(options)) return Object.assign(options, {queries});
  return Object.create(Object.getPrototypeOf(options), {...Object.getOwnPropertyDescriptors(options), queries: {value: queries, enumerable: true}});
}
