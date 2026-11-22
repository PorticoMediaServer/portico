import {useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {findNodeHandle, type View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {RoomSessionSnapshot} from '../../../../../../packages/client-core/src/room-session';
import {useTheme} from '../../../kit/theme';
import {groupPort, subscribeGroupPort, type NativeGroupPort} from '../../../screens/settings-live/group-port';
import {memberNames} from '../../../screens/settings-live/together-presentation';

type Opening = Readonly<{owner: NativeGroupPort; groupId: string; viewer: string}>;
type LiveGroup = NonNullable<RoomSessionSnapshot['group']>;
export type TVGroupPillMember = LiveGroup['members'][number];

/** tv-more18: the player's top-row group pill. It reads only what the live owner reports — the group's own name, its
 * joined members and the service's permissions — and its people sheet sends host actions through that owner's existing
 * command. `TVGroupPillView` draws what this returns. */
export function useTVGroupPill({onHost, onSheetChange}: {onHost?: (host: number | undefined) => void; onSheetChange?: (open: boolean) => void} = {}) {
  const theme = useTheme(), t = defaultI18n.t;
  // The registration slot changes when an owner appears, retires or is replaced…
  const owner = useSyncExternalStore(subscribeGroupPort, groupPort, () => undefined);
  // …and the registered owner's own stream carries every membership, permission and state
  // change, so this surface retires or redraws with the session rather than on a later render.
  const subscribeOwner = useCallback((listener: () => void) => owner ? owner.subscribe(listener) : () => {}, [owner]);
  const readOwner = useCallback((): RoomSessionSnapshot | undefined => owner && owner.current() ? owner.getSnapshot() : undefined, [owner]);
  const snapshot = useSyncExternalStore(subscribeOwner, readOwner, () => undefined);
  // An opening owns its original registration, group and viewer. Never rebind an existing
  // opening to the next render's tuple, even when the same group later returns.
  const [sheet, setSheet] = useState<Opening | undefined>(), sheetRef = useRef<Opening | undefined>(undefined), mounted = useRef(false), pill = useRef<View>(null);
  const group = snapshot?.phase === 'live' || snapshot?.phase === 'reconnecting' ? snapshot.group : undefined;
  if (sheetRef.current && (sheetRef.current.owner !== owner || sheetRef.current.groupId !== group?.id || sheetRef.current.viewer !== group?.you.memberId)) sheetRef.current = undefined;
  const currentSheet = sheet && sheetRef.current === sheet ? sheet : undefined;
  const closeSheet = (opening: Opening | undefined = sheetRef.current) => {
    if (!opening || sheetRef.current !== opening) return;
    sheetRef.current = undefined;
    setSheet(previous => previous === opening ? undefined : previous);
  };
  const openSheet = () => {
    if (!mounted.current || !owner || !group || groupPort() !== owner || !owner.current()) return;
    const live = owner.getSnapshot();
    if (live.phase !== 'live' || live.group?.id !== group.id || live.group.you.memberId !== group.you.memberId) return;
    const opening = Object.freeze({owner, groupId: group.id, viewer: group.you.memberId});
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
  if (!group || !owner) return undefined;
  const joined = group.members;
  const memberName = (member: TVGroupPillMember) => memberNames(member, t('together.privateViewer')).primary;
  const waiting = joined.find(member => member.id !== group.you.memberId && member.status === 'buffering');
  // Drawn states come only from the service's own readiness, presence and room phase.
  const pillState = snapshot?.phase === 'reconnecting' ? t('web.together.pill.reconnecting')
    : !group.host.present && !group.you.host ? t('web.together.pill.hostAway')
    : waiting ? t('web.together.pill.waitingFor', {name: memberName(waiting)})
    : group.clock.waitingFor === 0 ? t('web.together.pill.inStep') : t('web.together.catchingUp');
  const statusColor = snapshot?.phase === 'reconnecting' || group.clock.waitingFor > 0 || !group.host.present ? theme.colors.amber : theme.colors.healthy;
  const memberState = (member: TVGroupPillMember) => member.status === 'buffering' ? t('web.together.loading')
    : member.status === 'catching_up' ? t('web.together.catchingUp')
    : member.status === 'away' ? t('web.together.away') : t('web.together.inStep');
  /** A member's row names: the private viewer stays unnamed; your own row says it is you. */
  const rowNames = (member: TVGroupPillMember) => {
    const names = memberNames(member, t('together.privateViewer'));
    const primary = member.private || member.id !== group.you.memberId ? names.primary : t('web.together.you', {name: names.primary});
    return {primary, secondary: names.secondary};
  };
  const you = group.you, canClaim = !you.host && you.mayBecomeHost;
  /** Whole eligibility is re-read from this owner's current group at dispatch and completion. */
  const eligible = (state: RoomSessionSnapshot | undefined, memberId: string, opening: Opening): boolean => {
    const live = state?.phase === 'live' ? state.group : undefined;
    if (!live || live.id !== opening.groupId || live.you.memberId !== opening.viewer) return false;
    const member = live.members.find(value => value.id === memberId);
    if (!member) return false;
    return memberId === live.you.memberId
      ? !live.you.host && live.you.mayBecomeHost === true
      : live.you.host && !member.host && member.status === 'in_step' && memberId !== live.you.memberId;
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
  return {group, joined, pill, pillState, statusColor, memberState, rowNames, you, canClaim, sheetOpen: currentSheet !== undefined,
    openSheet, closeSheet: () => closeSheet(currentSheet), makeHost: (memberId: string) => makeHost(memberId, currentSheet)};
}
