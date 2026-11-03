import {FreshnessHub} from '@core/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries, type ViewerRequester} from '@core/query/viewer-queries.ts';

/** A viewer's cache over a test context's own API, so every read it makes is counted there. */
export function testViewerQueries(context: Readonly<{api: unknown; scope: Readonly<{serverId: string; viewerId: string}>}>): ViewerQueries {
  return createViewerQueries({scope: context.scope, hub: new FreshnessHub(), api: context.api as ViewerRequester});
}
