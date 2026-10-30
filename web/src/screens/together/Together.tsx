import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {itemSubject} from '../../platform/playback/subject';
import {Artwork, ProfileFace} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {Button} from '../../kit/controls';
import {usePhone} from '../../kit/overlays/shared';
import {Dialog, Menu, type MenuEntry} from '../../kit/overlays';
import type {TogetherBindings} from './bindings';
import type {Group} from '@core/social-playback.ts';
import {watchClock} from '../watch/model';
import {memberAlias, memberName, togetherCanMakeHost, togetherHostNotice, togetherInviteVisible, togetherPlayLabel, togetherStateKey} from './together-model';
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
  const [authority, setAuthority] = useState<'host-only' | 'anyone'>('host-only'), [confirm, setConfirm] = useState<'leave' | 'end' | null>(null);
  const [menu, setMenu] = useState<string | null>(null), menuAnchor = useRef<HTMLButtonElement | null>(null), cancel = useRef<HTMLButtonElement | null>(null), trigger = useRef<HTMLButtonElement | null>(null);
  const inRoom = !!group && roomView && (room.phase === 'live' || room.phase === 'reconnecting'), queue = room.queue ?? group?.queue;
  const hostNotice = group ? togetherHostNotice(group, binding.service.serverNow()) : undefined;
  const currentEntry = queue?.entries.find(entry => entry.entryId === group?.timeline.currentEntryId);
  const current = inRoom && group?.timeline.itemId && !currentEntry?.unavailable ? snapshot.items.get(group.timeline.itemId) : undefined;
  const identity = current ? playbackIdentity(itemSubject(current), 'full', binding.environment) : undefined;
  const art = current?.backdropUrl ?? current?.posterUrl;
  const source = useMemo(() => binding.artwork.foreground(art, 1120, 16 / 9, {group: 'playback', priority: 'normal'}), [binding, art]);
  const act = (action: () => unknown | Promise<unknown>) => {if (!binding.isCurrent() || group && binding.getSnapshot().room.group?.id !== group.id) return;try {void Promise.resolve(action()).catch(binding.reportError);} catch (error) {binding.reportError(error);}};
  const currentPlayback = !!group && !!playback.fence && binding.runtime.groupPlaybackMatches(playback.fence, group.id, group.timeline.currentEntryId);
  const openPlayer = () => {const value = binding.runtime.getSnapshot();if (group && value.fence && binding.runtime.groupPlaybackMatches(value.fence, group.id, group.timeline.currentEntryId)) binding.runtime.open(value.fence);};
  const ask = (kind: 'leave' | 'end', button: HTMLButtonElement) => {trigger.current = button;setConfirm(kind);};
  const entries: MenuEntry[] = group && queue && menu ? [
    {id: 'play', label: t('web.together.watchThis'), disabled: !group.permissions.canControl, onSelect: () => act(() => binding.service.transport('set-queue-position', {entryId: menu}))},
    {id: 'remove', label: t('web.together.removeFromQueue'), disabled: !group.permissions.canManageQueue, onSelect: () => act(() => binding.service.queueRemove(menu))},
  ] : [];
  const joinForm = (<form className={styles.box} onSubmit={event => {event.preventDefault();act(async () => {if (await binding.service.join(code, displayName)) {setSheet(null);setRoomView(true);}});}}>{!phone && <><h2>{t('web.together.joinTitle')}</h2><p>{t('web.together.joinHelp')}</p></>}
              <label>{t('web.together.hostCode')}<input autoComplete="off" placeholder={t('web.together.codeExample')} value={code} onChange={event => setCode(event.target.value)} maxLength={32} required /></label>
              {phone && <label>{t('web.together.yourName')}<input value={displayName} onChange={event => setDisplayName(event.target.value)} maxLength={80} required /></label>}<button type="submit" className={styles.primary} disabled={room.busy || !code.trim() || !displayName.trim()}>{t('web.together.join')}</button></form>);
  const shareButton = <button type="button" disabled={typeof navigator.share !== 'function' && !navigator.clipboard} onClick={() => act(() => typeof navigator.share === 'function' ? navigator.share({text: t('web.together.shareMessage', {code: room.invite!.code})}) : navigator.clipboard.writeText(room.invite!.code))}><Icon name="i-share" size={15} />{t('web.together.shareCode')}</button>;
  const startForm = (<form className={styles.box} onSubmit={event => {event.preventDefault();act(async () => {if (await binding.service.create({name, displayName, hostAuthority: authority})) {setSheet(null);setRoomView(true);}});}}>{!phone && <h2>{t('web.together.startTitle')}</h2>}<p>{t(phone ? 'web.together.startHelpPhone' : 'web.together.startHelp')}</p>
              <label>{t('builder.name')}<input value={name} onChange={event => setName(event.target.value)} maxLength={128} required /></label>
              {phone && <label>{t('web.together.yourName')}<input value={displayName} onChange={event => setDisplayName(event.target.value)} maxLength={80} required /><small className={styles.fieldHint}>{t('web.together.shownToOthers')}</small></label>}<fieldset><legend>{t(phone ? 'web.together.whoControlsPhone' : 'web.together.whoControls')}</legend><div className={styles.segment}>{(['host-only', 'anyone'] as const).map(value => <button type="button" key={value} aria-pressed={authority === value} onClick={() => setAuthority(value)}>{t(value === 'host-only' ? 'web.together.onlyMe' : 'web.together.everyone')}</button>)}</div></fieldset>
              <button type="submit" className={styles.primary} disabled={room.busy || !name.trim() || !displayName.trim()}>{t('web.together.start')}</button></form>);
  if (!binding.isCurrent()) return null;
  return <main className={styles.page} data-together-screen>
    {source && <div className={styles.wash} aria-hidden="true"><Artwork source={source} alt="" /></div>}
    {phone && <div className={styles.mobileNav}><button type="button" onClick={() => {if (inRoom) {setRoomView(false);act(() => binding.service.refreshDirectory());} else binding.openSettings();}} aria-label={t(inRoom ? 'web.together.backToLobby' : 'web.shell.settings')}><Icon name="i-left" size={24} />{!inRoom && t('web.shell.settings')}</button><b>{inRoom ? group.name : t('web.settings.together')}</b></div>}
    <div className={styles.content}>
      {inRoom && <div className={styles.kind}>{t('web.settings.together')}</div>}
      <h1>{inRoom ? group.name : t('web.settings.together')}{inRoom && <span className={styles.badge}>{t(togetherStateKey(group))}</span>}</h1>
      {!inRoom && room.phase !== 'ended' && <p className={styles.lead}>{t('web.together.subtitle')}</p>}
      {inRoom && group.permissions.isHost && <p className={styles.lead}>{t('web.together.hostIntro')}</p>}
      {inRoom && hostNotice && <div className={styles.notice} role="status"><Icon name="i-warn" size={20} /><span>{t('web.together.hostDropped')} {t(hostNotice.key, {time: watchClock(hostNotice.seconds)})}{hostNotice.takeOver && <> {t('web.together.takeOver')}</>}</span>{hostNotice.canClaim && <button type="button" disabled={room.busy} onClick={() => act(() => binding.service.makeHost(group.viewerMemberId))}>{t('web.together.becomeHost')}</button>}</div>}
      {(room.error || room.directoryError) && <div className={styles.notice} role="status"><Icon name="i-warn" size={20} />{room.refused ? t('web.together.refused') : room.error ?? room.directoryError}
        {room.refused && group?.permissions.isHost && <button type="button" onClick={() => act(() => binding.service.transport(room.refused!.command, {...room.refused!.input, allowUnavailable: true}))}>{t('web.together.goAhead')}</button>}
      </div>}
      {room.phase === 'reconnecting' && <div className={styles.notice} role="status"><Icon name="i-refresh" size={20} />{t('web.together.reconnecting')}</div>}
      {room.phase === 'ended' ? <section className={styles.ended}><Icon name="i-together" size={40} /><h2>{t('web.together.endedTitle')}</h2>
        <p>{t(group?.endedReason === 'host-ended' ? 'web.together.ended.host' : group?.endedReason === 'host-unavailable' ? 'web.together.ended.away' : group?.endedReason === 'empty' ? 'web.together.ended.empty' : 'web.together.ended.other')}</p>
        <button className={styles.primary} type="button" onClick={() => act(() => {binding.service.dismiss();void binding.service.refreshDirectory();})}>{t('web.together.backToLobby')}</button></section>
        : inRoom ? <div className={styles.room}>
          <section>
            <div className={styles.now}><Artwork source={source} alt="" saturation={.85} brightness={.7} /><div className={styles.nowText}>
              <span className={styles.status}>{t(togetherStateKey(group))}</span><b>{identity?.title}</b><small>{identity?.subtitle.map(part => part.text).join(' ')}
                {currentPlayback && playback.presentation && <>{identity?.subtitle.length ? <i /> : null}{t('web.together.positionOf', {position: watchClock(playback.presentation.positionSeconds), duration: watchClock(playback.presentation.durationSeconds)})}</>}
              </small>{playback.presentation && currentPlayback && playback.presentation.durationSeconds > 0 && <div className={styles.progress}><i style={{width: `${Math.min(1, playback.presentation.positionSeconds / playback.presentation.durationSeconds) * 100}%`}} /></div>}
            </div></div>
            <div className={styles.actions}>{group.permissions.canControl ? <button type="button" className={styles.primary} disabled={room.busy || !group.timeline.itemId}
              onClick={() => act(() => binding.service.transport(group.timeline.state === 'playing' ? 'pause' : 'play'))}><Icon name={group.timeline.state === 'playing' ? 'i-pause' : 'i-play'} size={18} />{t(togetherPlayLabel(group))}</button>
              : <span className={styles.hostControls}>{t('web.together.pill.hostControls')}</span>}
              {!phone && <button type="button" onClick={openPlayer} disabled={!currentPlayback}>{t('web.together.openPlayer')}</button>}</div>
            {!phone && <p className={styles.readiness}>{t('web.together.waiting', {ready: group.readiness.ready, total: group.readiness.memberCount, count: Math.max(0, group.readiness.memberCount - group.readiness.ready)})}</p>}
            {!phone && <><h3>{t('web.together.queue')}<small>{queue ? t('web.person.titles', {count: queue.entries.length}) : ''}</small></h3>
            <div className={styles.list}>{queue?.entries.map(entry => {const raw = !entry.unavailable && entry.itemId ? snapshot.items.get(entry.itemId) : undefined;
              const item = raw ? playbackIdentity(itemSubject(raw), 'full', binding.environment) : undefined;
              return <div className={styles.queueRow} key={entry.entryId}><span className={styles.number}>{entry.entryId === group.timeline.currentEntryId ? <Icon name="i-play" size={16} /> : binding.number(entry.position + 1)}</span>
                <div className={styles.thumb}><Artwork source={binding.artwork.foreground(raw?.backdropUrl ?? raw?.posterUrl, 144, 16 / 9, {group: 'playback', priority: 'normal'})} alt="" /></div>
                <span className={styles.rowText}><b>{entry.unavailable ? t('web.together.notAvailable') : item?.title}</b><small>{item?.subtitle.map(part => part.text).join(' ')}</small></span>
                {entry.entryId === group.timeline.currentEntryId && <span className={styles.status}>{t('web.together.nowPlaying')}</span>}
                <button type="button" className={styles.more} aria-label={defaultI18n.t('player.more')} onClick={event => {menuAnchor.current = event.currentTarget;setMenu(entry.entryId);}}><Icon name="i-more" size={18} /></button>
              </div>;})}</div></>}
          </section>
          <section><h3>{t('web.together.people', {count: group.readiness.memberCount})}</h3><div className={styles.list}>
            {group.members.filter(member => member.state === 'joined').map(member => <div className={styles.person} key={member.id}>
              {member.private && member.id !== group.viewerMemberId ? <span className={styles.privateFace}><Icon name="i-person" size={18} /></span> : <ProfileFace initial={memberName(member).trim().slice(0, 1).toUpperCase()} art={member.id === group.viewerMemberId ? binding.profileArt : undefined} size={36} />}
              <span className={styles.rowText}>{member.id === group.viewerMemberId ? t('web.together.you', {name: memberName(member)}) : memberName(member)}{(memberAlias(member) || member.role === 'host') && <small>{[memberAlias(member), member.role === 'host' ? t('web.together.pill.host') : ''].filter(Boolean).join(' · ')}</small>}</span>
              {!phone && togetherCanMakeHost(group, member) && <button type="button" onClick={() => act(() => binding.service.makeHost(member.id))}>{t('web.together.makeHost')}</button>}
              <span className={styles.status} data-waiting={member.readiness !== 'ready'}>{t(member.presence !== 'connected' ? 'web.together.away' : member.readiness === 'ready' ? 'web.together.inStep' : member.readiness === 'buffering' ? 'web.together.loading' : 'web.together.catchingUp')}</span>
            </div>)}
          </div>{togetherInviteVisible(group) && <><h3 className={styles.inviteHeading}>{t('web.together.invite')}</h3><div className={styles.code}>
            {room.invite ? <><b>{room.invite.code.replace(/(.{4})(?=.)/g, '$1 ')}</b><p>{t('web.together.inviteHelpJoin', {uses: Math.max(0, room.invite.maxUses - room.invite.uses), time: new Intl.DateTimeFormat(binding.environment.locale ?? 'en-US', {hour: 'numeric', minute: '2-digit'}).format(new Date(room.invite.expiresAt))})}</p>
              <div className={styles.actions}>{!phone && <button type="button" disabled={!navigator.clipboard} onClick={() => act(() => navigator.clipboard.writeText(room.invite!.code))}><Icon name="i-copy" size={15} />{t('web.together.copyCode')}</button>}
                {phone && shareButton}
                <button type="button" disabled={room.busy} onClick={() => act(() => binding.service.invite())}>{t('web.together.newCode')}</button>
                {!phone && shareButton}</div></>
              : <button type="button" onClick={() => act(() => binding.service.invite())}>{t('web.together.getCode')}</button>}
          </div></>}{!phone && <div className={styles.actions}><button type="button" className={styles.ghost} onClick={event => group.permissions.isHost ? ask('leave', event.currentTarget) : act(() => binding.service.leave())}>{t('web.together.leave')}</button>
            {group.permissions.isHost && <button type="button" className={styles.ghost} onClick={event => ask('end', event.currentTarget)}>{t('web.together.endAll')}</button>}</div>}</section>
        </div> : <>
          <div className={styles.you}><ProfileFace initial={displayName.trim().slice(0, 1).toUpperCase()} art={binding.profileArt} size={44} /><span><b>{t('web.together.yourName')}</b><small>{t('web.together.shownToOthers')}</small></span>
            <input aria-label={t('web.together.yourName')} value={displayName} onChange={event => setDisplayName(event.target.value)} maxLength={80} /></div>
          {phone ? <div className={styles.phoneIntro}><div className={styles.faces} aria-hidden="true">{room.directory.flatMap(value => value.members.filter(member => member.state === 'joined')).slice(0, 3).map(member => member.private ? <span className={styles.privateFace} key={member.id}><Icon name="i-person" size={18} /></span> : <ProfileFace key={member.id} initial={member.displayName.trim().slice(0, 1).toUpperCase()} size={32} />)}</div><h2>{t('web.together.tagline')}</h2><p>{t('web.together.taglineBody')}</p><button type="button" className={styles.primary} onClick={() => setSheet('start')}><Icon name="i-together" size={20} />{t('web.together.startTitle')}</button><button type="button" onClick={() => setSheet('join')}><Icon name="i-kbd" size={20} />{t('web.together.joinWithCode')}</button></div> : <div className={styles.two}>
            {joinForm}
            {startForm}
          </div>}<section className={styles.groups}><h2>{t(phone ? 'web.together.yourGroupsPhone' : 'web.together.yourGroups')}</h2>{room.directory.map(value => <button type="button" className={styles.groupRow} key={value.id} onClick={() => {setRoomView(true);act(() => binding.service.open(value.id));}}>
            <span className={styles.groupIcon}><Icon name="i-together" size={20} /></span><span className={styles.rowText}><b>{value.name}</b><small>{t('web.together.here', {count: value.readiness.memberCount})}<i />{t(togetherStateKey(value))}</small></span>
            {value.permissions.isHost && <span>{t('web.together.youHost')}</span>}<Icon name="i-right" size={18} /></button>)}</section>
        </>}
    </div>
    <Dialog open={sheet !== null} onOpenChange={open => {if (!open) setSheet(null);}} title={t(sheet === 'join' ? 'web.together.joinWithCode' : 'web.together.startTitle')}>{sheet === 'join' ? joinForm : startForm}</Dialog>
    <Menu open={menu !== null} onOpenChange={open => {if (!open) setMenu(null);}} title={t('web.together.queue')} items={entries} anchorRef={menuAnchor} returnFocusRef={menuAnchor} />
    <Dialog open={confirm !== null} onOpenChange={open => {if (!open) setConfirm(null);}} title={t(confirm === 'end' ? 'confirm.endGroup.title' : 'confirm.leaveAsHost.title')}
      description={t(confirm === 'end' ? 'confirm.endGroup.body' : 'confirm.leaveAsHost.body')} initialFocusRef={cancel} returnFocusRef={trigger}
      footer={<><Button variant="glass" size="small" type="button" ref={cancel} onClick={() => setConfirm(null)}>{t('action.cancel')}</Button><Button variant="glass" size="small" type="button" className={styles.danger} onClick={() => {const action = confirm;setConfirm(null);act(() => action === 'end' ? binding.service.end() : binding.service.leave());}}>{t(confirm === 'end' ? 'confirm.endGroup.action' : 'confirm.leaveAsHost.action')}</Button></>}>{null}</Dialog>
  </main>;
}
