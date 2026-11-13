import {uploadOwnerChannelLogo,type LibraryChannel} from '@core/server-admin/owner-library-channels.ts';
import type {ShellAssemblyContext} from '../../../shell/bootstrap';
import {errorFacts, terminal} from './lifetime';
/** Fixed-route raw-picture port for the owned Library Channels page. The parent
 * owns disposal and checks its complete captured owner identity on every call.
 * This port captures authentication; the page owns readback after an unknown result. */
export function libraryChannelUpload(context: ShellAssemblyContext, isCurrent: () => boolean, reportError: (error: unknown) => void) {
  const original = context.session(), captured = Object.freeze({...original.viewer});
  const admitted = captured.role === 'owner' && context.facts.identity.primaryProfile === true && context.facts.identity.capabilities?.manageServer === true;
  const current = () => {
    const viewer = context.session().viewer;
    return admitted && isCurrent() && context.isCurrent() && context.session().sessionFamilyId === original.sessionFamilyId && context.session().deviceId === original.deviceId && viewer.role === 'owner' && viewer.serverId === captured.serverId && viewer.authority === captured.authority && viewer.accountId === captured.accountId && viewer.profileId === captured.profileId && context.facts.identity.primaryProfile === true && context.facts.identity.capabilities?.manageServer === true;
  };
  return async (channelId: string, file: File, signal: AbortSignal): Promise<LibraryChannel> => {
    if (!current() || signal.aborted) throw {code: 'cancelled'};
    if (!/^[A-Za-z0-9_-]{1,128}$/.test(channelId) || file.size <= 0 || file.size > 10 * 1024 * 1024 || !['image/jpeg', 'image/png', 'image/webp', 'image/gif'].includes(file.type)) throw {code: 'invalid_request'};
    try {
      const token = context.session().accessToken;
      if (!current() || signal.aborted) throw {code: 'cancelled'};
      const response = await uploadOwnerChannelLogo(context.api, channelId, token, file, signal);
      if (!current() || signal.aborted) {await response.body?.cancel();throw {code: 'cancelled'};}
      if (!response.ok) {await response.body?.cancel();throw {code: response.status === 401 ? 'unauthorized' : response.status === 403 ? 'not_permitted' : 'write_failed', status: response.status};}
      const result = await response.json() as LibraryChannel;
      if (!current() || signal.aborted) throw {code: 'cancelled'};
      return result;
    } catch (error) {
      const facts = errorFacts(error);
      if (terminal(error)) reportError(facts);
      throw facts;
    }
  };
}
