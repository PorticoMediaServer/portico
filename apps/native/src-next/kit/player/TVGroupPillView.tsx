import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable} from '../controls';
import {ProfileFace} from '../artwork';
import {Icon} from '../Icon';
import {SidePanel} from '../overlays';
import {textLines, useTheme} from '../theme';
import {browseType} from '../browse/presentation';
import type {useTVGroupPill, TVGroupPillMember} from '../../pages/shared/player/useTVGroupPill';

export type TVGroupPillViewProps = NonNullable<ReturnType<typeof useTVGroupPill>>;

/** tv-more18: the player's top-row group pill (faces, the group's name and its state) and its people sheet (each member,
 * host status, Make host, Become host). Draws only what `useTVGroupPill` reads from the live owner. */
export function TVGroupPillView({group, joined, pill, pillState, statusColor, memberState, rowNames, you, canClaim, sheetOpen, openSheet, closeSheet, makeHost}: TVGroupPillViewProps) {
  const theme = useTheme(), t = defaultI18n.t;
  const face = (member: TVGroupPillMember, size: number) => member.private
    ? <View style={{width: size, height: size, borderRadius: size / 2, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedHigh}}><Icon name="i-user" size={size / 2} color={theme.colors.textSecondary}/></View>
    : <ProfileFace initial={[...(member.name ?? member.username ?? '').trim()][0] ?? ''} size={size} scaleOnFocus={false}/>;
  return <>
    <Focusable ref={pill} testID="player-group-pill" focusId="player-group-pill" accessibilityRole="button"
      accessibilityLabel={t('web.together.pill.label', {name: group.name, count: joined.length, status: pillState})} onPress={openSheet} focusRadius={32}
      style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'center', gap: 14, minHeight: 64, maxWidth: 380, paddingLeft: 10, paddingRight: 26,
        borderRadius: 32, backgroundColor: 'rgba(7,16,24,.5)', boxShadow: 'inset 0 0 0 1px rgba(237,245,250,.12)'}}>
      <View accessible={false} style={{flexDirection: 'row'}}>{joined.slice(0, 3).map((member, index) => <View key={member.id} style={{marginLeft: index ? -11 : 0, borderRadius: 21, boxShadow: '0 0 0 2.5px #0b1720'}}>{face(member, 42)}</View>)}</View>
      <View style={{minWidth: 0, flexShrink: 1}}>
        <Text {...textLines(1)} style={[browseType(theme, 22, 700, 0, 26), {color: theme.colors.text}]}>{group.name}</Text>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
          <View style={{width: 9, height: 9, borderRadius: 4.5, backgroundColor: statusColor}}/>
          <Text {...textLines(1)} style={[browseType(theme, 20, 600, 0, 24), {color: statusColor, flexShrink: 1}]}>{pillState}</Text>
        </View>
      </View>
    </Focusable>
    <SidePanel open={sheetOpen} onOpenChange={next => {if (!next) closeSheet();}} title={group.name}
      testID="player-group-sheet" width={620} topInset={0} returnFocusRef={pill}
      style={{paddingTop: 46, paddingLeft: 56, paddingRight: 96}}>
      <View style={{gap: 18}}>
        <Text testID="player-group-people" style={[browseType(theme, 20, 600), {color: theme.colors.textSecondary}]}>
          {t('web.together.pill.people', {count: joined.length})}
        </Text>
        {joined.map(member => {
          const names = rowNames(member);
          return <View key={member.id} style={{flexDirection: 'row', alignItems: 'center', gap: 20, height: 88, paddingHorizontal: 22, borderRadius: 16}}>
            {face(member, 64)}
            <View style={{flex: 1, minWidth: 0}}>
              <Text style={[browseType(theme, 22, 700), {color: theme.colors.text}]}>
                {names.primary}
              </Text>
              {names.secondary && <Text {...textLines(1)} style={[browseType(theme, 18, 600), {color: theme.colors.textSecondary}]}>
                {names.secondary}
              </Text>}
            </View>
            <Text style={[browseType(theme, 18, 600), {color: member.host ? theme.colors.accentStrong : theme.colors.textTertiary}]}>
              {member.host ? t('web.together.hostStatus', {status: memberState(member)}) : memberState(member)}
            </Text>
            {you.host && !member.host && member.status === 'in_step' && <Button variant="glass" size="small" focusId={`group-host-${member.id}`}
              onPress={() => makeHost(member.id)}>{t('web.together.makeHost')}</Button>}
          </View>;})}
        {canClaim && <Button variant="primary" focusId="group-claim-host" preferredFocus onPress={() => makeHost(you.memberId)}>
          {t('web.together.becomeHost')}</Button>}
        {!you.mayControl && <Text testID="player-group-host-controls" style={[browseType(theme, 18, 600), {color: theme.colors.textTertiary}]}>
          {t('web.together.pill.hostControls')}</Text>}
      </View>
    </SidePanel>
  </>;
}
