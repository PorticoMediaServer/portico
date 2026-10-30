/**
 * Words for remote storage on the Storage & backups page: what a mount or a share is doing, in
 * the viewer's language. A state this client has no word for reads as unavailable, never as its
 * code.
 */
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;

const MOUNT_STATES: Readonly<Record<string, MessageId>> = {
  mounted: 'server.storage.state.mounted', stopped: 'server.storage.state.stopped', starting: 'server.storage.state.starting', stopping: 'server.storage.state.stopping',
  failed: 'server.storage.state.failed', quarantined: 'server.storage.state.quarantined', unavailable: 'server.storage.state.unavailable',
};
export const mountStateId = (state: string): MessageId => MOUNT_STATES[state] ?? 'server.storage.state.unavailable';

const SOURCE_STATES: Readonly<Record<string, MessageId>> = {
  ready: 'server.storage.state.ready', healthy: 'server.storage.state.ready', checking: 'server.storage.state.checking', pending: 'server.storage.state.checking',
  unreachable: 'server.storage.state.unreachable', degraded: 'server.storage.state.unreachable', credentials_required: 'server.storage.state.credentials', unauthorized: 'server.storage.state.credentials',
};
/** A share's state, or what is wrong with it: the server's own code when the catalogue has a sentence for it (`server.storage.problem.<code>`), a general word otherwise. */
export function sourceStateText(t: Translate, has: (id: string) => boolean, source: Readonly<{state: string; errorCode?: string}>): string {
  if (source.errorCode) {
    const own = `server.storage.problem.${source.errorCode}`;
    return has(own) ? t(own as MessageId) : t(SOURCE_STATES[source.errorCode] ?? 'server.storage.state.problem');
  }
  return t(SOURCE_STATES[source.state] ?? 'server.storage.state.unavailable');
}
