import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type {FlatListProps, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {RoomSessionSnapshot} from '../../../../../../packages/client-core/src/room-session';
import {roomEntry, roomPositionMs, type Room, type GroupAction} from '../../../../../../packages/client-core/src/room-protocol';
import {playbackIdentity} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import {useOptionalFocusRegistry} from '../../../kit/focus';
import {sameAuthority} from '../../../shell/model';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {tvFocusIds} from '../../../shell/tv-focus';
import {groupPort} from '../../../screens/settings-live/group-port';
import {useScreenEntry} from '../../../screens/settings-live/Screen';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeTogetherController} from '../../../screens/settings-live/together-controller';
import type {NativeTogetherMedia} from '../../../screens/settings-live/together-media';
import {memberNames} from '../../../screens/settings-live/together-presentation';

export type RoomMember = Room['members'][number];
export type RoomQueueEntry = Room['queue'][number];
export type TogetherTVRoomProps = {state: RoomSessionSnapshot; group: Room; controller: NativeTogetherController; media: NativeTogetherMedia;
  ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime; isCurrent?: () => boolean};

/** Explicit transfer by the actual host is different from claiming an absent host. Become host uses the current
 * `Room.you.mayBecomeHost` grant. */
export const eligibleHost = (member: RoomMember) => !member.host && member.status === 'in_step';

/** A Watch Together room on TV (tv-more 14, 15). The app still owns the only group, follower and player lifetime; the room
 * reads only the current title and the queue rows in view (`NativeTogetherMedia`), names an episode as the player does, lets
 * a viewer claim an absent host, and puts focus back on Open player when the player is hidden. Every command checks the
 * live room before it is sent. */
export function useTogetherTVRoom({state, group, controller, media, ports, runtime, isCurrent = () => true}: TogetherTVRoomProps) {
  const t = runtime.text.t, focus = useOptionalFocusRegistry();
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]);
  const controls = ids.surface('together.room.controls'), queueRegion = ids.surface('together.room.queue'), people = ids.surface('together.room.people'), footer = ids.surface('together.room.footer');
  const playback = useSyncExternalStore(ports.context.playback.subscribe, ports.context.playback.getSnapshot);
  const reads = useSyncExternalStore(media.subscribe, media.getSnapshot);
  const currentItemId = roomEntry(group)?.itemId ?? '';
  const entries = group.queue, members = group.members;
  const [visible, setVisible] = useState<readonly string[]>(() => entries.slice(0, 4).flatMap(value => !value.available || !value.itemId ? [] : [value.itemId]));
  const wanted = JSON.stringify([...new Set([currentItemId, ...visible.filter(id => entries.some(entry => entry.available && entry.itemId === id))].filter(Boolean))]);
  const [clock, setClock] = useState(controller.serverNow);
  const [confirm, setConfirm] = useState<'end' | 'leave'>(), [confirmOpener, setConfirmOpener] = useState<'end' | 'leave'>('leave'), [memberMenu, setMemberMenu] = useState<string>(), [queueMenu, setQueueMenu] = useState<string>();
  const cancel = useRef<View>(null), endRef = useRef<View>(null), leaveRef = useRef<View>(null), menuReturn = useRef<View | null>(null);
  // `confirm` is cleared while the dialog closes. The opener is its own committed state so the focus lifecycle still restores
  // the exact control that opened this occurrence, not a ref derived from the state that is already being cleared.
  const queueRefs = useRef(new Map<string, View>()), memberRefs = useRef(new Map<string, View>());
  const viewable = useRef<NonNullable<FlatListProps<RoomQueueEntry>['onViewableItemsChanged']>>(({viewableItems}) => {
    const next = viewableItems.flatMap(value => !value.item.available || !value.item.itemId ? [] : [value.item.itemId]);
    setVisible(previous => JSON.stringify(previous) === JSON.stringify(next) ? previous : next);
  }).current;
  useEffect(() => {media.select(JSON.parse(wanted) as string[]);}, [media, wanted]);
  useEffect(() => () => media.select([]), [media]);
  useEffect(() => {
    const timer = repeat(async () => {setClock(controller.serverNow());}, {everyMs: 1000, backoff: {baseMs: 1000, maxMs: 1000, jitter: 'none'}, immediate: false});
    return () => timer.stop();
  }, [controller]);
  const currentRead = reads.items[currentItemId], item = currentRead?.phase === 'ready' ? currentRead.item : undefined;
  const subject = playback.subject, subjectId = subject?.kind === 'channel' ? subject.id : subject?.item.id;
  const samePlayback = !!playback.fence && groupPort()?.matches?.(playback.fence) === true && sameAuthority(playback.authority, controller.lifetime.authority) && playback.playback.itemId === currentItemId;
  // Watch is the semantic producer of episode/show numbering. Basic item reads do not contain it; never manufacture S/E
  // numbers or strip an episode title.
  const identity = samePlayback && subject && subjectId === currentItemId && currentRead?.phase !== 'error' ? playbackIdentity(subject, 'full', runtime.text) : undefined;
  const currentTitle = !currentItemId ? t(group.clock.entryId ? 'web.together.hiddenTitle' : 'web.together.nothingChosen')
    : currentRead?.phase === 'error' ? runtime.error(currentRead.error, () => media.retry(currentItemId)).title : identity?.title ?? item?.title ?? t('web.together.loadingTitle');
  const playing = group.clock.playing, away = !group.host.present, canControl = group.you.mayControl;
  const showTransport = canControl && !!group.clock.entryId;
  // Absent means false until the server publishes the advisory permission. The server still checks the same guards
  // transactionally on the claim itself.
  const canClaimHost = !group.you.host && group.you.mayBecomeHost === true;
  const entryRegion = showTransport || samePlayback || canClaimHost ? controls : footer, entryChild = canClaimHost ? 'claim-host' : showTransport ? 'transport' : samePlayback ? 'open-player' : 'leave';
  const enter = useScreenEntry({regionId: entryRegion, childId: entryChild}, controller.lifetime.current);
  // The platform also starts on the entry control (Android TV's own recovery otherwise lands on the first control it finds, New
  // code), but only until it has taken focus once: the room redraws every second and must not pull focus back to it.
  const [entered, setEntered] = useState(false);
  const entryFocus = (id: string) => entered || entryChild !== id ? {} : {preferredFocus: true, onFocus: () => setEntered(true)};
  const current = () => isCurrent() && ports.context.navigation.getSnapshot().route.kind === 'together' ? controller.currentGroup(group) : undefined;
  useEffect(() => {
    // This effect's own immutable witness over the two scalars its keys already track: the route check and the controller's
    // exact guard stay inside the subscription/RAF callbacks.
    const witness = Object.freeze({id: group.id, you: Object.freeze({memberId: group.you.memberId})});
    const current = () => isCurrent() && ports.context.navigation.getSnapshot().route.kind === 'together' ? controller.currentGroup(witness) : undefined;
    const port = groupPort(); if (!port) return;
    let wasVisible = port.playerVisible?.() !== false, frame: number | undefined;
    const off = port.subscribe(() => {
      const visible = port.playerVisible?.() !== false;
      if (wasVisible && !visible && current()) frame = requestAnimationFrame(() => {if (current()) focus?.focus({regionId: controls, childId: 'open-player'});});
      wasVisible = visible;
    });
    return () => {off(); if (frame !== undefined) cancelAnimationFrame(frame);};
  }, [ports.context, controller, group.id, group.you.memberId, controls, focus, isCurrent]);
  const run = (work: () => Promise<unknown>, allowed: (live: Room) => boolean = () => true) => {
    const live = current(); if (!live || controller.getSnapshot().busy || !allowed(live)) return;
    void work().catch(error => {if (current()) runtime.reportError(error);});
  };
  const transport = (command: GroupAction, input: Parameters<NativeTogetherController['transport']>[1] = {}) => run(() => controller.transport(command, input), live =>
    live.you.mayControl
    && (!input.entryId || live.queue.some(value => value.entryId === input.entryId && value.available && !!value.itemId) === true));
  const transfer = (id: string) => run(() => controller.makeHost(id), live => id === live.you.memberId
    ? !live.you.host && live.you.mayBecomeHost === true
    : live.you.host && live.members.some(value => value.id === id && eligibleHost(value)));
  const remove = (id: string) => run(() => controller.queueRemove(id), live => live.you.mayControl
    && live.queue.some(value => value.entryId === id) === true);
  const askLeave = () => {const live = current(); if (!live) return; if (live.you.host) {setConfirmOpener('leave'); setConfirm('leave');} else run(() => controller.leave(), value => !value.you.host);};
  const askEnd = () => {setConfirmOpener('end'); setConfirm('end');};
  const confirmEnd = () => run(async () => {const done = confirm === 'end' ? await controller.end() : (await controller.leave(), true); if (done && current()) setConfirm(undefined);}, live => live.you.host);
  const openPlayer = () => {
    const actual = ports.context.playback.getSnapshot();
    if (current() && actual.fence && groupPort()?.matches?.(actual.fence) === true && sameAuthority(actual.authority, controller.lifetime.authority) && actual.playback.itemId === currentItemId) ports.context.playback.open(actual.fence);
  };
  const deadline = playing ? group.host.pausesAt : group.host.endsAt;
  const countdown = deadline ? defaultI18n.clock(Math.max(0, Math.ceil((Date.parse(deadline) - clock) / 1000))) : undefined;
  const notice = away ? [t('web.together.hostDropped'), countdown ? t(playing ? 'web.together.pausesIn' : 'web.together.endsIn', {time: countdown}) : undefined].filter(Boolean).join(' ') : state.error;
  const cantSee = !!group.cantSee && group.you.host && canControl;
  const position = roomPositionMs(group, clock) / 1000;
  // An unknown length (P2-29: unrepresentable) shows no total and no bar, never a zero.
  const duration = item?.duration !== undefined && item.duration > 0 ? item.duration : undefined;
  const elapsed = duration !== undefined ? t('web.together.positionOf', {position: defaultI18n.clock(position), duration: defaultI18n.clock(duration)}) : undefined;
  const fraction = duration !== undefined ? Math.max(0, Math.min(1, position / duration)) : undefined;
  const art = runtime.artwork(item?.backdropUrl), viewer = runtime.getSnapshot().profile;
  const currentMember = members.find(value => value.id === memberMenu), currentQueue = entries.find(value => value.entryId === queueMenu);
  const menuRead = currentQueue?.itemId ? reads.items[currentQueue.itemId] : undefined;
  useEffect(() => {
    const hasQueueAction = currentQueue && (group.you.mayControl || canControl && currentQueue.available && !!currentQueue.itemId);
    if (queueMenu && !hasQueueAction) setQueueMenu(undefined);
    if (memberMenu && (!currentMember || !group.you.host || !eligibleHost(currentMember))) setMemberMenu(undefined);
    if (confirm && !group.you.host) setConfirm(undefined);
  }, [queueMenu, currentQueue, memberMenu, currentMember, canControl, confirm, group.you.mayControl, group.you.host]);
  const validInvite = group.invitation && Date.parse(group.invitation.expiresAt) > clock && group.invitation.joinsLeft > 0 ? group.invitation : undefined;
  const openQueueMenu = (entry: RoomQueueEntry) => {menuReturn.current = queueRefs.current.get(entry.entryId) ?? null; setQueueMenu(entry.entryId);};
  const openMemberMenu = (member: RoomMember) => {menuReturn.current = memberRefs.current.get(member.id) ?? null; setMemberMenu(member.id);};
  /** One queue row's words: the title (an error's own title when its read failed), its facts, and whether it is on now. */
  const queueRow = (entry: RoomQueueEntry, index: number) => {
    const read = entry.itemId ? reads.items[entry.itemId] : undefined, value = read?.phase === 'ready' ? read.item : undefined;
    const isCurrent = entry.entryId === group.clock.entryId;
    const title = !entry.available ? t('web.together.notAvailable') : read?.phase === 'error' ? runtime.error(read.error, () => media.retry(entry.itemId!)).title
      : isCurrent ? identity?.title ?? value?.title ?? t('web.together.loadingTitle') : value?.title ?? t('web.together.loadingTitle');
    const facts = isCurrent && identity ? identity.subtitle.map(part => part.text) : value?.kind === 'movie' ? [t('next.browse.kind.movie'), ...(value.year === undefined ? [] : [String(value.year)])] : [];
    const press = () => read?.phase === 'error' && entry.itemId ? media.retry(entry.itemId) : openQueueMenu(entry);
    return {key: entry.entryId, number: index + 1, current: isCurrent, title: typeof title === 'string' ? title : String(title), facts,
      still: runtime.artwork(value?.backdropUrl), onPress: press,
      ref: (node: View | null) => {if (node) queueRefs.current.set(entry.entryId, node); else queueRefs.current.delete(entry.entryId);}};
  };
  /** One person's row: their face (the viewer's own colour and picture on their own row; a private member's says nothing),
   * name ("Demo (you)"), what they chose to be called, Host, and in step, away, loading or catching up. */
  const memberRow = (member: RoomMember) => {
    const canTransfer = group.you.host && eligibleHost(member), names = memberNames(member, t('together.privateViewer'));
    const name = member.private || member.id !== group.you.memberId ? names.primary : t('web.together.you', {name: names.primary});
    const own = member.id === group.you.memberId;
    return {key: member.id, name, note: names.secondary, host: member.host ? t('web.together.pill.host') : undefined,
      face: member.private ? {id: member.id, private: true as const} : {id: member.id, private: false as const, initial: [...(member.name ?? member.username ?? '').trim()][0] ?? '',
        ...(own ? {color: viewer.color, source: viewer.source} : {})},
      status: t(member.status === 'away' ? 'web.together.away' : member.status === 'in_step' ? 'web.together.inStep' : member.status === 'buffering' ? 'web.together.loading' : 'web.together.catchingUp'),
      tone: member.status === 'away' ? 'quiet' as const : member.status === 'in_step' ? 'good' as const : 'warn' as const,
      accessibilityLabel: names.secondary ? `${name} ${names.secondary}` : name,
      onPress: canTransfer ? () => openMemberMenu(member) : undefined,
      ref: (node: View | null) => {if (node) memberRefs.current.set(member.id, node); else memberRefs.current.delete(member.id);}};
  };
  // The queue's and a member's side sheets: a row's title (an unavailable one says so), Watch this for whoever may control
  // and the row can play, Remove for whoever may change the queue; Make host for the host on an in-step member.
  const queueMenuTitle = !currentQueue?.available ? t('web.together.notAvailable') : menuRead?.phase === 'ready' ? menuRead.item.title : undefined;
  const queueMenuItems = currentQueue ? [
    ...(canControl && currentQueue.available && currentQueue.itemId ? [{id: 'watch', label: t('web.together.watchThis'), disabled: state.busy, onSelect: () => transport('watch', {entryId: currentQueue.entryId})}] : []),
    ...(group.you.mayControl ? [{id: 'remove', label: t('web.together.removeFromQueue'), disabled: state.busy, onSelect: () => remove(currentQueue.entryId)}] : []),
  ] : [];
  const memberMenuOpen = !!memberMenu && !!currentMember && group.you.host && eligibleHost(currentMember);
  const memberMenuTitle = currentMember ? memberNames(currentMember, t('together.privateViewer')).primary : undefined;
  const memberMenuItems = memberMenuOpen ? [{id: 'host', label: t('web.together.makeHost'), disabled: state.busy, onSelect: () => transfer(currentMember!.id)}] : [];
  const stateLabel = t(away ? 'web.together.state.hostReconnecting' : !group.clock.entryId ? 'web.together.state.lobby' : group.clock.waitUntil ? 'web.together.state.waiting' : playing ? 'web.together.state.playing' : 'web.downloads.state.paused');
  const readiness = group.clock.waitingFor === 0 ? t('web.together.allReady') : t('web.together.waiting', {ready: group.clock.ready, total: group.members.length, count: group.clock.waitingFor});
  return {ids, controls, queueRegion, people, footer, state, group, entries, members, currentItemId, currentTitle, identity, item, samePlayback,
    playing, away, canControl, showTransport, canClaimHost, entryRegion, entryFocus, enter, run, transport, transfer, remove, askLeave, askEnd, confirmEnd, openPlayer,
    notice, cantSee, elapsed, fraction, art, viewer, currentMember, currentQueue, menuRead, validInvite, queueRow, memberRow, openMemberMenu, memberRefs,
    viewable, queueMenuTitle, queueMenuItems, memberMenuOpen, memberMenuTitle, memberMenuItems, confirm, setConfirm, confirmOpener, memberMenu, setMemberMenu, queueMenu, setQueueMenu, cancel, endRef, leaveRef, menuReturn, stateLabel, readiness};
}
