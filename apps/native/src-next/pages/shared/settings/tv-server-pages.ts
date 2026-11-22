import type React from 'react';
import {settingsSpec} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {nextIcons} from '../../../../../../packages/design/src/next-icons';
import type {Icon} from '../../../kit/Icon';

/** The TV's Server settings pages, in their list order. */
export const tvServerPageOrder = ['general', 'dashboard', 'history', 'libraries', 'remote-sources', 'metadata', 'segments', 'live', 'people', 'signin', 'remote', 'integrations', 'streaming', 'storage'] as const;
export type TVServerPage = typeof tvServerPageOrder[number];
export const isTVServerPage = (value: unknown): value is TVServerPage => typeof value === 'string' && (tvServerPageOrder as readonly string[]).includes(value);
export function tvServerPageDefinition(id: TVServerPage) {
  const section = settingsSpec.find(value => value.scope === 'server' && value.id === id);
  if (!section) throw new Error('native_tv_server_page_missing');
  const icon = `i-${section.icon.replace(/^i-/, '')}`;
  return {id, label: section.title, icon: Object.hasOwn(nextIcons, icon) ? icon as React.ComponentProps<typeof Icon>['name'] : 'i-sliders' as const};
}
// The primary-profile admission and reader still enforce these roles. G35 places
// Troubleshooting off television for both roles; no message-log route is offered.
const administratorPages: ReadonlySet<TVServerPage> = new Set(['dashboard', 'people']);
/** Only pages with a real mounted reader are listed. Admission is still checked by route composition; this list never
 * grants a reader authority. */
export const tvServerPagesFor = (role: 'owner' | 'administrator') =>
  tvServerPageOrder.filter(id => role === 'owner' || administratorPages.has(id)).map(tvServerPageDefinition);
