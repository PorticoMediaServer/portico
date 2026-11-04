import {Share} from 'react-native';
import {buildServerLink, destinationLocation, type ServerDestination} from '../../../../../packages/client-core/src/server-links';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {IconName} from '../../kit/Icon';
import {copyText} from '../../platform/clipboard';

export type ShareLinkAction = Readonly<{id: 'share-link' | 'copy-link'; label: string; icon: IconName; onSelect: () => void}>;
export type ShareLinkFeedback = Readonly<{notice?: (message: string) => void; report?: (error: unknown) => void}>;
/** The server a link is shared from, captured where the action is drawn: its id, whether people sign
 * in to it with a Portico Account (`hosted`), and its own address as this device reaches it. */
export type NativeShareScope = Readonly<{serverId: string; hosted: boolean; origin?: string}>;

/** O-161/O-162, N5-6: the shareable address of one destination, as web's `shareableLink` builds it. A
 * Portico Account server's is the one canonical `https://web.getportico.tv/s/<serverId>/…` link
 * (client-core's builder), which the apps open too; a local-account server's is its own plain address,
 * where its own web UI answers. Undefined when neither can be built. */
export function nativeShareLink(scope: NativeShareScope | undefined, destination: ServerDestination, params: Readonly<Record<string, string>>): string | undefined {
  if (!scope?.serverId) return undefined;
  if (scope.hosted) return buildServerLink({serverId: scope.serverId, destination, params});
  const own = destinationLocation(destination, params), origin = scope.origin?.replace(/\/+$/, '');
  return own && origin && /^https?:\/\/[^/]+$/.test(origin) ? origin + own.pathname + own.search : undefined;
}

/** O-162: Share (the system share sheet) and Copy link for one destination on one server. No
 * timestamp is ever written. Nothing is offered when no link can be built. */
export function shareLinkActions(scope: NativeShareScope | undefined, destination: ServerDestination, params: Readonly<Record<string, string>>, feedback: ShareLinkFeedback = {}): readonly ShareLinkAction[] {
  const link = nativeShareLink(scope, destination, params);
  if (!link) return [];
  const t = defaultI18n.t;
  return Object.freeze([
    {id: 'share-link', label: t('next.overlays.share'), icon: 'i-share', onSelect: () => {void Share.share({message: link, url: link}).catch(error => feedback.report?.(error));}},
    {id: 'copy-link', label: t('next.overlays.copyLink'), icon: 'i-copy', onSelect: () => {
      void copyText(link).then(() => feedback.notice?.(t('next.overlays.linkCopied')), () => feedback.notice?.(t('next.overlays.copyLinkFailed')));
    }},
  ]);
}
