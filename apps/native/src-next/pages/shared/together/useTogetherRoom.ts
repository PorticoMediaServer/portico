import {useEffect, useId, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type {View} from 'react-native';
import {readCatalogueItemResponse} from '../../../../../../packages/client-core/src/query/catalogue-reads';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {MediaItem} from '../../../../../../packages/client-core/src';
import {parseMediaItem} from '../../../../../../packages/client-core/src/media-item';
import {roomEntry} from '../../../../../../packages/client-core/src/room-protocol';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {tvFocusIds} from '../../../shell/tv-focus';
import {useScreenEntry} from '../../../screens/settings-live/Screen';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeTogetherController} from '../../../screens/settings-live/together-controller';

export type TogetherRoomProps = {isCurrent: () => boolean; controller: NativeTogetherController; ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime};

/** In a Watch Together room (states 27c): the group, what is on and the titles in its queue, the room clock, the host's
 * countdown, and the menus and confirmation a host or member opens. */
export function useTogetherRoom({controller, ports, runtime, isCurrent}: TogetherRoomProps) {
  const theme = useTheme(), t = runtime.text.t, phone = theme.platform === 'phone', gradientId = useId().replace(/:/g, '');
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), group = state.group!;
  const playback = useSyncExternalStore(ports.context.playback.subscribe, ports.context.playback.getSnapshot);
  const [items, setItems] = useState<Readonly<Record<string, MediaItem | null>>>({}), [confirm, setConfirm] = useState<'end' | 'leave'>();
  const [memberMenu, setMemberMenu] = useState<string>(), [queueMenu, setQueueMenu] = useState<string>();
  const menuReturn = useRef<View | null>(null), memberRefs = useRef(new Map<string, View>()), queueRefs = useRef(new Map<string, View>());
  const [clock, setClock] = useState(controller.serverNow);
  const cancel = useRef<View>(null), endRef = useRef<View>(null), leaveRef = useRef<View>(null);
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]), region = ids.surface('together.room');
  const currentItemId = roomEntry(group)?.itemId ?? '';
  const canControl = group.you.mayControl, playing = group.clock.playing;
  const enter = useScreenEntry(theme.isTV ? {regionId: region, childId: canControl && currentItemId ? 'transport' : 'leave'} : undefined, controller.lifetime.current);
  const key = JSON.stringify([...new Set([currentItemId, ...group.queue.filter(value => value.available).map(value => value.itemId)].filter((id): id is string => !!id))]);
  useEffect(() => {
    let active = true;
    for (const id of JSON.parse(key) as string[]) void controller.lifetime.request(signal => readCatalogueItemResponse(ports.context.api, id, signal).then(parseMediaItem)).then(item => {if (active) setItems(value => ({...value, [id]: item}));}, () => {if (active && controller.lifetime.current()) setItems(value => ({...value, [id]: null}));});
    return () => {active = false;};
  }, [key, controller, ports]);
  useEffect(() => {
    const timer = repeat(async () => {setClock(controller.serverNow());}, {everyMs: 1000, backoff: {baseMs: 1000, maxMs: 1000, jitter: 'none'}, immediate: false});
    return () => timer.stop();
  }, [controller]);
  const run = (work: () => Promise<unknown>) => {if (!isCurrent() || !controller.currentGroup(group)) return; void work().catch(error => {if (isCurrent() && controller.currentGroup(group)) runtime.reportError(error);});};
  const now = currentItemId ? items[currentItemId] : undefined;
  const title = !currentItemId ? t(group.clock.entryId ? 'web.together.hiddenTitle' : 'web.together.nothingChosen') : now === null ? t('web.together.hiddenTitle') : now?.title ?? t('web.together.loadingTitle');
  const art = now?.backdropUrl ? runtime.artwork(now.backdropUrl) : undefined;
  const members = group.members, profile = runtime.getSnapshot().profile;
  const hostDeadline = group.clock.playing ? group.host.pausesAt : group.host.endsAt;
  const hostCountdown = hostDeadline ? defaultI18n.clock(Math.max(0, Math.ceil((Date.parse(hostDeadline) - clock) / 1000))) : undefined;
  // A member's face: the initial of the name they chose, and the viewer's own colour and picture on their own row; a private
  // member's face says nothing.
  const faceFor = (member: typeof members[number]) => member.private ? {id: member.id, private: true as const} : {id: member.id, private: false as const,
    initial: [...(member.name ?? member.username ?? '').trim()][0] ?? '',
    ...(member.id === group.you.memberId ? {color: profile.color, source: profile.source} : {})};
  return {faceFor, phone, gradientId, state, group, playback, items, setItems, confirm, setConfirm, memberMenu, setMemberMenu, queueMenu, setQueueMenu, menuReturn, memberRefs, queueRefs, clock, setClock, cancel, endRef, leaveRef, ids, region, currentItemId, canControl, playing, enter, key, run, now, title, art, members, profile, hostDeadline, hostCountdown};
}
