import React from 'react';
import {Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import type {UpdatesSnapshot} from '../../../../../../packages/client-core/src/server-admin/updates-source';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {useTheme} from '../../../kit/theme';
import {alpha, shellType} from '../../../kit/shell/visual';
import type {Translate} from './model';

/** owner-dashboard 2b: what the update card says. Drawn only while the owner's cached release check reports a newer build. */
export type OwnerUpdateView = Readonly<{latest: string; current: string; notesURL?: string; checking: boolean}>;

/** Release notes open only an https page the server's report names; anything else leaves the button off. */
export function ownerUpdateView(snapshot: UpdatesSnapshot): OwnerUpdateView | undefined {
  const report = snapshot.report;
  if (!report?.updateAvailable || !report.latest) return undefined;
  const notes = /^https:\/\/[^\s]+$/i.test(report.latest.url) ? report.latest.url : undefined;
  return Object.freeze({latest: report.latest.version, current: report.current.version, ...(notes ? {notesURL: notes} : {}), checking: snapshot.busy});
}

/** The update's own tinted card at the end of Alerts. Portico never installs an update by itself: the card says so, and its
 * only actions are the release notes and asking the server to check again. */
export function OwnerUpdateCard({update, t, onReleaseNotes, onCheck}: {update: OwnerUpdateView; t: Translate; onReleaseNotes: () => void; onCheck: () => void}) {
  const theme = useTheme();
  return <View testID="owner-update-card" accessibilityLabel={t('next.shell.updateAvailable')} style={{padding: 16, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised, borderWidth: 1, borderColor: alpha(theme.colors.accentStrong, .28)}}>
    <Svg pointerEvents="none" style={{position: 'absolute', left: 0, top: 0, right: 0, bottom: 0}} width="100%" height="100%">
      <Defs><LinearGradient id="owner-update-tint" x1="0%" y1="0%" x2="100%" y2="58%"><Stop offset="0" stopColor={theme.colors.accent} stopOpacity=".24"/><Stop offset=".72" stopColor={theme.colors.accent} stopOpacity=".05"/></LinearGradient></Defs>
      <Rect width="100%" height="100%" fill="url(#owner-update-tint)"/>
    </Svg>
    <View style={{flexDirection: 'row', gap: 14}}>
      <View style={{width: 38, height: 38, borderRadius: 19, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.accent, .2)}}><Icon name="i-arrow-up" size={20} color={theme.colors.accentStrong}/></View>
      <View style={{flex: 1, minWidth: 0}}>
        <Text accessibilityRole="header" style={[shellType(15.5, 700, 20.2), {color: theme.colors.text, marginTop: 1}]}>{t('next.shell.updateAvailable')}</Text>
        <Text style={[shellType(13.5, 400, 19), {color: theme.colors.textSecondary, marginTop: 3}]}>{[t('next.shell.version', {version: update.latest}), t('next.shell.youHaveVersion', {version: update.current})].join(' · ')}</Text>
        <Text style={[shellType(13.5, 400, 19.6), {color: theme.colors.textTertiary, marginTop: 6}]}>{t('web.maintenance.updatesLede')}</Text>
      </View>
    </View>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 6, marginTop: 12}}>
      {update.notesURL ? <Button testID="owner-update-notes" variant="glass" size="xs" icon="i-ext" onPress={onReleaseNotes}>{t('web.maintenance.releaseNotes')}</Button> : null}
      <Button testID="owner-update-check" variant="text" size="xs" loading={update.checking} disabled={update.checking} onPress={onCheck}>{t('server.updates.check')}</Button>
    </View>
  </View>;
}
