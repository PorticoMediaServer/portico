import {Share} from 'react-native';
import {buildServerLink, type ServerDestination} from '../../../../../packages/client-core/src/server-links';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {IconName} from '../../kit/Icon';
import {copyText} from '../../platform/clipboard';

export type ShareLinkAction = Readonly<{id: 'share-link' | 'copy-link'; label: string; icon: IconName; onSelect: () => void}>;
export type ShareLinkFeedback = Readonly<{notice?: (message: string) => void; report?: (error: unknown) => void}>;

/** O-162: Share (the system share sheet) and Copy link for one destination on one server. Both use
 * client-core's one builder, so the app, the web and a pasted link agree; no timestamp is ever
 * written. Nothing is offered when the builder refuses the destination. */
export function shareLinkActions(serverId: string | undefined, destination: ServerDestination, params: Readonly<Record<string, string>>, feedback: ShareLinkFeedback = {}): readonly ShareLinkAction[] {
  const link = serverId ? buildServerLink({serverId, destination, params}) : undefined;
  if (!link) return [];
  const t = defaultI18n.t;
  return Object.freeze([
    {id: 'share-link', label: t('next.overlays.share'), icon: 'i-share', onSelect: () => {void Share.share({message: link, url: link}).catch(error => feedback.report?.(error));}},
    {id: 'copy-link', label: t('next.overlays.copyLink'), icon: 'i-copy', onSelect: () => {
      void copyText(link).then(() => feedback.notice?.(t('next.overlays.linkCopied')), () => feedback.notice?.(t('next.overlays.copyLinkFailed')));
    }},
  ]);
}
