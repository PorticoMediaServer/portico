import {useCallback, useEffect, useRef, type MouseEvent, type PointerEvent} from 'react';
import {legacyChannel, type GuideChannelRow} from '@core/guide/index.ts';
import {defaultI18n} from '@i18n';
import type {MenuEntry} from '../../kit/overlays';

const t = defaultI18n.t;
/** The touch hold that opens a channel's menu (tablet-play 6), before the platform's own. */
export const CHANNEL_HOLD_MS = 500;
const HOLD_SLOP_PX = 10;

export type ChannelMenuActions = Readonly<{
  owner: boolean;
  busy: boolean;
  onFavorite(channel: GuideChannelRow): void;
  onHidden(channel: GuideChannelRow, hidden: boolean): void;
  onEdit(channel: GuideChannelRow): void;
}>;

/** desktop-live List view: Add to Favorites for everyone; owners also get Hide channel, and
 * Edit channel on Library Channels. A hidden channel (shown only to an owner who asked) offers
 * Show channel instead. */
export function guideChannelMenuEntries(channel: GuideChannelRow, actions: ChannelMenuActions): readonly MenuEntry[] {
  const hidden = legacyChannel(channel)?.serverHidden ?? false;
  return [
    {id: 'favorite', label: t(channel.favorite ? 'channels.removeFavorite' : 'channels.addFavorite'), icon: channel.favorite ? 'i-star-f' : 'i-star',
      disabled: actions.busy, onSelect: () => actions.onFavorite(channel)},
    ...(actions.owner && channel.kind !== 'library' ? [{id: 'hide', label: t(hidden ? 'channels.unhide' : 'channels.hide'), icon: hidden ? 'i-eye' as const : 'i-eyeoff' as const,
      disabled: actions.busy, onSelect: () => actions.onHidden(channel, !hidden)}] : []),
    ...(actions.owner && channel.kind === 'library' ? [{id: 'edit', label: t('channels.editChannel'), icon: 'i-edit' as const, onSelect: () => actions.onEdit(channel)}] : []),
  ];
}

export const guideChannelEditorDestination = (channelId: string) => ({to: '/settings', search: {scope: 'server', section: 'live', row: 'live.lib.channels', channelId}} as const);

/** A long press or the platform's context menu (right-click, Shift+F10, the Menu key, Android's
 * hold) opens the row's menu. A held touch that moves is a scroll, never a press. */
export function useChannelMenuGestures(open: () => void) {
  const hold = useRef<{timer: ReturnType<typeof setTimeout>; x: number; y: number} | undefined>(undefined), held = useRef(false);
  const cancel = useCallback(() => {if (hold.current) {clearTimeout(hold.current.timer); hold.current = undefined;}}, []);
  useEffect(() => cancel, [cancel]);
  return {
    onContextMenu: (event: MouseEvent<HTMLElement>) => {event.preventDefault(); cancel(); open();},
    // The release that ends a hold is not also a tap on what was under the finger.
    onClickCapture: (event: MouseEvent<HTMLElement>) => {if (held.current) {held.current = false; event.preventDefault(); event.stopPropagation();}},
    onPointerDown: (event: PointerEvent<HTMLElement>) => {
      if (event.pointerType === 'mouse' || !event.isPrimary) return;
      cancel(); held.current = false;
      hold.current = {x: event.clientX, y: event.clientY, timer: setTimeout(() => {hold.current = undefined; held.current = true; open();}, CHANNEL_HOLD_MS)};
    },
    onPointerMove: (event: PointerEvent<HTMLElement>) => {
      const start = hold.current;
      if (start && Math.hypot(event.clientX - start.x, event.clientY - start.y) > HOLD_SLOP_PX) cancel();
    },
    onPointerUp: cancel,
    onPointerCancel: cancel,
    onPointerLeave: cancel,
  };
}
