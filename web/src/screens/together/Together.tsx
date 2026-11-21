import {useEffect, useMemo, useRef, useState, useSyncExternalStore, type FormEvent} from 'react';
import {defaultI18n} from '@i18n';
import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {itemSubject} from '../../platform/playback/subject';
import {Artwork, ProfileFace} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {Button, Segmented, TextField} from '../../kit/controls';
import {SectionCard, SettingsPageFrame, StateRow} from '../../kit/frames';
import {SettingsRow} from '../../kit/shell/SettingsSurfaces';
import {usePhone} from '../../kit/overlays/shared';
import {Banner, Dialog, Menu, type MenuEntry} from '../../kit/overlays';
import type {TogetherBindings} from './bindings';
import {watchClock} from '../watch/model';
import {memberAlias, memberName, memberStatusKey, togetherCanMakeHost, togetherHostNotice, togetherInviteVisible, togetherPlayLabel, togetherStateKey} from './together-model';
import styles from './together.module.css';

/** tv-more desktop19–21 and states27. Room, membership, item facts and actions are real. */
export function TogetherScreen({binding}: {binding: TogetherBindings}) {
  useEffect(() => {if (binding.isCurrent()) void binding.service.refreshDirectory();}, [binding]);
  const snapshot = useSyncExternalStore(binding.subscribe, binding.getSnapshot, binding.getSnapshot), room = snapshot.room, group = room.group;
  const playback = useSyncExternalStore(binding.runtime.subscribe, binding.runtime.getSnapshot, binding.runtime.getSnapshot);
  const phone = usePhone();
  const [sheet, setSheet] = useState<'start' | 'join' | null>(null);
  const [roomView, setRoomView] = useState(true);
  const t = binding.t, [name, setName] = useState(''), [displayName, setDisplayName] = useState(binding.profileName), [code, setCode] = useState('');
  const [authority, setAuthority] = useState<'host' | 'everyone'>('host'), [confirm, setConfirm] = useState<'leave' | 'end' | null>(null);
  const [menu, setMenu] = useState<string | null>(null), menuAnchor = useRef<HTMLButtonElement | null>(null), cancel = useRef<HTMLButtonElement | null>(null), trigger = useRef<HTMLButtonElement | null>(null);
  const inRoom = !!group && roomView && (room.phase === 'live' || room.phase === 'reconnecting'), queue = group?.queue;
  const hostNotice = group ? togetherHostNotice(group, binding.service.serverNow()) : undefined;
  const currentEntry = queue?.find(entry => entry.entryId === group?.clock.entryId);
  const current = inRoom && currentEntry?.itemId && currentEntry?.available ? snapshot.items.get(currentEntry?.itemId) : undefined;
  const identity = current ? playbackIdentity(itemSubject(current), 'full', binding.environment) : undefined;
  const art = current?.backdropUrl ?? current?.posterUrl;
  const source = useMemo(() => binding.artwork.foreground(art, 1120, 16 / 9, {group: 'playback', priority: 'normal'}), [binding, art]);
  const act = (action: () => unknown | Promise<unknown>) => {if (!binding.isCurrent() || group && binding.getSnapshot().room.group?.id !== group.id) return;try {void Promise.resolve(action()).catch(binding.reportError);} catch (error) {binding.reportError(error);}};
  const currentPlayback = !!group && !!playback.fence && binding.runtime.groupPlaybackMatches(playback.fence, group.id, group.clock.entryId ?? '');
  const openPlayer = () => {const value = binding.runtime.getSnapshot();if (group && value.fence && binding.runtime.groupPlaybackMatches(value.fence, group.id, group.clock.entryId ?? '')) binding.runtime.open(value.fence);};
  const ask = (kind: 'leave' | 'end', button: HTMLButtonElement) => {trigger.current = button;setConfirm(kind);};
  const entries: MenuEntry[] = group && queue && menu ? [
    {id: 'play', label: t('web.together.watchThis'), disabled: !group.you.mayControl, onSelect: () => act(() => binding.service.transport('watch', {entryId: menu}))},
    {id: 'remove', label: t('web.together.removeFromQueue'), disabled: !group.you.mayControl, onSelect: () => act(() => binding.service.queueRemove(menu))},
  ] : [];
  // The forms are the same fields wide and in the phone's sheet; wide, each is its own section card (desktop-settings rows).
  const joinFields = <>
    <TextField label={t('web.together.hostCode')} autoComplete="off" placeholder={t('web.together.codeExample')} value={code} onChange={event => setCode(event.target.value)} maxLength={32} required />
    {phone && <TextField label={t('web.together.yourName')} value={displayName} onChange={event => setDisplayName(event.target.value)} maxLength={80} required />}
    <Button type="submit" variant="primary" disabled={room.busy || !code.trim() || !displayName.trim()}>{t('web.together.join')}</Button></>;
  const startFields = <>
    <TextField label={t('builder.name')} value={name} onChange={event => setName(event.target.value)} maxLength={128} required />
    {phone && <TextField label={t('web.together.yourName')} description={t('web.together.shownToOthers')} value={displayName} onChange={event => setDisplayName(event.target.value)} maxLength={80} required />}
    <div className={styles.field}><span className={styles.fieldLabel} id="together-control">{t(phone ? 'web.together.whoControlsPhone' : 'web.together.whoControls')}</span>
      <Segmented aria-labelledby="together-control" value={authority} onValueChange={value => setAuthority(value === 'everyone' ? 'everyone' : 'host')} items={[{value: 'host', label: t('web.together.onlyMe')}, {value: 'everyone', label: t('web.together.everyone')}]} /></div>
    <Button type="submit" variant="primary" disabled={room.busy || !name.trim() || !displayName.trim()}>{t('web.together.start')}</Button></>;
  const submitJoin = (event: FormEvent) => {event.preventDefault();act(async () => {if (await binding.service.join(code, displayName)) {setSheet(null);setRoomView(true);}});};
  const submitStart = (event: FormEvent) => {event.preventDefault();act(async () => {if (await binding.service.create({name, displayName, control: authority})) {setSheet(null);setRoomView(true);}});};
  const shareButton = <Button size="small" icon="i-share" disabled={typeof navigator.share !== 'function' && !navigator.clipboard} onClick={() => act(() => typeof navigator.share === 'function' ? navigator.share({text: t('web.together.shareMessage', {code: group?.invitation?.code ?? ''})}) : navigator.clipboard.writeText(group?.invitation?.code ?? ''))}>{t('web.together.shareCode')}</Button>;
  if (!binding.isCurrent()) return null;
  const toLobby = () => {setRoomView(false);act(() => binding.service.refreshDirectory());};
  const notices = <>
    {inRoom && hostNotice && <Banner icon="i-warn">{t('web.together.hostDropped')} {t(hostNotice.key, {time: watchClock(hostNotice.seconds)})}{hostNotice.takeOver && <> {t('web.together.takeOver')}</>}
      {hostNotice.canClaim && <Button size="xs" variant="primary" className={styles.noticeAction} disabled={room.busy} onClick={() => act(() => binding.service.makeHost(group.you.memberId))}>{t('web.together.becomeHost')}</Button>}</Banner>}
    {(room.error || room.directoryError) && <Banner icon="i-warn">{room.error ?? room.directoryError}</Banner>}
    {inRoom && !!group.cantSee && <Banner icon="i-warn">{t('web.together.refused')}
      {group.you.mayControl && <Button size="xs" variant="primary" className={styles.noticeAction} disabled={room.busy} onClick={() => act(() => binding.service.transport('goAhead'))}>{t('web.together.goAhead')}</Button>}</Banner>}
    {room.phase === 'reconnecting' && <Banner icon="i-refresh">{t('web.together.reconnecting')}</Banner>}
  </>;
  return <main className={styles.page} data-together-screen>
    {source && <div className={styles.wash} aria-hidden="true"><Artwork source={source} alt="" /></div>}
    {/* A page shown alone on the settings page frame, as About is: "‹ Settings · Watch Together" on a phone; in a group, Back
        returns to the lobby and the group's name is the title. No mock-up draws it; it matches the Settings pages around it. */}
    <SettingsPageFrame title={inRoom ? group.name : t('web.settings.together')}
      back={inRoom ? {label: t('web.settings.together'), onPress: toLobby} : {label: t('web.shell.settings'), onPress: () => binding.openSettings()}}
      actions={inRoom ? <span className={styles.badge}>{t(togetherStateKey(group))}</span> : undefined}
      intro={!inRoom && room.phase !== 'ended' ? t('web.together.subtitle') : inRoom && group.you.host ? t('web.together.hostIntro') : undefined}>
      {notices}
      {room.phase === 'ended' ? <SectionCard><StateRow kind="empty" icon="i-together" title={t('web.together.endedTitle')}
          reason={t(group?.ended?.reason === 'ended_by_host' ? 'web.together.ended.host' : group?.ended?.reason === 'no_host' ? 'web.together.ended.away' : group?.ended?.reason === 'everyone_left' ? 'web.together.ended.empty' : 'web.together.ended.other')}
          action={{label: t('web.together.backToLobby'), onPress: () => act(() => {binding.service.dismiss();void binding.service.refreshDirectory();})}} /></SectionCard>
        : inRoom ? <div className={styles.room}>
          <div className={styles.column}>
            <div className={styles.now}><Artwork source={source} alt="" saturation={.85} brightness={.7} /><div className={styles.nowText}>
              <span className={styles.status}>{t(togetherStateKey(group))}</span><b>{identity?.title}</b><small>{identity?.subtitle.map(part => part.text).join(' ')}
                {currentPlayback && playback.presentation && <>{identity?.subtitle.length ? <i /> : null}{t('web.together.positionOf', {position: watchClock(playback.presentation.positionSeconds), duration: watchClock(playback.presentation.durationSeconds)})}</>}
              </small>{playback.presentation && currentPlayback && playback.presentation.durationSeconds > 0 && <div className={styles.progress}><i style={{width: `${Math.min(1, playback.presentation.positionSeconds / playback.presentation.durationSeconds) * 100}%`}} /></div>}
            </div></div>
            <div className={styles.actions}>{group.you.mayControl ? <Button variant="primary" icon={group.clock.playing && !group.clock.waitUntil ? 'i-pause' : 'i-play'} disabled={room.busy || !currentEntry?.itemId}
              onClick={() => act(() => binding.service.transport(group.clock.waitUntil ? 'goAhead' : group.clock.playing ? 'pause' : 'play'))}>{t(togetherPlayLabel(group))}</Button>
              : <span className={styles.hostControls}>{t('web.together.pill.hostControls')}</span>}
              {!phone && <Button onClick={openPlayer} disabled={!currentPlayback}>{t('web.together.openPlayer')}</Button>}
</div>
            {!phone && <p className={styles.readiness}>{group.clock.waitingFor === 0 ? t('web.together.allReady') : t('web.together.waiting', {ready: group.clock.ready, total: group.clock.ready + group.clock.waitingFor, count: group.clock.waitingFor})}</p>}
            {!phone && <SectionCard label={`${t('web.together.queue')}${queue ? ` · ${t('web.person.titles', {count: queue.length})}` : ''}`}>{queue?.map((entry, index) => {const raw = !!entry.available && entry.itemId ? snapshot.items.get(entry.itemId) : undefined;
              const item = raw ? playbackIdentity(itemSubject(raw), 'full', binding.environment) : undefined;
              return <div className={styles.row} key={entry.entryId}><span className={styles.number}>{entry.entryId === group.clock.entryId ? <Icon name="i-play" size={16} /> : binding.number(index + 1)}</span>
                <div className={styles.thumb}><Artwork source={binding.artwork.foreground(raw?.backdropUrl ?? raw?.posterUrl, 144, 16 / 9, {group: 'playback', priority: 'normal'})} alt="" /></div>
                <span className={styles.rowText}><b>{!entry.available ? t('web.together.notAvailable') : item?.title}</b><small>{item?.subtitle.map(part => part.text).join(' ')}</small></span>
                {entry.entryId === group.clock.entryId && <span className={styles.status}>{t('web.together.nowPlaying')}</span>}
                <Button variant="icon" size="small" icon="i-more" iconOnly aria-label={defaultI18n.t('player.more')} onClick={event => {menuAnchor.current = event.currentTarget;setMenu(entry.entryId);}} />
              </div>;})}{queue && !queue.length && <StateRow kind="empty" icon="i-queue" title={t('web.together.nothingChosen')} reason={t(group.you.mayControl ? 'web.together.emptyQueueHost' : 'web.together.emptyQueue')} />}</SectionCard>}
          </div>
          <div className={styles.column}>
            <SectionCard label={t('web.together.people', {count: group.members.length})}>{group.members.map(member => <div className={styles.row} key={member.id}>
              {member.private && member.id !== group.you.memberId ? <span className={styles.privateFace}><Icon name="i-person" size={18} /></span> : <ProfileFace initial={memberName(member).trim().slice(0, 1).toUpperCase()} art={member.id === group.you.memberId ? binding.profileArt : undefined} size={36} />}
              <span className={styles.rowText}><b>{member.id === group.you.memberId ? t('web.together.you', {name: memberName(member)}) : memberName(member)}</b>{(memberAlias(member) || member.host) && <small>{[memberAlias(member), member.host ? t('web.together.pill.host') : ''].filter(Boolean).join(' · ')}</small>}</span>
              {!phone && togetherCanMakeHost(group, member) && <Button size="xs" onClick={() => act(() => binding.service.makeHost(member.id))}>{t('web.together.makeHost')}</Button>}
              <span className={styles.status} data-waiting={member.status !== 'in_step'}>{t(memberStatusKey(member))}</span>
            </div>)}</SectionCard>
            {togetherInviteVisible(group) && <SectionCard label={t('web.together.invite')}><div className={styles.code}>
              {group.invitation ? <><b>{group.invitation.code.replace(/(.{4})(?=.)/g, '$1 ')}</b><p>{t('web.together.inviteHelpJoin', {uses: group.invitation.joinsLeft, time: new Intl.DateTimeFormat(binding.environment.locale ?? 'en-US', {hour: 'numeric', minute: '2-digit'}).format(new Date(group.invitation.expiresAt))})}</p>
                <div className={styles.actions}>{!phone && <Button size="small" icon="i-copy" disabled={!navigator.clipboard} onClick={() => act(() => navigator.clipboard.writeText(group?.invitation?.code ?? ''))}>{t('web.together.copyCode')}</Button>}
                  {phone && shareButton}
                  <Button size="small" disabled={room.busy} onClick={() => act(() => binding.service.invite())}>{t('web.together.newCode')}</Button>
                  {!phone && shareButton}</div></>
                : <Button size="small" onClick={() => act(() => binding.service.invite())}>{t('web.together.getCode')}</Button>}
            </div></SectionCard>}
            {!phone && <div className={styles.actions}><Button variant="text" onClick={event => group.you.host ? ask('leave', event.currentTarget) : act(() => binding.service.leave())}>{t('web.together.leave')}</Button>
              {group.you.host && <Button variant="text" onClick={event => ask('end', event.currentTarget)}>{t('web.together.endAll')}</Button>}</div>}
          </div>
        </div> : <>
          {!phone && <SectionCard><SettingsRow label={<span className={styles.you}><ProfileFace initial={displayName.trim().slice(0, 1).toUpperCase()} art={binding.profileArt} size={36} />{t('web.together.yourName')}</span>} help={t('web.together.shownToOthers')}>
            <TextField aria-label={t('web.together.yourName')} value={displayName} onChange={event => setDisplayName(event.target.value)} maxLength={80} /></SettingsRow></SectionCard>}
          {phone ? <SectionCard><div className={styles.phoneIntro}><b>{t('web.together.tagline')}</b><p>{t('web.together.taglineBody')}</p>
            <Button variant="primary" icon="i-together" onClick={() => setSheet('start')}>{t('web.together.startTitle')}</Button><Button icon="i-kbd" onClick={() => setSheet('join')}>{t('web.together.joinWithCode')}</Button></div></SectionCard>
            : <div className={styles.two}>
              <SectionCard label={t('web.together.joinTitle')} help={t('web.together.joinHelp')}><form className={styles.form} onSubmit={submitJoin}>{joinFields}</form></SectionCard>
              <SectionCard label={t('web.together.startTitle')} help={t('web.together.startHelp')}><form className={styles.form} onSubmit={submitStart}>{startFields}</form></SectionCard>
            </div>}
          {room.directory.length > 0 && <SectionCard label={t(phone ? 'web.together.yourGroupsPhone' : 'web.together.yourGroups')}>{room.directory.map(value => <button type="button" className={styles.groupRow} key={value.id} onClick={() => {setRoomView(true);act(() => binding.service.open(value.id));}}>
            <span className={styles.groupIcon}><Icon name="i-together" size={20} /></span><span className={styles.rowText}><b>{value.name}</b><small>{t('web.together.here', {count: value.members})}</small></span>
            {value.host && <span className={styles.hostTag}>{t('web.together.youHost')}</span>}<Icon name="i-right" size={18} /></button>)}</SectionCard>}
        </>}
    </SettingsPageFrame>
    <Dialog footerDismisses open={sheet !== null} onOpenChange={open => {if (!open) setSheet(null);}} title={t(sheet === 'join' ? 'web.together.joinWithCode' : 'web.together.startTitle')}>
      {sheet === 'join' ? <form className={styles.form} onSubmit={submitJoin}>{joinFields}</form> : <form className={styles.form} onSubmit={submitStart}><p className={styles.sheetHelp}>{t('web.together.startHelpPhone')}</p>{startFields}</form>}</Dialog>
    <Menu open={menu !== null} onOpenChange={open => {if (!open) setMenu(null);}} title={t('web.together.queue')} items={entries} anchorRef={menuAnchor} returnFocusRef={menuAnchor} />
    <Dialog footerDismisses open={confirm !== null} onOpenChange={open => {if (!open) setConfirm(null);}} title={t(confirm === 'end' ? 'confirm.endGroup.title' : 'confirm.leaveAsHost.title')}
      description={t(confirm === 'end' ? 'confirm.endGroup.body' : 'confirm.leaveAsHost.body')} initialFocusRef={cancel} returnFocusRef={trigger}
      footer={<><Button variant="glass" size="small" type="button" ref={cancel} onClick={() => setConfirm(null)}>{t('action.cancel')}</Button><Button variant="destructive" size="small" type="button" onClick={() => {const action = confirm;setConfirm(null);act(() => action === 'end' ? binding.service.end() : binding.service.leave());}}>{t(confirm === 'end' ? 'confirm.endGroup.action' : 'confirm.leaveAsHost.action')}</Button></>}>{null}</Dialog>
  </main>;
}
