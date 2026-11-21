import {remoteRefusal, type RemoteFolder} from '../../../../../../packages/client-core/src/remote-storage';
import type {defaultI18n, MessageId} from '../../../../../../packages/i18n/src';

type Translate = typeof defaultI18n.t;
/** A folder chosen on a source, for a library. */
export type ChosenSourceFolder = Pick<RemoteFolder, 'name' | 'path'>;

/** The server's refusal in the owner's words; a step this server does not offer yet says so plainly. */
export function remoteSourceError(t: Translate, error: unknown): string {
  const refusal = remoteRefusal(error);
  if ('notOnServer' in refusal) return t('next.remoteSources.notOnServerYet');
  if ('sentence' in refusal) return refusal.sentence;
  if ('field' in refusal) return t(`next.remoteSources.field.${refusal.field}` as MessageId);
  if (refusal.other === 'unauthorized') return t('next.remoteSources.error.signIn');
  if (refusal.other === 'forbidden' || refusal.other === 'not_permitted') return t('next.remoteSources.error.forbidden');
  return t('next.remoteSources.error.generic');
}
export const stateWords: Record<string, MessageId> = {
  connected: 'next.remoteSources.state.ready', setup: 'next.remoteSources.state.setup', unreachable: 'next.remoteSources.state.unreachable',
  sign_in: 'next.remoteSources.state.credentials', waiting: 'next.remoteSources.state.waiting', limited: 'next.remoteSources.state.limited',
  needs_attention: 'next.remoteSources.state.attention',
};
