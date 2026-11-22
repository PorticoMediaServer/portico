import React from 'react';
import {frameSpec} from '@portico/design';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {roomEntry, type GroupControl} from '../../../../../../packages/client-core/src/room-protocol';
import {Button, Segmented} from '../../../kit/controls';
import {FocusRegion} from '../../../kit/focus';
import {SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {Banner, Dialog, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsRow} from '../../../kit/shell';
import {TogetherTVChoice, TogetherTVControls, TogetherTVEyebrow, TogetherTVField, TogetherTVFooter, TogetherTVForm, TogetherTVInvitation, TogetherTVInviteCode, TogetherTVNote,
  TogetherTVNowCard, TogetherTVPeople, TogetherTVQueue, TogetherTVRoomFrame, TogetherTVSection} from '../../../kit/together/TogetherTVParts';
import {NativeTVKeyboard} from '../../../kit/search/TVKeyboard';
import {useScreenEntry} from '../../../kit/layout/Screen';
import {useTogetherLobby, useTogetherLobbyArt, type TogetherProps} from '../../shared/together/useTogetherLobby';
import {useTogetherTVRoom, type TogetherTVRoomProps} from '../../shared/together/useTogetherTVRoom';

const t = defaultI18n.t, spec = frameSpec.together.tv;

/** Watch Together on TV (tv-more 11 to 16): the lobby with its invitation and the groups you're in; Start a group and Join
 * with a code as a side sheet whose fields open the keyboard beside them; then the room; then the page that says a group
 * ended. Back belongs to the route (and to an open form or menu first). */
export function TogetherPage(props: TogetherProps) {
  const lobby = useTogetherLobby(props), {state, group, room, form, viewCurrent} = lobby, {controller} = props;
  if (room && group && state.phase === 'ended') {
    const reason = group.ended?.reason;
    return <SettingsPageFrame testID="together-ended" title={t('entry.group.together')} startFocus="together-ended-lobby" state={{icon: 'i-people', title: t('web.together.endedTitle'),
      body: t(reason === 'ended_by_host' ? 'web.together.ended.host' : reason === 'no_host' ? 'web.together.ended.away' : reason === 'everyone_left' ? 'web.together.ended.empty' : 'web.together.ended.other'),
      primaryAction: {id: 'lobby', testID: 'together-ended-lobby', label: t('web.together.backToLobby'), onPress: () => {if (viewCurrent(form)) controller.dismiss();}}}}/>;
  }
  if (room && group) return <TogetherRoomPage key={`${group.id}/${group.you.memberId}`} state={state} group={group} controller={controller} media={props.media}
    ports={props.ports} runtime={props.runtime} isCurrent={lobby.roomCurrent}/>;
  return <TogetherLobby {...props} lobby={lobby}/>;
}

function TogetherLobby({lobby, controller, ports, runtime}: TogetherProps & {lobby: ReturnType<typeof useTogetherLobby>}) {
  const {state, ids, region, faces, form, sheet, busy, name, setName, code, setCode, who, setWho, authority, setAuthority, typing, setTyping, typingIntent, typingRevision,
    viewCurrent, fieldsRegion, keyboard, type, openForm} = lobby;
  const art = useTogetherLobbyArt({itemId: state.group && roomEntry(state.group)?.itemId, controller, ports, runtime});
  // The lobby is drawn again on return from a room or an ended group: its entry follows that drawing's own layout.
  const enter = useScreenEntry({regionId: region, childId: 'start'}, controller.lifetime.current);
  const submit = () => lobby.run(async () => {
    const ok = await lobby.enter(lifetime => sheet === 'join' ? controller.join(code, who, lifetime) : controller.create(name, who, authority, lifetime), form);
    if (ok && viewCurrent(form)) lobby.closeForm();
  });
  const ready = !busy && !!who.trim() && (sheet === 'join' ? code.replace(/[\s-]/g, '').length === 8 : !!name.trim());
  return <>
    <SettingsPageFrame testID="together-lobby" title={t('entry.group.together')}>
      {state.error && <Banner message={state.error} tone="warning"/>}
      <FocusRegion id={region} entry="start" orientation="free" edges={{left: ids.rail}} onLayout={enter}>
        <TogetherTVInvitation art={art} faces={faces} privateLabel={t('together.privateViewer')} title={t('web.together.tagline')} body={t('web.together.taglineBody')}>
          <Button ref={lobby.startRef} focusId="start" testID="together-start" disabled={busy} icon="i-people" onPress={() => openForm('start')}>{t('web.together.startTitle')}</Button>
          <Button ref={lobby.joinRef} focusId="join" testID="together-join" disabled={busy} variant="glass" icon="i-kbd" onPress={() => openForm('join')}>{t('web.together.joinWithCode')}</Button>
        </TogetherTVInvitation>
        {state.directory.length > 0 && <SectionCard label={t('web.together.yourGroups')}>
          {state.directory.map((value, index) => <SettingsRow key={value.id} first={index === 0} navigates icon="i-people" testID={`together-group-${value.id}`}
            label={value.name} description={t('web.together.people', {count: value.members}).split(' · ').join('  ')} value={value.host ? t('web.together.youHost') : undefined}
            onPress={() => lobby.run(() => lobby.enter(lifetime => controller.open(value.id, lifetime), undefined))}/>)}
        </SectionCard>}
      </FocusRegion>
      {state.directoryError && <Banner message={state.directoryError} tone="warning"/>}
    </SettingsPageFrame>
    <Dialog testID="together-form" open={sheet !== undefined} onOpenChange={open => {if (!open) lobby.closeForm();}} placement="side"
      headerContext={<TogetherTVEyebrow>{t('entry.group.together')}</TogetherTVEyebrow>} title={t(sheet === 'join' ? 'web.together.joinWithCode' : 'web.together.startTitle')}
      description={t(sheet === 'join' ? 'web.together.joinHelp' : 'web.together.startHelp')} width={sheet === 'join' || typing ? spec.formWide : spec.formNarrow}
      initialFocusRef={typing ? undefined : lobby.firstTVField} returnFocusRef={sheet === 'join' ? lobby.joinRef : lobby.startRef}
      footer={<Button testID="together-submit" align="stretch" onFocus={() => {if (viewCurrent(form) && sheet === 'start') setTyping(undefined);}} loading={busy} disabled={!ready} onPress={submit}>
        {t(sheet === 'join' ? 'web.together.join' : 'web.together.start')}</Button>}>
      {state.error && <Banner message={state.error} tone="warning"/>}
      <TogetherTVForm region={{id: fieldsRegion, entry: sheet === 'join' ? 'code' : 'name', edges: typing ? {right: keyboard} : undefined}} fields={<>
        <TogetherTVField ref={lobby.firstTVField} focusId={sheet === 'join' ? 'code' : 'name'} testID={sheet === 'join' ? 'together-code' : 'together-name'}
          label={t(sheet === 'join' ? 'web.together.hostCode' : 'profile.name')} value={sheet === 'join' ? code : name} onPress={() => type(sheet === 'join' ? 'code' : 'name')}/>
        <TogetherTVField focusId="who" testID="together-display-name" label={t('web.together.yourName')} description={t('web.together.shownToOthers')} value={who} onPress={() => type('who')}/>
        {sheet === 'start' && <TogetherTVChoice label={t('web.together.whoControls')}>
          <Segmented testID="together-authority" accessibilityLabel={t('web.together.whoControls')} value={authority} onValueChange={value => {if (viewCurrent(form)) setAuthority(value as GroupControl);}}
            items={(['host', 'everyone'] as const).map(value => ({value, label: t(value === 'host' ? 'web.together.onlyMe' : 'web.together.everyone')}))}
            itemProps={item => ({testID: `together-authority-${item.value}`, onFocus: () => {if (viewCurrent(form)) setTyping(undefined);}})}/>
        </TogetherTVChoice>}
      </>} keyboard={typing && <NativeTVKeyboard id={keyboard} value={typing === 'code' ? code : typing === 'name' ? name : who} leftRegionId={fieldsRegion}
        width={frameSpec.search.keyboardWidth} testID="together-keyboard" activation={{visible: sheet !== undefined, intent: typingIntent, revision: typingRevision}}
        onChangeText={value => {if (!viewCurrent(form)) return; if (typing === 'code') setCode(value.toUpperCase().slice(0, 12)); else if (typing === 'name') setName(value.slice(0, 80)); else setWho(value.slice(0, 64));}}/>}/>
    </Dialog>
  </>;
}

/** The room (tv-more 14, 15), drawn by `TogetherPage` while the viewer is in a group. */
export function TogetherRoomPage(props: TogetherTVRoomProps) {
  const room = useTogetherTVRoom(props), {group, state, ids, controls, queueRegion, people, footer, members} = room;
  return <>
    <TogetherTVRoomFrame kicker={t('entry.group.together')} title={group.name}
      notices={(room.notice || room.cantSee) && <>
        {room.notice && <Banner message={room.notice} tone="warning"/>}
        {room.cantSee && <Banner message={t('web.together.refused')} tone="warning" action={{label: t('web.together.goAhead'), onPress: () => room.transport('goAhead')}}/>}
      </>}
      main={<>
        <TogetherTVNowCard art={room.art} away={room.away} label={room.stateLabel} title={String(room.currentTitle)}
          facts={[...(room.identity?.subtitle.map(part => part.text) ?? []), ...(room.elapsed ? [room.elapsed] : [])]} readiness={room.readiness.split(' · ')} progress={room.fraction}/>
        <TogetherTVControls region={{id: controls, entry: room.showTransport ? 'transport' : 'open-player', edges: {left: ids.rail, right: people, down: queueRegion},
          onLayout: room.entryRegion === controls ? room.enter : undefined}}>
          {room.showTransport && <Button focusId="transport" testID="together-transport" {...room.entryFocus('transport')} disabled={state.busy} icon={room.playing && !group.clock.waitUntil ? 'i-pause' : 'i-play'}
            onPress={() => room.transport(group.clock.waitUntil ? 'goAhead' : room.playing ? 'pause' : 'play')}>
            {t(group.clock.waitUntil ? 'web.together.playAnyway' : room.playing ? 'web.together.pauseAll' : 'web.together.playAll')}</Button>}
          {room.samePlayback && <Button focusId="open-player" testID="together-open-player" {...room.entryFocus('open-player')} variant="glass" icon="i-tv" onPress={room.openPlayer}>{t('web.together.openPlayer')}</Button>}
          {room.canClaimHost && <Button focusId="claim-host" testID="together-claim-host" preferredFocus disabled={state.busy} icon="i-people" onPress={() => room.transfer(group.you.memberId)}>{t('web.together.becomeHost')}</Button>}
          {!room.canControl && !room.canClaimHost && <TogetherTVNote>{t('web.together.pill.hostControls')}</TogetherTVNote>}
        </TogetherTVControls>
        <TogetherTVSection label={t('player.queue')}>
          <TogetherTVQueue region={{id: queueRegion, entry: room.entries[0]?.entryId, edges: {left: ids.rail, right: people, up: controls, down: footer}}}
            data={room.entries} keyOf={entry => entry.entryId} item={room.queueRow} onViewableItemsChanged={room.viewable} nowPlaying={t('web.together.nowPlaying')}
            empty={t(group.you.mayControl ? 'web.together.emptyQueueHost' : 'web.together.emptyQueue')}/>
        </TogetherTVSection>
      </>}
      side={<>
        <TogetherTVSection label={t('web.together.people', {count: members.length})}>
          <TogetherTVPeople region={{id: people, entry: members[0]?.id, edges: {left: controls, down: footer}}} data={members} keyOf={member => member.id}
            item={room.memberRow} privateLabel={t('together.privateViewer')}/>
        </TogetherTVSection>
        {group.you.host && <TogetherTVSection label={t('web.together.invite')}>
          <TogetherTVInviteCode code={room.validInvite && `${room.validInvite.code.slice(0, 4)} ${room.validInvite.code.slice(4)}`}
            note={room.validInvite && t('web.together.inviteHelpJoin', {uses: room.validInvite.joinsLeft, time: defaultI18n.time(room.validInvite.expiresAt)})}>
            <Button variant="glass" testID="together-new-code" disabled={state.busy} onPress={() => room.run(() => props.controller.invite(), live => live.you.host)}>
              {t(room.validInvite ? 'web.together.newCode' : 'web.together.getCode')}</Button>
          </TogetherTVInviteCode>
        </TogetherTVSection>}
      </>}
      footer={<TogetherTVFooter region={{id: footer, entry: 'leave', edges: {left: ids.rail, up: queueRegion}, onLayout: room.entryRegion === footer ? room.enter : undefined}}>
        <Button ref={room.leaveRef} focusId="leave" testID="together-leave" {...room.entryFocus('leave')} variant="glass" disabled={state.busy} onPress={room.askLeave}>{t('web.together.leave')}</Button>
        {group.you.host && <Button ref={room.endRef} testID="together-end" variant="glass" disabled={state.busy} onPress={room.askEnd}>{t('web.together.endAll')}</Button>}
      </TogetherTVFooter>}/>
    <Menu testID="together-queue-menu" open={!!room.queueMenu && !!room.currentQueue} onOpenChange={open => {if (!open) room.setQueueMenu(undefined);}} presentation="sideSheet"
      title={room.queueMenuTitle} returnFocusRef={room.menuReturn} items={room.queueMenuItems}/>
    <Menu testID="together-member-menu" open={room.memberMenuOpen} onOpenChange={open => {if (!open) room.setMemberMenu(undefined);}}
      presentation="sideSheet" title={room.memberMenuTitle} returnFocusRef={room.menuReturn} items={room.memberMenuItems}/>
    <Dialog testID="together-confirm" open={!!room.confirm} onOpenChange={open => {if (!open) room.setConfirm(undefined);}}
      title={t(room.confirm === 'end' ? 'confirm.endGroup.title' : 'confirm.leaveAsHost.title')} description={t(room.confirm === 'end' ? 'confirm.endGroup.body' : 'confirm.leaveAsHost.body')}
      initialFocusRef={room.cancel} returnFocusRef={room.confirmOpener === 'end' ? room.endRef : room.leaveRef}
      footer={<OverlayFootRow layout="end">
        <Button ref={room.cancel} focusId="cancel" variant="glass" onPress={() => room.setConfirm(undefined)}>{t('action.cancel')}</Button>
        <Button focusId="confirm" variant="danger" disabled={state.busy} onPress={room.confirmEnd}>{t(room.confirm === 'end' ? 'confirm.endGroup.action' : 'confirm.leaveAsHost.action')}</Button>
      </OverlayFootRow>}>{null}</Dialog>
  </>;
}
