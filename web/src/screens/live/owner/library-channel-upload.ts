import {encodedRoutePath} from '../../../../../packages/contracts/src/routes.ts';
import type {ShellAssemblyContext} from '../../../shell/bootstrap';
import {errorFacts, terminal} from './lifetime';

/** Fixed-route multipart port for the owned Library Channels page. The parent
 * owns disposal and checks its complete captured owner identity on every call.
 * This port does not own channel models, receipts, retries or config revisions. */
export function libraryChannelUpload(context: ShellAssemblyContext, isCurrent: () => boolean, reportError: (error: unknown) => void) {
  const original = context.session(), captured = Object.freeze({...original.viewer});
  const admitted = captured.role === 'owner' && context.facts.identity.primaryProfile === true && context.facts.identity.capabilities?.manageServer === true;
  const current = () => {
    const viewer = context.session().viewer;
    return admitted && isCurrent() && context.isCurrent() && context.session().sessionFamilyId === original.sessionFamilyId && context.session().deviceId === original.deviceId && viewer.role === 'owner' && viewer.serverId === captured.serverId && viewer.authority === captured.authority && viewer.accountId === captured.accountId && viewer.profileId === captured.profileId && context.facts.identity.primaryProfile === true && context.facts.identity.capabilities?.manageServer === true;
  };
  return async (channelId: string, file: File, operationId: string, signal: AbortSignal): Promise<unknown> => {
    if (!current() || signal.aborted) throw {code: 'cancelled'};
    if (!/^[A-Za-z0-9_-]{1,128}$/.test(channelId) || !/^[a-f0-9]{48}$/.test(operationId) || file.size <= 0 || file.size > 10 * 1024 * 1024 || !['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) throw {code: 'invalid_request'};
    const path = encodedRoutePath('post_admin_library_channels_id_logo', {id: encodeURIComponent(channelId)});
    const body = new FormData();body.append('file', file);body.append('operationId', operationId);
    try {
      const token = context.session().accessToken;
      if (!current() || signal.aborted) throw {code: 'cancelled'};
      const response = await context.api.routeFetch(context.api.baseUrl + path, {method: 'POST', signal, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', headers: {Authorization: 'Bearer ' + token, Accept: 'application/json'}, body});
      if (!current() || signal.aborted) {await response.body?.cancel();throw {code: 'cancelled'};}
      if (!response.ok) {await response.body?.cancel();throw {code: response.status === 401 ? 'unauthorized' : response.status === 403 ? 'not_permitted' : 'write_failed', status: response.status};}
      const result: unknown = await response.json();
      if (!current() || signal.aborted) throw {code: 'cancelled'};
      return result;
    } catch (error) {
      const facts = errorFacts(error);
      if (terminal(error)) reportError(facts);
      throw facts;
    }
  };
}
