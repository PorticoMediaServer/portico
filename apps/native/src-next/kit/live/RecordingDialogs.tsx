import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {activeRecording, playableRecording} from '../../../../../packages/client-core/src/dvr';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {keepChoiceList, keepChoiceValue, withKeep} from '../../screens/settings-live/recording-rule-presentation';
import {Button, Chip, Focusable, Select, Switch} from '../controls';
import {Icon} from '../Icon';
import {Dialog} from '../overlays';
import {StateView} from '../states';
import {useTheme} from '../theme';

import {useRecordingDetails, useRecordingRuleEditor, type RecordingDetailsProps, type RuleEditorProps} from '../../pages/shared/live/useRecordingDialogs';
import {reasonLabels, stateLabels} from '../../pages/shared/live/useRecordings';

export function RecordingDetails(props: RecordingDetailsProps) {
  const {recording, controller, runtime, opener, onClose, onPlay, onRemove, deletionAvailable, isCurrent} = props;
  const theme = useTheme();
  const {t, state, conflict, primary, opened, current, detail, error, overlapCurrent, runResolve} = useRecordingDetails(props);
  return <Dialog open onOpenChange={value => {if (!value && !controller.getSnapshot().busy) onClose();}} title={conflict ? t('web.dvr.conflictTitle', {title: recording.programme.title}) : recording.programme.title} placement={theme.isTV || theme.width >= 700 ? 'side' : undefined} width={theme.isTV ? 640 : undefined} initialFocusRef={primary} returnFocusRef={opener} testID="recordings-details">
    <View style={{gap: 24}}>{error !== undefined && <StateView testID="recordings-details-error" {...runtime.error(error, onClose, 'live')}/>}{!detail && error === undefined && <StateView title={t('recordings.loading')}/>}
      {detail && <><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t(stateLabels[detail.recording.state])}</Text><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t('guide.range', {start: defaultI18n.time(detail.recording.start), end: defaultI18n.time(detail.recording.end)})}</Text>{reasonLabels[detail.recording.reason] && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t(reasonLabels[detail.recording.reason])}</Text>}
        {conflict && <><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t(detail.capacity.known ? 'recordings.conflictBodyTuners' : 'recordings.conflictBody', {count: detail.capacity.effective})}</Text>{(overlapCurrent?.snapshot.items ?? detail.overlaps).map(value => <View key={value.id} style={{flexDirection: 'row', gap: 20, alignItems: 'center'}}><Icon name="i-rec" size={20}/><View style={{flex: 1}}><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{value.title}</Text></View><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{defaultI18n.time(value.start)}</Text></View>)}{overlapCurrent && <>
          {overlapCurrent.snapshot.error !== undefined && <StateView {...runtime.error(overlapCurrent.snapshot.error, () => {void overlapCurrent.window.retry(overlapCurrent.snapshot);}, 'live')} testID="recordings-overlap-error"/>}
          {overlapCurrent.snapshot.refreshRequired && <StateView {...runtime.error({code: 'stale_continuation'}, onClose, 'live')} testID="recordings-overlap-refresh"/>}
          {overlapCurrent.snapshot.chunks[0]?.cursor && <Button focusId="recordings-overlap-previous" testID="recordings-overlap-previous" onPress={() => {void overlapCurrent.window.previous(overlapCurrent.snapshot);}}>{t('web.pager.previous')}</Button>}
          {(overlapCurrent.snapshot.nextCursor || overlapCurrent.snapshot.pending) && <Focusable key="recordings-overlap-next" focusId="recordings-overlap-next" testID="recordings-overlap-next" disabled={overlapCurrent.snapshot.pending} focusableWhenDisabled onPress={() => {if (!opened.live || !isCurrent()) return; void (overlapCurrent.snapshot.error !== undefined ? overlapCurrent.window.retry(overlapCurrent.snapshot) : overlapCurrent.window.next(overlapCurrent.snapshot));}}><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{t(overlapCurrent.snapshot.error !== undefined ? 'next.states.tryAgain' : 'next.settings.loadMore')}</Text></Focusable>}
        </>}<Button testID="recordings-conflict-record" ref={primary} disabled={state.busy || !current?.resolver?.canResolve(detail)} loading={state.busy} onPress={() => {if (!opened.live || !isCurrent() || !current?.resolver?.canResolve(detail)) return; runResolve(current.resolver, detail);}} icon="i-rec">{t('recordings.recordThis')}</Button><Button testID="recordings-conflict-skip" variant="glass" disabled={state.busy} onPress={() => {if (!opened.live || !isCurrent()) return; void controller.cancel(detail.recording).then(result => {if (result && opened.live && isCurrent()) onClose();});}}>{t('recordings.skipThis')}</Button></>}
        {!conflict && <>{playableRecording(detail.recording) && <Button testID="recordings-details-play" ref={primary} onPress={onPlay} icon="i-play">{t('action.play')}</Button>}{activeRecording(detail.recording) && <Button testID="recordings-details-cancel" ref={!playableRecording(detail.recording) ? primary : undefined} variant="glass" disabled={state.busy} onPress={onRemove}>{t('recordings.cancelAction')}</Button>}{deletionAvailable && !activeRecording(detail.recording) && <Button testID="recordings-details-delete" variant="glass" textStyle={{color: theme.colors.record}} style={{backgroundColor: `${theme.colors.record}29`}} disabled={state.busy} onPress={onRemove}>{t('web.dvr.deleteRecording')}</Button>}</>}
      </>}
    </View>
  </Dialog>;
}

export function RuleEditor(props: RuleEditorProps) {
  const {rule, controller, runtime, opener} = props;
  const theme = useTheme(), tv = theme.isTV;
  const {t, state, config, setConfig, deleting, setDeleting, future, setFuture, save, cancel, keep, facts, current, close, offsets, minutes, write, remove} = useRecordingRuleEditor(props);
  const rowStyle = {minHeight: theme.targets.touch, borderRadius: theme.baseRadii.control, paddingHorizontal: theme.space.sm, backgroundColor: theme.colors.raised};
  const rowText = theme.type.rowLabel;
  return <><Dialog open={!deleting} onOpenChange={value => {if (!value) close();}} title={rule.config.name}
    headerContext={<View style={{flexDirection: 'row', alignItems: 'center', gap: 16, marginBottom: tv ? 18 : 12}}><Icon name="i-repeatrule" size={tv ? 26 : 20} color={theme.colors.accentStrong}/><Text style={[theme.type.rowLabel, {color: theme.colors.accentStrong}]}>{t('recordings.seriesRule')}</Text></View>}
    placement={tv || theme.width >= 700 ? 'side' : undefined} width={tv ? 760 : undefined} initialFocusRef={keep} returnFocusRef={opener} testID="recordings-rule-editor">
    <ScrollView contentContainerStyle={{gap: 12}}>{state.error !== undefined && <StateView testID="recordings-error" {...runtime.error(state.error, () => {void controller.load();}, 'live')}/>}
      {rule.summary && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 22, marginBottom: tv ? 18 : 6}}><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{t('recordings.matches', {count: facts.upcoming})}</Text><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('recordings.recordedSoFar', {count: facts.recorded})}</Text></View>}
      <View style={[rowStyle, {flexDirection: 'row', alignItems: 'center', gap: 20}]}><Text style={[theme.type.listLabel, {color: theme.colors.text, flex: 1}]}>{t('recordings.ruleIsOn')}</Text><Switch testID="recordings-rule-enabled" accessibilityLabel={t('recordings.ruleIsOn')} checked={config.enabled} onCheckedChange={enabled => setConfig(value => ({...value, enabled}))}/></View>
      <View style={{flexDirection: 'row', gap: 12}}>{(['new', 'all'] as const).map(episodes => <Chip key={episodes} testID={`recordings-rule-episodes-${episodes}`} selected={config.episodes === episodes} onPress={() => setConfig(value => ({...value, episodes}))} style={{flex: 1, justifyContent: 'center'}}>{t(episodes === 'new' ? 'recordings.newEpisodes' : 'recordings.allEpisodes')}</Chip>)}</View>
      <Select ref={keep} testID="recordings-rule-keep" rowLabel={t('recordings.keepLabel')} accessibilityLabel={t('recordings.keepLabel')} value={keepChoiceValue(config.options)} options={keepChoiceList(runtime.text, config.options)} onValueChange={value => setConfig(current => ({...current, options: withKeep(current.options, value)}))} style={rowStyle} textStyle={rowText} pickerContext={<View style={{flexDirection: 'row', alignItems: 'center', gap: 16}}><Icon name="i-repeatrule" size={26} color={theme.colors.accentStrong}/><Text style={[theme.type.rowLabel, {color: theme.colors.accentStrong}]}>{t('recordings.seriesRule')}</Text></View>}/>
      <Select testID="recordings-rule-before" rowLabel={t('recordings.startEarly')} accessibilityLabel={t('recordings.startEarly')} value={String(config.options.beforeSeconds)} options={offsets.map(value => ({value: String(value), label: minutes(value)}))} onValueChange={value => setConfig(current => ({...current, options: {...current.options, beforeSeconds: Number(value)}}))} style={rowStyle} textStyle={rowText}/>
      <Select testID="recordings-rule-after" rowLabel={t('recordings.endLate')} accessibilityLabel={t('recordings.endLate')} value={String(config.options.afterSeconds)} options={offsets.map(value => ({value: String(value), label: minutes(value)}))} onValueChange={value => setConfig(current => ({...current, options: {...current.options, afterSeconds: Number(value)}}))} style={rowStyle} textStyle={rowText}/>
      <View style={{gap: 12, marginTop: tv ? 16 : 8}}><Button testID="recordings-rule-save" ref={save} disabled={state.busy} loading={state.busy} onPress={() => {void write(config);}}>{t('action.save')}</Button><Button testID="recordings-rule-toggle" variant="glass" disabled={state.busy} onPress={() => {void write({...config, enabled: !config.enabled});}} icon={config.enabled ? 'i-pause' : 'i-play'}>{t(config.enabled ? 'recordings.pauseRule' : 'recordings.resumeRule')}</Button><Button testID="recordings-rule-delete-open" variant="glass" textStyle={{color: theme.colors.record}} style={{backgroundColor: `${theme.colors.record}29`}} disabled={state.busy} onPress={() => setDeleting(true)} icon="i-trash">{t('web.dvr.deleteRule')}</Button></View>
    </ScrollView>
  </Dialog>
    <Dialog open={deleting} onOpenChange={value => {if (!value && current() && !controller.getSnapshot().busy) setDeleting(false);}} title={t('recordings.deleteRuleTitle', {name: rule.config.name})} description={t('recordings.deleteRuleBody')} width={theme.isTV ? 980 : undefined} initialFocusRef={cancel} returnFocusRef={opener} testID="recordings-rule-delete" footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 16}}><Button testID="recordings-rule-delete-cancel" ref={cancel} variant="glass" disabled={state.busy} onPress={() => {if (current() && !controller.getSnapshot().busy) setDeleting(false);}}>{t('action.cancel')}</Button><Button testID="recordings-rule-delete-confirm" variant="glass" textStyle={{color: theme.colors.record}} style={{backgroundColor: `${theme.colors.record}29`}} disabled={state.busy} loading={state.busy} onPress={() => {void remove();}}>{t('recordings.deleteRule')}</Button></View>}><View style={{gap: 18}}><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{t('web.dvr.upcomingChoice')}</Text><View style={{flexDirection: 'row', gap: 16}}>{(['keep', 'cancel'] as const).map(value => <Chip key={value} testID={`recordings-rule-delete-future-${value}`} selected={future === value} disabled={state.busy} onPress={() => setFuture(value)} style={{flex: 1, justifyContent: 'center'}}>{t(value === 'keep' ? 'recordings.keepUpcoming' : 'recordings.cancelUpcoming')}</Chip>)}</View></View></Dialog>
  </>;
}

export function RecordingConfirm({open, onClose, cancel, returnFocus, busy, title, description, cancelLabel, confirmLabel, onConfirm, children}: {
  open: boolean; onClose: () => void; cancel: React.RefObject<View | null>; returnFocus: React.RefObject<View | null>; busy: boolean;
  title: string; description?: string; cancelLabel: string; confirmLabel: string; onConfirm: () => void; children?: React.ReactNode;
}) {
  const theme = useTheme();
  return <Dialog open={open} onOpenChange={value => {if (!value) onClose();}} title={title} description={description} width={theme.isTV ? 980 : undefined}
    initialFocusRef={cancel} returnFocusRef={returnFocus} testID="recordings-confirm" footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: theme.space.sm}}>
      <Button testID="recordings-confirm-cancel" ref={cancel} variant="glass" disabled={busy} onPress={onClose}>{cancelLabel}</Button>
      <Button testID="recordings-confirm-delete" variant="glass" textStyle={{color: theme.colors.record}} disabled={busy} loading={busy} onPress={onConfirm}>{confirmLabel}</Button>
    </View>}>{children}</Dialog>;
}
