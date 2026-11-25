import React, {useMemo} from 'react';
import {createI18n, defaultI18n} from '../../../../../../packages/i18n/src';
import {roomEntry, roomPositionMs, type GroupControl} from '../../../../../../packages/client-core/src/room-protocol';
import {Button, Segmented, TextField} from '../../../kit/controls';
import {SectionCard, SettingsPageFrame, SheetFrame} from '../../../kit/frames';
import {Banner, Dialog, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsRow} from '../../../kit/shell';
import {TogetherActions, TogetherFaces, TogetherInvitation, TogetherInviteCode, TogetherMemberRow, TogetherNowCard} from '../../../kit/together/TogetherParts';
import {useTheme} from '../../../kit/theme';
import {groupPort} from '../../../screens/settings-live/group-port';
import {memberNames} from '../../../screens/settings-live/together-presentation';
import {useTogetherLobby, useTogetherLobbyArt, type TogetherProps} from '../../shared/together/useTogetherLobby';
import {useTogetherRoom, type TogetherRoomProps} from '../../shared/together/useTogetherRoom';
import {shareText} from '../../shared/together/share';

/** Watch Together on a phone or tablet (states 27): from Settings, a page in the Settings sheet on a phone, on its own on a
 * tablet. The invitation to start or join with the groups you're in; Start a group and Join with a code as task sheets;
 * then the room, and the page that says a group ended. */
export function TogetherPage(props: TogetherProps) {
  const t = props.runtime.text?.t ?? defaultI18n.t;
  const theme = useTheme(), lobby = useTogetherLobby(props), {state, group, room, controller} = {...lobby, controller: props.controller};
  const back = props.onBack ? {label: t(room ? 'action.back' : 'next.shell.settings'), testID: 'together-page-back', onPress: lobby.back} : undefined;
  const page = room && group && state.phase === 'ended'
    ? <SettingsPageFrame testID="together-ended" title={t('entry.group.together')} back={back} state={{icon: 'i-people', title: t('web.together.endedTitle'),
        body: t(group.ended?.reason === 'ended_by_host' ? 'web.together.ended.host' : group.ended?.reason === 'no_host' ? 'web.together.ended.away'
          : group.ended?.reason === 'everyone_left' ? 'web.together.ended.empty' : 'web.together.ended.other'),
        primaryAction: {id: 'lobby', testID: 'together-ended-lobby', label: t('web.together.backToLobby'), onPress: () => {if (lobby.viewCurrent(lobby.form)) controller.dismiss();}}}}/>
    : room && group ? <TogetherRoomPage key={`${group.id}/${group.you.memberId}`} back={back} controller={controller} ports={props.ports} runtime={props.runtime}
        isCurrent={lobby.roomCurrent}/>
    : <TogetherLobbyPage {...props} lobby={lobby} back={back}/>;
  return theme.platform === 'phone'
    ? <SheetFrame kind="full" testID="together-sheet" open onOpenChange={open => {if (!open) lobby.back();}}>{page}</SheetFrame>
    : page;
}

function TogetherLobbyPage({lobby, back, controller, ports, runtime}: TogetherProps & {lobby: ReturnType<typeof useTogetherLobby>;
  back: React.ComponentProps<typeof SettingsPageFrame>['back']}) {
  const t = runtime.text?.t ?? defaultI18n.t;
  const {state, form, sheet, busy, faces, name, setName, code, setCode, who, setWho, authority, setAuthority, codeFailure, viewCurrent} = lobby;
  const art = useTogetherLobbyArt({itemId: state.group && roomEntry(state.group)?.itemId, controller, ports, runtime});
  const submit = () => lobby.run(async () => {
    const ok = await lobby.enter(lifetime => sheet === 'join' ? controller.join(code, who, lifetime) : controller.create(name, who, authority, lifetime), form);
    if (ok && viewCurrent(form)) lobby.closeForm();
  });
  const ready = !busy && !!who.trim() && (sheet === 'join' ? code.replace(/[\s-]/g, '').length === 8 : !!name.trim());
  return <>
    <SettingsPageFrame testID="together-lobby" title={t('entry.group.together')} back={back}>
      {state.error && <SectionCard plain><Banner message={state.error} tone="warning"/></SectionCard>}
      <TogetherInvitation art={art} faces={<TogetherFaces faces={faces} privateLabel={t('together.privateViewer')}/>}
        title={t('web.together.tagline')} body={t('web.together.taglineBody')}>
        <Button testID="together-start" align="stretch" ref={lobby.startRef} disabled={busy} icon="i-people" onPress={() => lobby.openForm('start')}>{t('web.together.startTitle')}</Button>
        <Button testID="together-join" align="stretch" ref={lobby.joinRef} disabled={busy} variant="glass" icon="i-kbd" onPress={() => lobby.openForm('join')}>{t('web.together.joinWithCode')}</Button>
      </TogetherInvitation>
      {state.directory.length > 0 && <SectionCard label={t('web.together.yourGroups')}>
        {state.directory.map((value, index) => <SettingsRow key={value.id} first={index === 0} navigates icon="i-people" testID={`together-group-${value.id}`}
          label={value.name} description={t('web.together.people', {count: value.members}).split(' · ').join('  ')}
          value={value.host ? t('web.together.youHost') : undefined}
          onPress={() => lobby.run(() => lobby.enter(lifetime => controller.open(value.id, lifetime), undefined))}/>)}
      </SectionCard>}
      {state.directoryError && <SectionCard plain><Banner message={state.directoryError} tone="warning"/></SectionCard>}
    </SettingsPageFrame>
    {sheet && <SheetFrame kind="task" task="task" testID="together-form" open onOpenChange={open => {if (!open) lobby.closeForm();}}
      title={t(sheet === 'join' ? 'web.together.joinWithCode' : 'web.together.startTitle')}
      subtitle={t(sheet === 'join' ? 'web.together.joinHelp' : 'web.together.startHelp')}
      action={{label: t(sheet === 'join' ? 'web.together.join' : 'web.together.start'), disabled: !ready, onPress: submit}}>
      {state.error && !codeFailure && <Banner message={state.error} tone="warning"/>}
      <SectionCard plain>
      {sheet === 'join'
        ? <TextField ref={lobby.firstField} focusId="code" testID="together-code" label={t('web.together.hostCode')} value={code}
            error={codeFailure ? state.error : undefined} autoCapitalize="characters" maxLength={12}
            onChangeText={value => {if (viewCurrent(form)) {controller.clearJoinFailure(); setCode(value.toUpperCase());}}}/>
        : <TextField ref={lobby.firstField} focusId="name" testID="together-name" label={t('profile.name')} value={name} maxLength={80}
            onChangeText={value => {if (viewCurrent(form)) setName(value);}}/>}
      <TextField focusId="who" testID="together-display-name" label={t('web.together.yourName')} description={t('web.together.shownToOthers')}
        value={who} maxLength={64} onChangeText={value => {if (viewCurrent(form)) setWho(value);}}/>
      </SectionCard>
      {sheet === 'start' && <SectionCard plain label={t('web.together.whoControls')}>
        <Segmented testID="together-authority" accessibilityLabel={t('web.together.whoControls')} value={authority}
          onValueChange={value => {if (viewCurrent(form)) setAuthority(value as GroupControl);}}
          items={(['host', 'everyone'] as const).map(value => ({value, label: t(value === 'host' ? 'web.together.onlyMe' : 'web.together.everyone')}))}
          itemProps={item => ({testID: `together-authority-${item.value}`})}/>
      </SectionCard>}
    </SheetFrame>}
  </>;
}

function TogetherRoomPage({back, ...props}: TogetherRoomProps & {back: React.ComponentProps<typeof SettingsPageFrame>['back']}) {
  const {controller, ports, runtime, isCurrent} = props, t = runtime.text?.t ?? defaultI18n.t;
  const i18n = useMemo(() => runtime.text?.region ? createI18n(runtime.text.region) : defaultI18n, [runtime.text?.region]);
  const room = useTogetherRoom(props), {state, group, playback, items, clock, canControl, playing, members, profile, now, title, art} = room;
  const seconds = roomPositionMs(group, clock) / 1000;
  const invitation = group.invitation && Date.parse(group.invitation.expiresAt) > clock && group.invitation.joinsLeft > 0 ? group.invitation : undefined;
  const queueTitle = (entry: typeof group.queue[number]) => !entry.available || !entry.itemId || items[entry.itemId] === null
    ? t('web.together.notAvailable') : items[entry.itemId]?.title ?? t('web.together.loadingTitle');
  const status = group.clock.waitingFor === 0 ? t('web.together.allReady')
    : t('web.together.waiting', {ready: group.clock.ready, total: group.members.length, count: group.clock.waitingFor});
  const memberMenuTarget = members.find(value => value.id === room.memberMenu);
  return <>
    <SettingsPageFrame testID="together-room" title={group.name} back={back}>
      {state.error && <SectionCard plain><Banner message={state.error} tone="warning"/></SectionCard>}
      {state.phase === 'reconnecting' && <SectionCard plain><Banner message={t('web.together.reconnecting')} tone="warning"/></SectionCard>}
      {!group.host.present && <SectionCard plain><Banner tone="warning" message={[t('web.together.hostDropped'), room.hostCountdown
        ? t(group.clock.playing ? 'web.together.pausesIn' : 'web.together.endsIn', {time: room.hostCountdown}) : undefined].filter(Boolean).join(' ')}/></SectionCard>}
      {!!group.cantSee && group.you.host && canControl && <SectionCard plain><Banner message={t('web.together.refused')} tone="warning"
        action={{label: t('web.together.goAhead'), onPress: () => room.run(() => controller.transport('goAhead'))}}/></SectionCard>}
      <TogetherNowCard art={art} hostPresent={group.host.present} label={t(!group.host.present ? 'web.together.state.hostReconnecting' : !group.clock.entryId ? 'web.together.state.lobby'
        : group.clock.waitUntil ? 'web.together.state.waiting' : playing ? 'web.together.state.playing' : 'web.downloads.state.paused')} title={title} status={status.split(' · ').join('  ')}
        position={now?.duration ? t('web.together.positionOf', {position: i18n.clock(seconds), duration: i18n.clock(now.duration)}) : undefined}
        progress={now?.duration ? seconds / now.duration : undefined}/>
      {(canControl && !!group.clock.entryId || !!playback.fence && !!groupPort()?.matches?.(playback.fence)) && <TogetherActions testID="together-transport-actions">
        {canControl && group.clock.entryId && <Button focusId="transport" testID="together-transport" align="stretch" disabled={state.busy}
          icon={playing && !group.clock.waitUntil ? 'i-pause' : 'i-play'}
          onPress={() => room.run(() => controller.transport(group.clock.waitUntil ? 'goAhead' : playing ? 'pause' : 'play'))}>
          {t(group.clock.waitUntil ? 'web.together.playAnyway' : playing ? 'web.together.pauseAll' : 'web.together.playAll')}</Button>}
        {playback.fence && groupPort()?.matches?.(playback.fence) && <Button testID="together-open-player" align="stretch" variant="glass" icon="i-tv" onPress={() => {
          if (!isCurrent() || !controller.currentGroup(group)) return;
          const current = ports.context.playback.getSnapshot();
          if (current.fence && groupPort()?.matches?.(current.fence)) ports.context.playback.open(current.fence);
        }}>{t('web.together.openPlayer')}</Button>}
      </TogetherActions>}
      {!canControl && <SectionCard plain help={t('web.together.pill.hostControls')}><></></SectionCard>}
      <SectionCard label={t('web.together.people', {count: members.length}).split(' · ')[0]}>
        {members.map((member, index) => {
          const names = memberNames(member, t('together.privateViewer'));
          const name = member.private || member.id !== group.you.memberId ? names.primary : t('web.together.you', {name: names.primary});
          const menu = group.you.host && !member.host && member.status === 'in_step';
          return <TogetherMemberRow key={member.id} first={index === 0} testID={`together-member-${member.id}`} privateLabel={t('together.privateViewer')}
            accessibilityLabel={names.secondary ? `${name} ${names.secondary}` : name} name={name}
            note={[names.secondary, member.host ? t('web.together.pill.host') : undefined].filter(Boolean).join('  ') || undefined}
            inStep={member.status === 'in_step'} status={t(member.status === 'away' ? 'web.together.away' : member.status === 'in_step' ? 'web.together.inStep'
              : member.status === 'buffering' ? 'web.together.loading' : 'web.together.catchingUp')}
            face={room.faceFor(member)}
            onPress={menu ? () => room.setMemberMenu(member.id) : undefined}/>;
        })}
      </SectionCard>
      {group.you.host && <SectionCard plain label={t('web.together.invite')}>
        <TogetherInviteCode code={invitation ? `${invitation.code.slice(0, 4)} ${invitation.code.slice(4)}` : undefined}
          note={invitation ? t('web.together.inviteHelpJoin', {uses: invitation.joinsLeft, time: i18n.time(invitation.expiresAt)}) : undefined}>
          {invitation && <Button variant="glass" size="small" icon="i-share" testID="together-share-code"
            onPress={() => {if (isCurrent() && controller.currentGroup(group)) void shareText(invitation.code).catch(runtime.reportError);}}>{t('web.together.shareCode')}</Button>}
          <Button variant="glass" size="small" testID="together-new-code" disabled={state.busy} onPress={() => room.run(() => controller.invite())}>
            {t(group.invitation ? 'web.together.newCode' : 'web.together.getCode')}</Button>
        </TogetherInviteCode>
      </SectionCard>}
      <SectionCard label={t('player.queue')} help={group.queue.length ? undefined : t(group.you.mayControl ? 'web.together.emptyQueueHost' : 'web.together.emptyQueue')}>
        {group.queue.map((entry, index) => <SettingsRow key={entry.entryId} first={index === 0} testID={`together-queue-${entry.entryId}`} label={queueTitle(entry)}
          onPress={group.you.mayControl || canControl ? () => room.setQueueMenu(entry.entryId) : undefined}/>)}
      </SectionCard>
      <TogetherActions>
        <Button ref={room.leaveRef} focusId="leave" testID="together-leave" align="stretch" variant="glass"
          onPress={() => group.you.host && members.length > 1 ? room.setConfirm('leave') : room.run(() => controller.leave())}>{t('web.together.leave')}</Button>
        {group.you.host && <Button ref={room.endRef} testID="together-end" align="stretch" variant="glass" onPress={() => room.setConfirm('end')}>{t('web.together.endAll')}</Button>}
      </TogetherActions>
    </SettingsPageFrame>
    <Menu testID="together-member-menu" open={room.memberMenu !== undefined} onOpenChange={open => {if (!open) room.setMemberMenu(undefined);}}
      title={memberMenuTarget ? memberNames(memberMenuTarget, t('together.privateViewer')).primary : undefined} presentation="sheet"
      items={room.memberMenu && group.you.host && memberMenuTarget && !memberMenuTarget.host && memberMenuTarget.status === 'in_step'
        ? [{id: 'host', label: t('web.together.makeHost'), disabled: state.busy, onSelect: () => room.run(() => controller.makeHost(room.memberMenu!))}] : []}/>
    <Menu testID="together-queue-menu" open={room.queueMenu !== undefined} onOpenChange={open => {if (!open) room.setQueueMenu(undefined);}} presentation="sheet"
      title={room.queueMenu ? items[group.queue.find(value => value.entryId === room.queueMenu)?.itemId ?? '']?.title ?? t('web.together.notAvailable') : undefined}
      items={room.queueMenu ? [
        ...(canControl && group.queue.some(value => value.entryId === room.queueMenu && value.available && !!value.itemId)
          ? [{id: 'watch', label: t('web.together.watchThis'), disabled: state.busy, onSelect: () => room.run(() => controller.transport('watch', {entryId: room.queueMenu}))}] : []),
        ...(group.you.mayControl ? [{id: 'remove', label: t('web.together.removeFromQueue'), disabled: state.busy, onSelect: () => room.run(() => controller.queueRemove(room.queueMenu!))}] : []),
      ] : []}/>
    <Dialog testID="together-confirm" open={room.confirm !== undefined} onOpenChange={open => {if (!open) room.setConfirm(undefined);}}
      title={t(room.confirm === 'end' ? 'confirm.endGroup.title' : 'confirm.leaveAsHost.title')}
      description={t(room.confirm === 'end' ? 'confirm.endGroup.body' : 'confirm.leaveAsHost.body')} initialFocusRef={room.cancel}
      footer={<OverlayFootRow layout="end">
        <Button ref={room.cancel} variant="glass" onPress={() => room.setConfirm(undefined)}>{t('action.cancel')}</Button>
        <Button variant="danger" disabled={state.busy} onPress={() => room.run(async () => {
          const ended = room.confirm === 'end' ? await controller.end() : (await controller.leave(), true);
          if (ended && controller.lifetime.current()) room.setConfirm(undefined);
        })}>{t(room.confirm === 'end' ? 'confirm.endGroup.action' : 'confirm.leaveAsHost.action')}</Button>
      </OverlayFootRow>}>{null}</Dialog>
  </>;
}
