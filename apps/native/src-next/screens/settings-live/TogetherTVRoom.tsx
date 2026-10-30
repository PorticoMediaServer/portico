import React, {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {FlatList, Text, View, type FlatListProps} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {GroupSessionSnapshot} from '../../../../../packages/client-core/src/group-session';
import {groupTargetPositionUs, type Group, type GroupMember, type GroupQueueEntry, type GroupTransportCommand} from '../../../../../packages/client-core/src/social-playback';
import {playbackIdentity} from '../../../../../packages/client-core/src/presentation/kit-model';
import {Button, Focusable} from '../../kit/controls';
import {FocusRegion, useOptionalFocusRegistry} from '../../kit/focus';
import {Banner, Dialog, Menu} from '../../kit/overlays';
import {Artwork, ProfileFace} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {alpha, shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import {sameAuthority} from '../../shell/model';
import type {NativeShellRuntime} from '../../shell/runtime';
import {tvFocusIds} from '../../shell/tv-focus';
import {groupPort} from './group-port';
import {Screen, useScreenEntry} from './Screen';
import type {NativeSettingsLivePorts} from './ports';
import type {NativeTogetherController} from './together-controller';
import type {NativeTogetherMedia} from './together-media';
import {memberNames} from './together-presentation';

/** tv-more14/15. The app still owns the only group/follower/player lifetime.
 * This screen reads only the current title and native-visible queue rows. */
export function NativeTogetherTVRoom({state, group, controller, media, ports, runtime}: {
  state: GroupSessionSnapshot; group: Group; controller: NativeTogetherController; media: NativeTogetherMedia;
  ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime;
}) {
  const theme = useTheme(), t = runtime.text.t, focus = useOptionalFocusRegistry();
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]);
  const controls = ids.surface('together.room.controls'), queueRegion = ids.surface('together.room.queue'), people = ids.surface('together.room.people'), footer = ids.surface('together.room.footer');
  const playback = useSyncExternalStore(ports.context.playback.subscribe, ports.context.playback.getSnapshot);
  const reads = useSyncExternalStore(media.subscribe, media.getSnapshot);
  const queue = state.queue ?? group.queue, entries = queue?.entries ?? [], members = group.members.filter(value => value.state === 'joined');
  const [visible, setVisible] = useState<readonly string[]>(() => entries.slice(0, 4).flatMap(value => value.unavailable || !value.itemId ? [] : [value.itemId]));
  const wanted = JSON.stringify([...new Set([group.timeline.itemId, ...visible].filter(Boolean))]);
  const [clock, setClock] = useState(controller.serverNow), [bodyHeight, setBodyHeight] = useState(0), [titleHeight, setTitleHeight] = useState(68.2);
  const [confirm, setConfirm] = useState<'end' | 'leave'>(), [confirmOpener, setConfirmOpener] = useState<'end' | 'leave'>('leave'), [memberMenu, setMemberMenu] = useState<string>(), [queueMenu, setQueueMenu] = useState<string>();
  const cancel = useRef<View>(null), endRef = useRef<View>(null), leaveRef = useRef<View>(null), menuReturn = useRef<View | null>(null);
  // `confirm` is cleared while the dialog closes. The opener is its own committed state so the
  // focus lifecycle still restores the exact control that opened this occurrence, not a ref
  // derived from the state that is already being cleared.
  const queueRefs = useRef(new Map<string, View>()), memberRefs = useRef(new Map<string, View>());
  const viewable = useRef<NonNullable<FlatListProps<GroupQueueEntry>['onViewableItemsChanged']>>(({viewableItems}) => {
    const next = viewableItems.flatMap(value => value.item.unavailable || !value.item.itemId ? [] : [value.item.itemId]);
    setVisible(previous => JSON.stringify(previous) === JSON.stringify(next) ? previous : next);
  }).current;
  useEffect(() => {media.select(JSON.parse(wanted) as string[]);}, [media, wanted]);
  useEffect(() => () => media.select([]), [media]);
  useEffect(() => {void controller.refreshQueue().catch(error => {if (controller.lifetime.current()) runtime.reportError(error);});}, [controller, runtime, group.queueRevision]);
  useEffect(() => {const timer = setInterval(() => setClock(controller.serverNow()), 1000); return () => clearInterval(timer);}, [controller]);
  const currentRead = reads.items[group.timeline.itemId], item = currentRead?.phase === 'ready' ? currentRead.item : undefined;
  const subject = playback.subject, subjectId = subject?.kind === 'channel' ? subject.id : subject?.item.id;
  const samePlayback = !!playback.fence && groupPort()?.matches?.(playback.fence) === true && sameAuthority(playback.authority, controller.lifetime.authority) && playback.playback.itemId === group.timeline.itemId;
  // Watch is the semantic producer of episode/show numbering. Basic item reads do
  // not contain it; never manufacture S/E numbers or strip an episode title.
  const identity = samePlayback && subject && subjectId === group.timeline.itemId && currentRead?.phase !== 'error' ? playbackIdentity(subject, 'full', runtime.text) : undefined;
  const currentTitle = !group.timeline.itemId ? t(group.timeline.currentEntryId ? 'web.together.hiddenTitle' : 'web.together.nothingChosen')
    : currentRead?.phase === 'error' ? runtime.error(currentRead.error, () => media.retry(group.timeline.itemId)).title : identity?.title ?? item?.title ?? t('web.together.loadingTitle');
  const playing = group.timeline.state === 'playing', away = group.host.presence !== 'connected', canControl = group.permissions.canControl;
  const showTransport = canControl && !!group.timeline.currentEntryId;
  // Absent means false until the server publishes the advisory permission. The
  // server still checks the same guards transactionally on the claim itself.
  const canClaimHost = !group.permissions.isHost && (group.permissions as typeof group.permissions & {canClaimHost?: boolean}).canClaimHost === true;
  const entryRegion = showTransport || samePlayback || canClaimHost ? controls : footer, entryChild = canClaimHost ? 'claim-host' : showTransport ? 'transport' : samePlayback ? 'open-player' : 'leave';
  const enter = useScreenEntry({regionId: entryRegion, childId: entryChild}, controller.lifetime.current);
  const current = () => ports.context.navigation.getSnapshot().route.kind === 'together' ? controller.currentGroup(group) : undefined;
  useEffect(() => {
    // This effect's own immutable witness over the two scalars its keys already track: the route
    // check and the controller's exact guard stay inside the subscription/RAF callbacks.
    const witness: Pick<Group, 'id' | 'viewerMemberId'> = Object.freeze({id: group.id, viewerMemberId: group.viewerMemberId});
    const current = () => ports.context.navigation.getSnapshot().route.kind === 'together' ? controller.currentGroup(witness) : undefined;
    const port = groupPort();if (!port) return;
    let wasVisible = port.playerVisible?.() !== false, frame: number | undefined;
    const off = port.subscribe(() => {
      const visible = port.playerVisible?.() !== false;
      if (wasVisible && !visible && current()) frame = requestAnimationFrame(() => {if (current()) focus?.focus({regionId: controls, childId: 'open-player'});});
      wasVisible = visible;
    });
    return () => {off();if (frame !== undefined) cancelAnimationFrame(frame);};
  }, [ports.context, controller, group.id, group.viewerMemberId, controls, focus]);
  const run = (work: () => Promise<unknown>, allowed: (live: Group) => boolean = () => true) => {
    const live = current();if (!live || controller.getSnapshot().busy || !allowed(live)) return;
    void work().catch(error => {if (current()) runtime.reportError(error);});
  };
  const transport = (command: GroupTransportCommand, input: Parameters<NativeTogetherController['transport']>[1] = {}) => run(() => controller.transport(command, input), live =>
    live.permissions.canControl && (!input.allowUnavailable || live.permissions.isHost)
    && (!input.entryId || (controller.getSnapshot().queue ?? live.queue)?.entries.some(value => value.entryId === input.entryId && !value.unavailable && !!value.itemId) === true));
  const transfer = (id: string) => run(() => controller.makeHost(id), live => id === live.viewerMemberId
    ? !live.permissions.isHost && live.permissions.canClaimHost === true
    : live.permissions.isHost && live.members.some(value => value.id === id && eligibleHost(value)));
  const remove = (id: string) => run(() => controller.queueRemove(id), live => live.permissions.canManageQueue
    && (controller.getSnapshot().queue ?? live.queue)?.entries.some(value => value.entryId === id) === true);
  const askLeave = () => {const live = current();if (!live) return;if (live.permissions.isHost) {setConfirmOpener('leave'); setConfirm('leave');}else run(() => controller.leave(), value => !value.permissions.isHost);};
  const deadline = playing ? group.host.pauseAt : group.host.endAt;
  const countdown = deadline ? defaultI18n.clock(Math.max(0, Math.ceil((Date.parse(deadline) - clock) / 1000))) : undefined;
  const notice = away ? [t('web.together.hostDropped'), countdown ? t(playing ? 'web.together.pausesIn' : 'web.together.endsIn', {time: countdown}) : undefined].filter(Boolean).join(' ') : state.error;
  const refused = state.refused && group.permissions.isHost && canControl ? state.refused : undefined;
  const headingExtra = Math.max(0, titleHeight - 68.2);
  const bodyTop = 196 + headingExtra + (notice ? 120 : 0) + (refused ? 84 : 0);
  const position = Number(groupTargetPositionUs(group.timeline, Date.parse(group.timeline.anchorAt), clock) / 1000000n);
  // An unknown length (P2-29: unrepresentable) shows no total and no bar, never a zero.
  const duration = item?.duration !== undefined && item.duration > 0 ? item.duration : undefined;
  const elapsed = duration !== undefined ? t('web.together.positionOf', {position: defaultI18n.clock(position), duration: defaultI18n.clock(duration)}) : undefined;
  const fraction = duration !== undefined ? Math.max(0, Math.min(1, position / duration)) : undefined;
  const art = runtime.artwork(item?.backdropUrl), viewer = runtime.getSnapshot().profile;
  const currentMember = members.find(value => value.id === memberMenu), currentQueue = entries.find(value => value.entryId === queueMenu);
  const menuRead = currentQueue?.itemId ? reads.items[currentQueue.itemId] : undefined;
  useEffect(() => {
    const hasQueueAction = currentQueue && (group.permissions.canManageQueue || canControl && !currentQueue.unavailable && !!currentQueue.itemId);
    if (queueMenu && !hasQueueAction) setQueueMenu(undefined);
    if (memberMenu && (!currentMember || !group.permissions.isHost || !eligibleHost(currentMember))) setMemberMenu(undefined);
    if (confirm && !group.permissions.isHost) setConfirm(undefined);
  }, [queueMenu, currentQueue, memberMenu, currentMember, canControl, confirm, group.permissions.canManageQueue, group.permissions.isHost]);
  const validInvite = state.invite && Date.parse(state.invite.expiresAt) > clock && state.invite.uses < state.invite.maxUses ? state.invite : undefined;
  const openQueueMenu = (entry: GroupQueueEntry) => {menuReturn.current = queueRefs.current.get(entry.entryId) ?? null; setQueueMenu(entry.entryId);};
  return <Screen style={{paddingTop: 0}}>
    <Text style={[shellType(24, 700), {position: 'absolute', left: 200, top: 52, color: theme.colors.textSecondary}]}>{t('entry.group.together')}</Text>
    <Text accessibilityRole="header" onLayout={event => setTitleHeight(event.nativeEvent.layout.height)} style={[shellType(62, 800, 68.2, -.04), {right: 96, position: 'absolute', left: 200, top: 80, color: theme.colors.text}]}>{group.name}</Text>
    {notice && <View style={{position: 'absolute', left: 200, right: 96, top: 176 + headingExtra}}><Banner message={notice} tone="warning"/></View>}
    {refused && <View style={{position: 'absolute', left: 200, right: 96, top: (notice ? 236 : 176) + headingExtra}}><Banner message={t('web.together.refused')} tone="warning"
      action={{label: t('web.together.goAhead'), onPress: () => transport(refused.command, {...refused.input, allowUnavailable: true})}}/></View>}
    <View onLayout={event => setBodyHeight(event.nativeEvent.layout.height)} style={{position: 'absolute', left: 200, right: 96, top: bodyTop, bottom: 136, flexDirection: 'row', gap: 96}}>
      <View style={{width: 840}}>
        <View style={{height: 290, borderRadius: 22, overflow: 'hidden', backgroundColor: theme.colors.raised, justifyContent: 'flex-end', paddingHorizontal: 32, paddingBottom: 26}}>
          {art && <View style={{position: 'absolute', inset: 0, opacity: .62}}><Artwork source={art}/></View>}
          <Text style={[shellType(22, 700), {color: away ? theme.colors.amber : theme.colors.healthy, marginBottom: 8}]}>{t(away ? 'web.together.state.hostReconnecting' : playing ? 'web.together.state.playing' : 'web.downloads.state.paused')}</Text>
          <Text numberOfLines={1} style={[shellType(40, 800, 'normal', -.03), {color: theme.colors.text}]}>{currentTitle}</Text>
          <FactLine size={24} parts={[...(identity?.subtitle.map(part => part.text) ?? []), ...(elapsed ? [elapsed] : [])]}/>
          <FactLine size={22} tertiary parts={(group.readiness.aggregate === 'ready' ? t('web.together.allReady') : t('web.together.waiting', {ready: group.readiness.ready, total: group.readiness.memberCount, count: group.readiness.buffering + group.readiness.lagging + group.readiness.stale})).split(' · ')}/>
          {fraction !== undefined && <View style={{height: 6, borderRadius: 3, backgroundColor: alpha(theme.colors.text, .2), marginTop: 14, overflow: 'hidden'}}><View style={{height: '100%', width: `${fraction * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>}
        </View>
        <FocusRegion id={controls} entry={showTransport ? 'transport' : 'open-player'} orientation="horizontal" edges={{left: ids.rail, right: people, down: queueRegion}} onLayout={entryRegion === controls ? enter : undefined} style={{height: 72, flexDirection: 'row', alignItems: 'center', gap: 18, marginTop: 26}}>
          {showTransport && <Button focusId="transport" testID="together-transport" disabled={state.busy} icon={playing ? 'i-pause' : 'i-play'} onPress={() => transport(playing ? 'pause' : 'play')}>{t(playing ? 'web.together.pauseAll' : 'web.together.playAll')}</Button>}
          {samePlayback && <Button focusId="open-player" testID="together-open-player" variant="glass" icon="i-tv" onPress={() => {const actual = ports.context.playback.getSnapshot(); if (current() && actual.fence && groupPort()?.matches?.(actual.fence) === true && sameAuthority(actual.authority, controller.lifetime.authority) && actual.playback.itemId === group.timeline.itemId) ports.context.playback.open(actual.fence);}}>{t('web.together.openPlayer')}</Button>}
          {canClaimHost && <Button focusId="claim-host" testID="together-claim-host" hasTVPreferredFocus disabled={state.busy} icon="i-people" onPress={() => transfer(group.viewerMemberId)}>{t('web.together.becomeHost')}</Button>}
          {!canControl && !canClaimHost && <Text style={[shellType(22, 400), {color: theme.colors.textTertiary, marginLeft: 14}]}>{t('web.together.pill.hostControls')}</Text>}
        </FocusRegion>
        <Text style={[shellType(20, 700), {color: theme.colors.textTertiary, marginTop: 40, marginBottom: 12}]}>{t('player.queue')}</Text>
        <FocusRegion id={queueRegion} entry={entries[0]?.entryId} edges={{left: ids.rail, right: people, up: controls, down: footer}}>
          <FlatList data={entries} keyExtractor={entry => entry.entryId} onViewableItemsChanged={viewable} initialNumToRender={4} maxToRenderPerBatch={4} windowSize={3} removeClippedSubviews={false}
            getItemLayout={(_, index) => ({index, length: 80, offset: index * 80})} style={{height: Math.min(entries.length * 80, Math.max(0, bodyHeight - 468)), flexGrow: 0, backgroundColor: theme.colors.raised, borderRadius: 18}}
            renderItem={({item: entry, index}) => {
              const read = entry.itemId ? reads.items[entry.itemId] : undefined, value = read?.phase === 'ready' ? read.item : undefined;
              const current = entry.entryId === group.timeline.currentEntryId, image = runtime.artwork(value?.backdropUrl);
              const title = entry.unavailable ? t('web.together.notAvailable') : read?.phase === 'error' ? runtime.error(read.error, () => media.retry(entry.itemId!)).title : current ? identity?.title ?? value?.title ?? t('web.together.loadingTitle') : value?.title ?? t('web.together.loadingTitle');
              return <Focusable ref={node => {if (node) queueRefs.current.set(entry.entryId, node); else queueRefs.current.delete(entry.entryId);}} focusId={entry.entryId} testID={`together-queue-${entry.entryId}`} accessibilityRole="button" accessibilityLabel={typeof title === 'string' ? title : undefined} focusKind="row" onPress={() => read?.phase === 'error' && entry.itemId ? media.retry(entry.itemId) : openQueueMenu(entry)} onLongPressAction={() => openQueueMenu(entry)} style={{height: 80, paddingHorizontal: 22, flexDirection: 'row', alignItems: 'center', gap: 18, borderTopWidth: index ? 1 : 0, borderTopColor: alpha(theme.colors.line, .7)}}>
                <View style={{width: 32, alignItems: 'center'}}>{current ? <Icon name="i-play" size={22} color={theme.colors.textTertiary}/> : <Text style={[shellType(22, 700), {color: theme.colors.textTertiary}]}>{index + 1}</Text>}</View>
                {image && <View style={{width: 64, height: 36, borderRadius: 7, overflow: 'hidden'}}><Artwork source={image}/></View>}
                <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[shellType(24, 700), {color: theme.colors.text}]}>{title}</Text><FactLine size={20} tertiary parts={current && identity ? identity.subtitle.map(part => part.text) : value?.kind === 'movie' ? [t('next.browse.kind.movie'), ...(value.year === undefined ? [] : [String(value.year)])] : []}/></View>
                {current && <Text style={[shellType(20, 700), {color: theme.colors.accentStrong}]}>{t('web.together.nowPlaying')}</Text>}
              </Focusable>;
            }}/>
          {!entries.length && <Text style={[shellType(22, 400), {color: theme.colors.textSecondary}]}>{t(group.permissions.canManageQueue ? 'web.together.emptyQueueHost' : 'web.together.emptyQueue')}</Text>}
        </FocusRegion>
      </View>
      <View style={{width: 688}}>
        <Text style={[shellType(20, 700), {color: theme.colors.textTertiary, marginBottom: 12}]}>{t('web.together.people', {count: members.length})}</Text>
        <FocusRegion id={people} entry={members[0]?.id} edges={{left: controls, down: footer}}>
          <FlatList data={members} keyExtractor={member => member.id} initialNumToRender={4} maxToRenderPerBatch={4} windowSize={3} removeClippedSubviews={false}
            getItemLayout={(_, index) => ({index, length: 88, offset: index * 88})} style={{height: Math.min(members.length * 88, Math.max(88, bodyHeight - (group.permissions.isHost ? 444 : 40))), flexGrow: 0, borderRadius: 18, backgroundColor: theme.colors.raised}}
            renderItem={({item: member, index}) => {
              const canTransfer = group.permissions.isHost && eligibleHost(member), names = memberNames(member, t('together.privateViewer'));
              const name = member.private || member.id !== group.viewerMemberId ? names.primary : t('web.together.you', {name: names.primary});
              return <Focusable ref={node => {if (node) memberRefs.current.set(member.id, node); else memberRefs.current.delete(member.id);}} focusId={member.id} testID={`together-member-${member.id}`} accessibilityRole={canTransfer ? 'button' : 'text'} accessibilityLabel={names.secondary ? `${name} ${names.secondary}` : name} focusKind="row" onPress={canTransfer ? () => {menuReturn.current = memberRefs.current.get(member.id) ?? null; setMemberMenu(member.id);} : undefined} style={{height: 88, paddingHorizontal: 22, flexDirection: 'row', alignItems: 'center', gap: 20, borderTopWidth: index ? 1 : 0, borderTopColor: alpha(theme.colors.line, .7)}}>
                {member.private ? <View style={{width: 64, height: 64, borderRadius: 32, backgroundColor: theme.colors.raisedHigh, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-user" size={32} color={theme.colors.textSecondary}/></View> : <ProfileFace size={64} initial={[...member.displayName.trim()][0] ?? ''} color={member.id === group.viewerMemberId ? viewer.color : undefined} source={member.id === group.viewerMemberId ? viewer.source : undefined} scaleOnFocus={false}/>}
                <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[shellType(26, 700), {color: theme.colors.text}]}>{name}</Text>{names.secondary && <Text numberOfLines={1} style={[shellType(20, 400), {color: theme.colors.textSecondary, marginTop: 2}]}>{names.secondary}</Text>}{member.role === 'host' && <Text style={[shellType(20, 600), {color: theme.colors.textTertiary, marginTop: 2}]}>{t('web.together.pill.host')}</Text>}</View>
                <Text style={[shellType(22, 700), {color: member.presence !== 'connected' ? theme.colors.textTertiary : member.readiness === 'ready' ? theme.colors.healthy : theme.colors.amber}]}>{t(member.presence !== 'connected' ? 'web.together.away' : member.readiness === 'ready' ? 'web.together.inStep' : member.readiness === 'buffering' ? 'web.together.loading' : 'web.together.catchingUp')}</Text>
              </Focusable>;
            }}/>
        </FocusRegion>
        {group.permissions.isHost && <>
          <Text style={[shellType(20, 700), {color: theme.colors.textTertiary, marginTop: 44, marginBottom: 12}]}>{t('web.together.invite')}</Text>
          <View style={{borderRadius: 20, paddingHorizontal: 32, paddingVertical: 28, backgroundColor: theme.colors.raised}}>
            {validInvite && <><Text style={[shellType(76, 800, 76, .14), {color: theme.colors.text}]}>{validInvite.code.slice(0, 4)} {validInvite.code.slice(4)}</Text><Text style={[shellType(21, 400, 30.45), {color: theme.colors.textTertiary, marginTop: 14, marginBottom: 20}]}>{t('web.together.inviteHelpJoin', {uses: validInvite.maxUses - validInvite.uses, time: defaultI18n.time(validInvite.expiresAt)})}</Text></>}
            <Button variant="glass" testID="together-new-code" disabled={state.busy} onPress={() => run(() => controller.invite(), live => live.permissions.isHost)}>{t(validInvite ? 'web.together.newCode' : 'web.together.getCode')}</Button>
          </View>
        </>}
      </View>
    </View>
    <FocusRegion id={footer} entry="leave" orientation="horizontal" edges={{left: ids.rail, up: queueRegion}} onLayout={entryRegion === footer ? enter : undefined} style={{position: 'absolute', left: 200, bottom: 46, flexDirection: 'row', gap: 18}}>
      <Button ref={leaveRef} focusId="leave" testID="together-leave" variant="text" textStyle={{color: theme.colors.text}} style={{borderWidth: 1.5, borderColor: alpha(theme.colors.text, .22)}} disabled={state.busy} onPress={askLeave}>{t('web.together.leave')}</Button>
      {group.permissions.isHost && <Button ref={endRef} testID="together-end" variant="text" textStyle={{color: theme.colors.text}} style={{borderWidth: 1.5, borderColor: alpha(theme.colors.text, .22)}} disabled={state.busy} onPress={() => {setConfirmOpener('end'); setConfirm('end');}}>{t('web.together.endAll')}</Button>}
    </FocusRegion>
    <Menu testID="together-queue-menu" open={!!queueMenu && !!currentQueue} onOpenChange={open => {if (!open) setQueueMenu(undefined);}} title={currentQueue?.unavailable ? t('web.together.notAvailable') : menuRead?.phase === 'ready' ? menuRead.item.title : undefined} presentation="sideSheet" returnFocusRef={menuReturn} items={currentQueue ? [
      ...(canControl && !currentQueue.unavailable && currentQueue.itemId ? [{id: 'watch', label: t('web.together.watchThis'), disabled: state.busy, onSelect: () => transport('load', {entryId: currentQueue.entryId})}] : []),
      ...(group.permissions.canManageQueue ? [{id: 'remove', label: t('web.together.removeFromQueue'), disabled: state.busy, onSelect: () => remove(currentQueue.entryId)}] : []),
    ] : []}/>
    <Menu testID="together-member-menu" open={!!memberMenu && !!currentMember && group.permissions.isHost && eligibleHost(currentMember)} onOpenChange={open => {if (!open) setMemberMenu(undefined);}} title={currentMember ? memberNames(currentMember, t('together.privateViewer')).primary : undefined} presentation="sideSheet" returnFocusRef={menuReturn} items={currentMember && group.permissions.isHost && eligibleHost(currentMember) ? [{id: 'host', label: t('web.together.makeHost'), disabled: state.busy, onSelect: () => transfer(currentMember.id)}] : []}/>
    <Dialog testID="together-confirm" open={!!confirm} onOpenChange={open => {if (!open) setConfirm(undefined);}} title={t(confirm === 'end' ? 'confirm.endGroup.title' : 'confirm.leaveAsHost.title')} description={t(confirm === 'end' ? 'confirm.endGroup.body' : 'confirm.leaveAsHost.body')} initialFocusRef={cancel} returnFocusRef={confirmOpener === 'end' ? endRef : leaveRef} footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 18}}><Button focusId="cancel" ref={cancel} variant="glass" onPress={() => setConfirm(undefined)}>{t('action.cancel')}</Button><Button focusId="confirm" variant="text" style={{backgroundColor: alpha(theme.colors.record, .14)}} textStyle={{color: theme.colors.record}} disabled={state.busy} onPress={() => run(async () => {const done = confirm === 'end' ? await controller.end() : (await controller.leave(), true); if (done && current()) setConfirm(undefined);}, live => live.permissions.isHost)}>{t(confirm === 'end' ? 'confirm.endGroup.action' : 'confirm.leaveAsHost.action')}</Button></View>}><View/></Dialog>
  </Screen>;
}

function FactLine({parts, size, tertiary = false}: {parts: readonly string[]; size: number; tertiary?: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16, marginTop: 4}}>{parts.map((part, index) => <Text key={index} style={[shellType(size, 400), {color: tertiary ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{part}</Text>)}</View>;
}
// Explicit transfer by the actual host is different from claiming an absent host.
// Become host remains unavailable until the canonical capability/claim seam is MAIN.
const eligibleHost = (member: GroupMember) => member.role !== 'host' && member.state === 'joined' && member.presence === 'connected' && member.readiness === 'ready';
