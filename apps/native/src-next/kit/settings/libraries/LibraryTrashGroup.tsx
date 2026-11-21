import React from 'react';
import {Text, View} from 'react-native';
import type {ServerRequester} from '../../../../../../packages/client-core/src/query/server-api';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {Button} from '../../controls/Button';
import {Select} from '../../controls/Select';
import {TextField} from '../../controls/TextField';
import {BottomSheet} from '../../overlays/BottomSheet';
import {SettingsRow} from '../../shell';
import {shellType} from '../../shell/visual';
import {useTheme} from '../../theme';
import {PanelBlock, SectionCard} from '../../frames';
import {useOwnerLibraryTrash} from '../../../pages/shared/settings/useOwnerLibraryTrash';

type Translate = typeof defaultI18n.t;
const RULES = [['days', 'next.ownerLibraries.missingRuleDays'], ['scan', 'next.ownerLibraries.missingRuleScan'], ['manual', 'next.ownerLibraries.missingRuleManual']] as const;

/**
 * A library's missing titles, its trash (03-scanning rules 59 to 62), as the web's LibraryTrash draws it: the library's own
 * rule for emptying it, the list with when each went missing and when it goes, and Empty trash once the count is typed.
 * A rule that would remove more at the next scan says how many first (discourage, don't block).
 */
export function LibraryTrashGroup({api, libraryId, parentCurrent, t}: {api: ServerRequester; libraryId: string; parentCurrent: () => boolean; t: Translate}) {
  const theme = useTheme();
  const {trash, failed, busy, days, setDays, dayCount, daysValid, listOpen, setListOpen, confirming, setConfirming,
    typed, setTyped, removed, setRemoved, asking, setAsking, label, removal, choose, save, empty} = useOwnerLibraryTrash({api, libraryId, parentCurrent, t});
  const body = [theme.type.rowHelp, {color: theme.colors.textSecondary}];
  return <>
    <SectionCard label={t('next.ownerLibraries.missingTitle')}>
      <SettingsRow first label={label('1.label')} description={label('1.help')} control={trash && <Select testID="owner-libraries-trash-rule" accessibilityLabel={label('1.label')} value={trash.rule.mode} disabled={busy}
        options={RULES.map(([value, key]) => ({value, label: t(key)}))} onValueChange={mode => {if (trash && (mode === 'days' || mode === 'scan' || mode === 'manual')) choose({mode, days: trash.rule.days});}}/>}/>
      {trash?.rule.mode === 'days' && <SettingsRow label={t('next.ownerLibraries.missingRuleDaysLabel')} control={<View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
        <TextField focusId="owner-libraries-trash-days" value={days} onChangeText={setDays} keyboardType="number-pad" editable={!busy} accessibilityLabel={t('next.ownerLibraries.missingRuleDaysLabel')} containerStyle={{width: 76}}/>
        <Button size="xs" variant="glass" disabled={busy || !daysValid || dayCount === trash.rule.days} onPress={() => choose({mode: 'days', days: dayCount})}>{t('next.settings.apply')}</Button>
      </View>}/>}
      <SettingsRow navigates label={label('0.label')} description={label('0.help')} value={trash ? defaultI18n.number(trash.total) : undefined} onPress={() => {if (parentCurrent()) {setRemoved(undefined); setListOpen(true);}}}/>
    </SectionCard>
    {failed && <Text accessibilityRole="alert" style={[shellType(13, 600, 18), {color: theme.colors.record, marginTop: 8, paddingHorizontal: 4}]}>{t('next.states.pageFailedBody')}</Text>}
    <BottomSheet open={listOpen && !confirming} onOpenChange={setListOpen} title={t('next.ownerLibraries.missingTitle')} description={t('next.ownerLibraries.missingLede')} scrollable
      footer={<View style={{flexDirection: 'row', gap: 10}}>
        {trash && trash.total > 0 && <Button size="small" variant="danger" disabled={busy} onPress={() => {setTyped(''); setConfirming(true);}} style={{flex: 1}}>{t('next.ownerLibraries.emptyTrash')}</Button>}
        <Button variant="glass" size="small" onPress={() => setListOpen(false)}>{t('action.close')}</Button></View>}>
      <PanelBlock inset={false}>
        {removed !== undefined && <Text accessibilityRole="alert" style={body}>{t('next.ownerLibraries.emptyTrashDone', {count: removed})}</Text>}
        {!trash && !failed && <Text style={body}>{t('next.settings.loading')}</Text>}
        {trash && !trash.items.length && <Text style={body}>{t('next.ownerLibraries.missingNone', {library: trash.name})}</Text>}
        {trash?.items.map((item, index) => <View key={item.id} style={{paddingVertical: 10, borderTopWidth: index ? 1 : 0, borderTopColor: theme.colors.lineSoft}}>
          <Text style={[shellType(15, 600, 20), {color: theme.colors.text}]}>{item.year ? t('next.ownerLibraries.missingTitleYear', {title: item.title, year: String(item.year)}) : item.title}</Text>
          <Text style={[shellType(12.5, 500, 17.5), {color: theme.colors.textTertiary, marginTop: 3}]}>{t('next.ownerLibraries.missingSince', {date: defaultI18n.date(item.missingSince)})}</Text>
          {removal(item.removedAt) && <Text style={[shellType(12.5, 500, 17.5), {color: theme.colors.textTertiary}]}>{removal(item.removedAt)}</Text>}
        </View>)}
        {trash && trash.total > trash.items.length && <Text style={[body, {marginTop: 8}]}>{t('next.ownerLibraries.missingMore', {shown: trash.items.length, count: trash.total})}</Text>}
      </PanelBlock>
    </BottomSheet>
    <BottomSheet open={confirming && !!trash} onOpenChange={value => {if (!value) setConfirming(false);}} title={t('next.ownerLibraries.emptyTrashTitle')}
      description={trash ? t('next.ownerLibraries.emptyTrashBody', {count: trash.total, library: trash.name}) : undefined}
      footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" size="small" disabled={busy} onPress={() => setConfirming(false)}>{t('action.cancel')}</Button>
        <Button variant="danger" size="small" loading={busy} disabled={busy || !trash || typed.trim() !== String(trash.total)} onPress={empty} style={{flex: 1}}>{t('next.ownerLibraries.emptyTrash')}</Button></View>}>
      <TextField focusId="owner-libraries-trash-confirm" value={typed} onChangeText={setTyped} keyboardType="number-pad" editable={!busy}
        accessibilityLabel={trash ? t('next.ownerLibraries.emptyTrashType', {count: trash.total}) : ''} placeholder={trash ? t('next.ownerLibraries.emptyTrashType', {count: trash.total}) : ''}/>
    </BottomSheet>
    <BottomSheet open={!!asking && !!trash} onOpenChange={value => {if (!value) setAsking(undefined);}} title={asking ? t('next.ownerLibraries.ruleRemovesTitle', {count: asking.count}) : ''}
      description={asking && trash ? t('next.ownerLibraries.ruleRemovesBody', {count: asking.count, library: trash.name}) : undefined}
      footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" size="small" disabled={busy} onPress={() => setAsking(undefined)}>{t('action.cancel')}</Button>
        <Button variant="danger" size="small" disabled={busy} onPress={() => {if (asking) save(asking.rule);}} style={{flex: 1}}>{t('next.ownerLibraries.ruleRemovesConfirm')}</Button></View>}>
      {null}
    </BottomSheet>
  </>;
}
