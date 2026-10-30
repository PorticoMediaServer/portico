import React, {useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View, findNodeHandle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {GroupSessionSnapshot} from '../../../../../packages/client-core/src/group-session';
import {Button, Focusable} from '../../kit/controls';
import {ProfileFace} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {SidePanel} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';
import {browseType} from '../../kit/browse/presentation';
import {groupPort, subscribeGroupPort, type NativeGroupPort} from './group-port';
import {memberNames} from './together-presentation';

type Opening = Readonly<{owner: NativeGroupPort; groupId: string; viewer: string}>;

/** tv-more18: the player's top-row group pill. It draws only what the live owner reports —
 * the group's own name, its joined members and the service's permissions — and its people
 * sheet sends host actions through that owner's existing command. No membership copy. */
export function NativeTVGroupPill({onHost, onSheetChange}: {onHost?: (host: number | undefined) => void; onSheetChange?: (open: boolean) => void} = {}) {
  const theme = useTheme(), t = defaultI18n.t;
  // The registration slot changes when an owner appears, retires or is replaced…
  const owner = useSyncExternalStore(subscribeGroupPort, groupPort, () => undefined);
  // …and the registered owner's own stream carries every membership, permission and state
  // change, so this surface retires or redraws with the session rather than on a later render.
  const subscribeOwner = useCallback((listener: () => void) => owner ? owner.subscribe(listener) : () => {}, [owner]);
  const readOwner = useCallback((): GroupSessionSnapshot | undefined => owner && owner.current() ? owner.getSnapshot() : undefined, [owner]);
  const snapshot = useSyncExternalStore(subscribeOwner, readOwner, () => undefined);
  // An opening owns its original registration, group and viewer. Never rebind an existing
  // opening to the next render's tuple, even when the same group later returns.
  const [sheet, setSheet] = useState<Opening | undefined>(), sheetRef = useRef<Opening | undefined>(undefined), mounted = useRef(false), pill = useRef<View>(null);
  const group = snapshot?.phase === 'live' || snapshot?.phase === 'reconnecting' ? snapshot.group : undefined;
  if (sheetRef.current && (sheetRef.current.owner !== owner || sheetRef.current.groupId !== group?.id || sheetRef.current.viewer !== group?.viewerMemberId)) sheetRef.current = undefined;
  const currentSheet = sheet && sheetRef.current === sheet ? sheet : undefined;
  const closeSheet = (opening: Opening | undefined = sheetRef.current) => {
    if (!opening || sheetRef.current !== opening) return;
    sheetRef.current = undefined;
    setSheet(previous => previous === opening ? undefined : previous);
  };
  const openSheet = () => {
    if (!mounted.current || !owner || !group || groupPort() !== owner || !owner.current()) return;
    const live = owner.getSnapshot();
    if (live.phase !== 'live' || live.group?.id !== group.id || live.group.viewerMemberId !== group.viewerMemberId) return;
    const opening = Object.freeze({owner, groupId: group.id, viewer: group.viewerMemberId});
    sheetRef.current = opening; setSheet(opening);
  };
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false; sheetRef.current = undefined;};}, []);
  useLayoutEffect(() => {onSheetChange?.(!!currentSheet); return () => onSheetChange?.(false);}, [currentSheet, onSheetChange]);
  // Report the real native host only while this pill is drawn; the seek bar uses it as nextFocusUp.
  useLayoutEffect(() => {
    if (!onHost) return;
    onHost(group ? findNodeHandle(pill.current) ?? undefined : undefined);
    return () => onHost(undefined);
  }, [group, onHost]);
  if (!group || !owner) return null;
  const joined = group.members.filter(member => member.state === 'joined');
  const memberName = (member: (typeof joined)[number]) => memberNames(member, t('together.privateViewer')).primary;
  const waiting = joined.find(member => member.id !== group.viewerMemberId && member.readiness === 'buffering');
  // Drawn states come only from the service's own readiness, presence and room phase.
  const pillState = snapshot?.phase === 'reconnecting' ? t('web.together.pill.reconnecting')
    : group.host.presence !== 'connected' && !group.permissions.isHost ? t('web.together.pill.hostAway')
    : waiting ? t('web.together.pill.waitingFor', {name: memberName(waiting)})
    : group.readiness.aggregate === 'ready' ? t('web.together.pill.inStep') : t('web.together.catchingUp');
  const statusColor = snapshot?.phase === 'reconnecting' || group.readiness.aggregate !== 'ready' || group.host.presence !== 'connected' ? theme.colors.amber : theme.colors.healthy;
  const face = (member: (typeof joined)[number], size: number) => member.private
    ? <View style={{width: size, height: size, borderRadius: size / 2, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedHigh}}><Icon name="i-user" size={size / 2} color={theme.colors.textSecondary}/></View>
    : <ProfileFace initial={[...member.displayName.trim()][0] ?? ''} size={size} scaleOnFocus={false}/>;
  const memberState = (member: (typeof joined)[number]) => member.readiness === 'buffering' ? t('web.together.loading')
    : member.readiness === 'lagging' ? t('web.together.catchingUp')
    : member.presence !== 'connected' ? t('web.together.away') : t('web.together.inStep');
  const permissions = group.permissions, canClaim = !permissions.isHost && permissions.canClaimHost === true;
  /** Whole eligibility is re-read from this owner's current group at dispatch and completion. */
  const eligible = (state: GroupSessionSnapshot | undefined, memberId: string, opening: Opening): boolean => {
    const live = state?.phase === 'live' ? state.group : undefined;
    if (!live || live.id !== opening.groupId || live.viewerMemberId !== opening.viewer) return false;
    const member = live.members.find(value => value.id === memberId);
    if (!member || member.state !== 'joined') return false;
    return memberId === live.viewerMemberId
      ? !live.permissions.isHost && live.permissions.canClaimHost === true
      : live.permissions.isHost && member.role !== 'host' && memberId !== live.viewerMemberId;
  };
  const makeHost = (memberId: string, opening: Opening | undefined) => {
    if (!opening) return;
    const port = opening.owner;
    const stillOurs = () => mounted.current && sheetRef.current === opening && groupPort() === port && port.current()
      && eligible(port.getSnapshot(), memberId, opening);
    if (!stillOurs()) {closeSheet(opening); return;}
    void port.makeHost(memberId).then(() => {
      // Only this opening may be closed by its own completion; a later sheet stays open.
      if (!mounted.current || sheetRef.current !== opening) return;
      if (!stillOurs()) closeSheet(opening);
    }, () => {});
  };
  return <>
    <Focusable ref={pill} testID="player-group-pill" focusId="player-group-pill" accessibilityRole="button"
      accessibilityLabel={t('web.together.pill.label', {name: group.name, count: joined.length, status: pillState})} onPress={openSheet} focusRadius={32}
      style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'center', gap: 14, height: 64, maxWidth: 380, paddingLeft: 10, paddingRight: 26,
        borderRadius: 32, backgroundColor: 'rgba(7,16,24,.5)', boxShadow: 'inset 0 0 0 1px rgba(237,245,250,.12)'}}>
      <View accessible={false} style={{flexDirection: 'row'}}>{joined.slice(0, 3).map((member, index) => <View key={member.id} style={{marginLeft: index ? -11 : 0, borderRadius: 21, boxShadow: '0 0 0 2.5px #0b1720'}}>{face(member, 42)}</View>)}</View>
      <View style={{minWidth: 0, flexShrink: 1}}>
        <Text numberOfLines={1} style={[browseType(theme, 22, 700), {lineHeight: 26 * theme.textScale, color: theme.colors.text}]}>{group.name}</Text>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
          <View style={{width: 9, height: 9, borderRadius: 4.5, backgroundColor: statusColor}}/>
          <Text numberOfLines={1} style={[browseType(theme, 20, 600), {lineHeight: 24 * theme.textScale, color: statusColor, flexShrink: 1}]}>{pillState}</Text>
        </View>
      </View>
    </Focusable>
    <SidePanel open={currentSheet !== undefined} onOpenChange={next => {if (!next) closeSheet(currentSheet);}} title={group.name}
      testID="player-group-sheet" width={620} topInset={0} returnFocusRef={pill}
      style={{paddingTop: 46, paddingLeft: 56, paddingRight: 96}}>
      <View style={{gap: 18}}>
        <Text testID="player-group-people" style={[browseType(theme, 20, 600), {color: theme.colors.textSecondary}]}>
          {t('web.together.pill.people', {count: joined.length})}
        </Text>
        {joined.map(member => {
          const names = memberNames(member, t('together.privateViewer'));
          const primary = member.private || member.id !== group.viewerMemberId ? names.primary : t('web.together.you', {name: names.primary});
          return <View key={member.id} style={{flexDirection: 'row', alignItems: 'center', gap: 20, height: 88, paddingHorizontal: 22, borderRadius: 16}}>
            {face(member, 64)}
            <View style={{flex: 1, minWidth: 0}}>
              <Text style={[browseType(theme, 22, 700), {color: theme.colors.text}]}>
                {primary}
              </Text>
              {names.secondary && <Text numberOfLines={1} style={[browseType(theme, 18, 600), {color: theme.colors.textSecondary}]}>
                {names.secondary}
              </Text>}
            </View>
            <Text style={[browseType(theme, 18, 600), {color: member.role === 'host' ? theme.colors.accentStrong : theme.colors.textTertiary}]}>
              {member.role === 'host' ? t('web.together.hostStatus', {status: memberState(member)}) : memberState(member)}
            </Text>
            {permissions.isHost && member.role !== 'host' && <Button variant="glass" size="small" focusId={`group-host-${member.id}`}
              onPress={() => makeHost(member.id, currentSheet)}>{t('web.together.makeHost')}</Button>}
          </View>;})}
        {canClaim && <Button variant="primary" focusId="group-claim-host" hasTVPreferredFocus onPress={() => makeHost(group.viewerMemberId, currentSheet)}>
          {t('web.together.becomeHost')}</Button>}
        {!permissions.canControl && <Text testID="player-group-host-controls" style={[browseType(theme, 18, 600), {color: theme.colors.textTertiary}]}>
          {t('web.together.pill.hostControls')}</Text>}
      </View>
    </SidePanel>
  </>;
}
