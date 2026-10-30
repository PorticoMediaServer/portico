import type {ChannelApi} from './channel-guide.ts';
import {parseEnvelope, parseChannelMapPage} from './administration.ts';
import {encodedRoutePath} from '../../contracts/src/route-paths.ts';

/** A bounded exact lookup; never walk the owner's entire channel map. No automatic
 * rebase: another settings edit must win over a stale hide or Undo. */
export async function setServerChannelHidden(api: ChannelApi, serverId: string, sourceId: string, channelId: string,
  hidden: boolean, operationId: () => string, signal: AbortSignal, isCurrent: () => boolean,
  expected?: Readonly<{revision: number; hidden: boolean}>): Promise<Readonly<{revision: number; hidden: boolean; previousHidden: boolean}>> {
  const assert = () => {signal.throwIfAborted();if (!isCurrent()) throw Object.assign(new Error('channel_scope_ended'), {name: 'AbortError'});};
  const path = encodedRoutePath('get_admin_live_sources_id_channel_map', {id: encodeURIComponent(sourceId)});
  assert();
  const page = parseEnvelope(await api.request(path + '?' + new URLSearchParams({channelId, limit: '1'}), 'GET', undefined, signal), serverId, parseChannelMapPage).result;
  assert();
  const row = page.items[0];
  if (page.sourceId !== sourceId || page.items.length !== 1 || row?.channelId !== channelId || page.revision === undefined || row.overrideNumber === undefined) throw new Error('invalid_channel_map_witness');
  if (expected && (page.revision !== expected.revision || row.hidden !== expected.hidden)) throw Object.assign(new Error('channel_map_changed'), {status: 409});
  const saved = parseEnvelope(await api.request(path, 'PUT', {expectedRevision: page.revision, operationId: operationId(), overrides: [{channelId, number: row.overrideNumber, guideChannelId: row.guideChannelId, hidden}]}, signal), serverId, parseChannelMapPage).result;
  assert();
  if (saved.sourceId !== sourceId || saved.revision !== page.revision + 1) throw new Error('invalid_channel_map_receipt');
  return {revision: saved.revision, hidden, previousHidden: row.hidden};
}
