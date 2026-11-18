import {routePath} from "../../../contracts/src/route-paths.ts";
/**
 * Now playing (`GET /v1/admin/sessions`): the active sessions with their delivery decisions. Stopping one is
 * `stopServerStream` (`DELETE /v1/admin/sessions/{id}`, admin/server-now-playing.ts).
 */
import {call, type V1Http} from './http.ts';
import {parseDashboardSessions} from '../admin/activity-summary/parser.ts';
import {parseAdminSession, parsePage} from './types.ts';

export class AdminSessionsClient {
  private http: V1Http;
  constructor(http: V1Http) { this.http = http; }

  async list(cursor?: string, limit = 10, signal?: AbortSignal) {
    const q = new URLSearchParams({limit: String(Math.max(1, Math.min(limit, 200)))});
    if (cursor) q.set('cursor', cursor);
    const raw=(await call(this.http,{method:'GET',path:routePath('list_admin_sessions')+'?'+q,signal})).body;
    const document=parseDashboardSessions(raw);
    return {...parsePage(raw,parseAdminSession),totals:document.totals,scope:document.scope};
  }
}
