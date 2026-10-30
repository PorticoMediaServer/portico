import React, {useEffect, useRef, useState} from 'react';
import {StyleSheet, View, type TextInput} from 'react-native';
import {fieldSpec} from '@portico/design';
import type {RepairFieldSpec} from '../../../../../packages/client-core/src/metadata-repair';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {validBulkListEntry} from '../../../../../packages/client-core/src/metadata-bulk';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {TextField} from '../controls/TextField';
import {Select} from '../controls/Select';
import {Switch} from '../controls/Switch';
import {Icon} from '../Icon';
import {focusTarget} from '../overlays/OverlayHost';
import type {FocusTarget} from '../overlays/types';
import {useTheme} from '../theme';
import {selectionText} from './copy';
import {selectionControlID as controlID} from './focus-ids';
import {bulkFieldHint, bulkFieldStateLabel, changeList, changeScalar, failedSelectionIds, removeFieldChange, setListChange} from './presentation';
import {selectionFooterProps, SelectionSurface, SelectionText} from './surface';
import type {BulkEditorProps} from './types';

type FieldProps = BulkEditorProps & {field: RepairFieldSpec; fieldRef?: (node: View | TextInput | null) => void};
function ListField({field, fieldRef, ...props}: FieldProps) {
  const {colors, isTV, type} = useTheme();
  const [draft, setDraft] = useState('');
  const change = props.changes[field.field];
  const list = change && 'add' in change ? change : {add: [], remove: []};
  const current = props.values[field.field];
  const existing = current?.entries ?? [];
  const values = [...existing.map(entry => ({...entry, added: false})), ...list.add.filter(value => !existing.some(entry => entry.value === value)).map(value => ({value, count: props.count, added: true}))];
  const addLabel = field.field === 'genres' ? defaultI18n.t('next.selection.addGenre') : field.field === 'tags' ? defaultI18n.t(values.length ? 'next.selection.addTag' : 'next.selection.emptyTag') : field.field === 'labels' ? defaultI18n.t(values.length ? 'next.selection.addLabel' : 'next.selection.emptyLabel') : selectionText('next.selection.native.addFieldValue', {field: props.fieldLabel(field)});
  const add = () => {const value = draft.trim(); if (validBulkListEntry(value) && !props.pending) {props.onChangesChange(changeList(props.changes, field.field, value, 'add')); setDraft('');}};
  return <View style={{gap: 6}}>
    <View style={styles.chips}>
      {values.map(entry => {const removed = list.remove.includes(entry.value); const added = list.add.includes(entry.value);return <Focusable key={entry.value} testID={controlID(props.testID, 'field', field.field, 'value-toggle', entry.value)} accessibilityRole="button" accessibilityLabel={selectionText(removed ? 'next.selection.native.restoreValue' : 'next.selection.native.removeValue', {value: entry.value})} disabled={props.pending}
        focusKind="chip" focusRadius={isTV ? 12 : 15} onPress={() => {
          if (entry.added) props.onChangesChange(setListChange(props.changes, field.field, list.add.filter(value => value !== entry.value), list.remove));
          else if (removed) props.onChangesChange(setListChange(props.changes, field.field, list.add, list.remove.filter(value => value !== entry.value)));
          else props.onChangesChange(changeList(props.changes, field.field, entry.value, 'remove'));
        }} style={{height: isTV ? 56 : 28, maxWidth: '100%', borderRadius: isTV ? 12 : 15, paddingLeft: isTV ? 20 : 10, paddingRight: isTV ? 20 : 6, flexDirection: 'row', alignItems: 'center', gap: isTV ? 8 : 6, borderWidth: 1, borderStyle: entry.count < props.count && !added ? 'dashed' : 'solid', borderColor: removed ? 'rgba(241,121,121,.45)' : added ? 'rgba(92,179,226,.7)' : colors.line,
          backgroundColor: removed || entry.count < props.count && !added ? 'transparent' : added ? 'rgba(64,159,212,.24)' : colors.raisedBright}}>
        <SelectionText size={isTV ? 22 : 12} weight={700} numberOfLines={1} style={{flexShrink: 1, textDecorationLine: removed ? 'line-through' : 'none', color: removed ? colors.textTertiary : colors.text}}>{entry.value}</SelectionText>
        <SelectionText size={isTV ? 20 : 12} secondary>{added ? selectionText('next.selection.native.new') : entry.count}</SelectionText><Icon name={removed ? 'i-undo' : 'i-x'} size={isTV ? 22 : 12} color={colors.textTertiary} />
      </Focusable>;})}
      <View style={{minWidth: 100, flex: 1, ...(isTV ? {width: '100%', flexBasis: '100%'} as const : {})}}><TextField appearance="selection" testID={controlID(props.testID, 'field', field.field, 'add-input')} ref={fieldRef} accessibilityLabel={addLabel} value={draft} onChangeText={setDraft} onSubmitEditing={add} editable={!props.pending} placeholder={addLabel} maxLength={field.maxLength} returnKeyType="done" fieldStyle={isTV ? {backgroundColor: colors.ink} : {height: 28, minHeight: 28, borderWidth: 0, paddingHorizontal: 4, backgroundColor: 'transparent'}} style={isTV ? {fontSize: type.body.fontSize} : {fontSize: 12.5}} /></View>
      {draft.trim() ? <Button testID={controlID(props.testID, 'field', field.field, 'add')} variant="text" size="small" disabled={!validBulkListEntry(draft.trim()) || props.pending} onPress={add}>{selectionText('next.selection.native.add')}</Button> : null}
    </View>
    {current?.state === 'many' ? <SelectionText secondary>{bulkFieldStateLabel(current)}</SelectionText> : null}
  </View>;
}
function ScalarField({field, fieldRef, ...props}: FieldProps) {
  const {colors, isTV, platform, type} = useTheme();
  const [focused, setFocused] = useState(false);
  const current = props.values[field.field];
  const change = props.changes[field.field];
  const changed = change !== undefined && 'value' in change;
  const value = changed ? change.value : current?.state === 'shared' ? current.value ?? '' : '';
  const hint = bulkFieldHint(current, props.count, changed);
  const update = (next: string) => {if (!props.pending) props.onChangesChange(changeScalar(props.changes, field.field, next));};
  if (field.type === 'enum' || props.fieldOptions?.[field.field]) return <Select testID={controlID(props.testID, 'field', field.field, 'input')} ref={fieldRef} value={value} onValueChange={update} disabled={props.pending} accessibilityLabel={props.fieldLabel(field)} placeholder={current && current.state !== 'shared' && !changed ? bulkFieldStateLabel(current) : changed || current?.value !== undefined ? selectionText('next.selection.native.notSet') : ''}
    description={isTV && hint ? hint : undefined} error={props.fieldErrors?.[field.field]} options={props.fieldOptions?.[field.field] ?? (field.allowed ?? []).map(option => ({value: option, label: props.fieldValueLabel?.(field, option) ?? option}))} />;
  return <View>
    <TextField appearance="selection" testID={controlID(props.testID, 'field', field.field, 'input')} ref={fieldRef} error={props.fieldErrors?.[field.field]} value={value} onChangeText={update} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)} editable={!props.pending}
      accessibilityLabel={props.fieldLabel(field)} keyboardType={field.type === 'integer' || field.type === 'number' ? 'numbers-and-punctuation' : 'default'} maxLength={field.maxLength}
      placeholder={changed || current?.state === 'shared' && current.value !== undefined ? selectionText('next.selection.native.notSet') : undefined}
      fieldStyle={{height: isTV ? fieldSpec.height[platform] : 44, borderRadius: isTV ? fieldSpec.radius[platform] : 10, backgroundColor: colors.ink, paddingHorizontal: isTV ? fieldSpec.paddingX[platform] : 12, borderWidth: changed || isTV ? 2 : 1, borderColor: changed ? colors.accentStrong : colors.line}}
      selection={!focused ? {start: 0, end: 0} : undefined} style={{fontSize: isTV ? type.body.fontSize : 14.5, paddingRight: isTV ? 0 : hint === selectionText('next.selection.native.wasMixed') ? 66 : hint ? 32 : 0}} />
    {!focused && !changed && current && current.state !== 'shared' ? <View pointerEvents="none" style={{position: 'absolute', left: isTV ? 20 : 12, top: isTV ? 14 : 10, borderRadius: 6, backgroundColor: 'rgba(237,245,250,.09)', paddingHorizontal: 9, paddingVertical: 3}}><SelectionText size={isTV ? 22 : 13.5} weight={700}>{bulkFieldStateLabel(current)}</SelectionText></View> : null}
    {hint ? <View pointerEvents="none" style={isTV ? {marginTop: 8} : {position: 'absolute', right: 12, top: 15}}><SelectionText size={isTV ? 20 : 11.5} secondary style={{color: changed ? colors.accentStrong : colors.textTertiary}}>{hint}</SelectionText></View> : null}
  </View>;
}
export function BulkEditor(props: BulkEditorProps) {
  const theme = useTheme();
  const {colors, isTV} = theme;
  const footerButton = selectionFooterProps(theme);
  const retryRef = useRef<View>(null);
  const cancelRef = useRef<View>(null);
  const firstFieldRef = useRef<FocusTarget | null>(null);
  // TV follows the existing side-sheet/Filters controls; no dedicated editor
  // frame exists. Admission and every submitted value remain caller-owned.
  const fields = props.schema.filter(field => field.bulk);
  const firstField = fields.find(field => !(field.type === 'enum' || props.fieldOptions?.[field.field]) || (props.fieldOptions?.[field.field] ?? (field.allowed ?? []).map(value => ({value}))).some(option => !('disabled' in option && option.disabled)));
  const changes = Object.entries(props.changes);
  const failed = props.result ? failedSelectionIds(props.result.results) : [];
  const close = () => props.onOpenChange(false, 'action');
  const result = props.result;
  const focusState = props.open ? result ? JSON.stringify(failed) : 'editing' : 'closed';
  const previousFocusState = useRef(focusState);
  const wasPending = useRef(false);
  useEffect(() => {
    if (props.pending) {wasPending.current = true; return;}
    if (props.open && (previousFocusState.current !== focusState || wasPending.current) && result) focusTarget(failed.length ? retryRef.current : cancelRef.current);
    wasPending.current = false;
    previousFocusState.current = focusState;
  }, [props.open, props.pending, focusState, result, failed.length]);
  const footer = <View style={{gap: isTV ? 18 : 10}}>
    {!result && props.summary.length ? <View style={[styles.summary, isTV ? {padding: 20, gap: 8, borderRadius: 14} : undefined]}>
      <View style={[styles.summaryHead, isTV ? {flexWrap: 'wrap', gap: 8} : undefined]}><SelectionText size={isTV ? 24 : 13} weight={800}>{selectionText('next.selection.native.whatChanges')}</SelectionText><SelectionText size={isTV ? 20 : 12.5} secondary>{selectionText('next.selection.native.changes', {count: props.summary.length})}</SelectionText></View>
      {props.summary.map(line => <View key={line.id} style={{flexDirection: 'row', gap: 8}}><View style={{width: 6, height: 6, marginTop: isTV ? 14 : 6, borderRadius: 3, backgroundColor: colors.accentStrong}} /><SelectionText size={isTV ? 20 : 12.5} style={{flex: 1}}>{line.text}</SelectionText></View>)}
    </View> : null}
    <View style={[styles.buttons, isTV ? {flexDirection: 'column', gap: 12} : undefined]}>
      <Button {...footerButton} testID={controlID(props.testID, 'dismiss')} ref={cancelRef} variant="glass" size="compact" style={[footerButton.style, isTV ? {alignSelf: 'stretch'} : undefined]} onPress={close}>{selectionText(result ? 'next.selection.native.close' : 'next.selection.native.cancel')}</Button>
      {result ? failed.length ? <Button {...footerButton} testID={controlID(props.testID, 'retry')} ref={retryRef} size="compact" loading={props.pending} onPress={() => props.onRetry(failed)} style={[footerButton.style, {flex: isTV ? undefined : 1, alignSelf: 'stretch'}]}>{failed.length === 1 ? selectionText('next.selection.native.retryFor', {title: props.resultEntries.find(row => row.id === failed[0])?.title ?? ''}) : selectionText('next.selection.native.retry')}</Button> : null
        : <Button {...footerButton} testID={controlID(props.testID, 'save')} size="compact" loading={props.pending} disabled={!changes.length || !fields.length || changes.some(([key]) => !fields.some(field => field.field === key))} onPress={props.onSave} style={[footerButton.style, {flex: isTV ? undefined : 1, alignSelf: 'stretch'}]}>{props.saveLabel}</Button>}
    </View>
  </View>;
  return <SelectionSurface open={props.open} onOpenChange={props.onOpenChange} fullHeight headerContext={<View style={{flexDirection: 'row', alignItems: 'center', paddingHorizontal: isTV ? 0 : 20, gap: 10, marginBottom: isTV ? 10 : 0}}><View style={{flex: 1}}>{props.ownerHeader}</View><Button testID={controlID(props.testID, 'header-dismiss')} variant="icon" size="xs" icon="i-x" accessibilityLabel={selectionText('next.selection.native.close')} onPress={close} style={{width: isTV ? 64 : 34, height: isTV ? 64 : 34}} /></View>} title={props.title}
    description={result ? undefined : selectionText('next.selection.native.bulkDescription', {count: props.count})} initialFocusRef={failed.length ? retryRef : props.initialFocusRef ?? (firstField && !result && !props.pending ? firstFieldRef : cancelRef)} returnFocusRef={props.returnFocusRef} testID={props.testID}
    footer={footer} descriptionTextStyle={isTV ? undefined : {fontSize: 13, lineHeight: 18.85, marginTop: 4}} contentStyle={{paddingHorizontal: isTV ? 0 : 18}}>
    {props.artwork}
    {props.error ? <SelectionText style={{color: colors.danger}}>{props.error}</SelectionText> : null}
    {result ? <View style={{gap: 6}}>
      <View style={[styles.notice, failed.length ? styles.warning : undefined]}><Icon name={failed.length ? 'i-warn' : 'i-check'} size={20} color={failed.length ? colors.record : colors.healthy} /><View style={{flex: 1, gap: 2}}><SelectionText size={isTV ? 24 : 14} weight={700}>{selectionText('next.selection.native.updated', {updated: result.updated, failed: result.failed})}</SelectionText>{failed.length ? <SelectionText size={isTV ? 20 : 13} secondary style={{lineHeight: isTV ? 30 : 19.5}}>{selectionText('next.selection.native.savedKept', {count: result.updated, failed: failed.length, titles: defaultI18n.list(props.resultEntries.filter(entry => failed.includes(entry.id)).map(entry => entry.title))})}</SelectionText> : null}</View></View>
      <View style={{gap: theme.platform === 'phone' ? 0 : 6}}>{result.results.map(row => {const entry = props.resultEntries.find(item => item.id === row.id);return <View key={`${row.kind}-${row.id}`} style={[styles.resultRow, isTV ? undefined : {borderBottomWidth: 0, borderTopWidth: row === result.results[0] ? 0 : 1, borderTopColor: 'rgba(40,64,79,.7)'}]}>
        {entry?.artwork ? <View pointerEvents="none" style={{width: 30, height: 45}}>{entry.artwork}</View> : null}
        <View style={{flex: 1, gap: 2}}><SelectionText size={isTV ? 24 : 14} weight={700}>{entry?.title}</SelectionText><SelectionText size={isTV ? 20 : 12} secondary style={{lineHeight: isTV ? 28 : 16.8}}>{row.ok ? selectionText('next.selection.native.saved') : selectionText(row.code === 'metadata_conflict' ? 'next.selection.native.conflict' : row.code === 'invalid_metadata_repair' ? 'next.selection.native.invalidValue' : row.code === 'unsupported_item_kind' ? 'next.selection.native.unsupportedKind' : row.code === 'not_found' ? 'next.selection.native.notFound' : 'next.selection.native.saveFailed')}</SelectionText></View>
        {isTV && !row.ok ? <Button testID={controlID(props.testID, 'result', row.kind, row.id, 'retry')} variant="text" size="small" disabled={props.pending} onPress={() => props.onRetry([row.id])}>{selectionText('next.selection.native.retry')}</Button> : null}
        <View style={{width: isTV ? 40 : 28, height: isTV ? 40 : 28, borderRadius: 20, alignItems: 'center', justifyContent: 'center', backgroundColor: row.ok ? 'rgba(117,213,164,.12)' : 'rgba(241,121,121,.12)'}}><Icon name={row.ok ? 'i-check' : 'i-x'} color={row.ok ? colors.healthy : colors.danger} size={isTV ? 28 : 16} /></View>
      </View>;})}</View>
    </View> : <>
      <View style={[styles.fieldGrid, isTV ? {rowGap: 22, paddingTop: 22} : undefined]}>{fields.map(field => <View key={field.field} style={{width: isTV || field.type === 'list' || field.type === 'multiline' ? '100%' : '48.5%', gap: isTV ? 8 : 5}}>
        <View style={styles.label}><SelectionText size={isTV ? 20 : 12.5} weight={700} secondary>{props.fieldLabel(field)}</SelectionText>{props.changes[field.field] ? <Button testID={controlID(props.testID, 'field', field.field, 'reset')} variant="text" size="small" disabled={props.pending} onPress={() => props.onChangesChange(removeFieldChange(props.changes, field.field))} style={isTV ? undefined : {height: 20, minHeight: 20, paddingHorizontal: 0}} textStyle={isTV ? undefined : {fontSize: 12.5}}>{selectionText('next.selection.native.changed')}</Button> : null}</View>
        {field.type === 'list' ? <ListField {...props} field={field} fieldRef={field === firstField ? node => {firstFieldRef.current = node;} : undefined} /> : <ScalarField {...props} field={field} fieldRef={field === firstField ? node => {firstFieldRef.current = node;} : undefined} />}
      </View>)}</View>
      {!fields.length ? <SelectionText>{selectionText('next.selection.native.noBulkFields')}</SelectionText> : null}
      <View style={styles.notice}><SelectionText size={isTV ? 20 : 12.5}>{selectionText('next.selection.native.oneAtTime', {fields: defaultI18n.list(props.singleItemFields.filter(field => !field.bulk).map(props.fieldLabel))})}</SelectionText></View>
      <Switch testID={controlID(props.testID, 'lock-edited')} variant="selection" checked={props.lockEdited} onCheckedChange={props.onLockEditedChange} disabled={props.pending} description={selectionText('next.selection.native.lockDescription')}>{selectionText('next.selection.native.lock')}</Switch>
    </>}
  </SelectionSurface>;
}
const styles = StyleSheet.create({warning: {flexDirection: 'row', gap: 10, backgroundColor: 'rgba(241,121,121,.08)', borderWidth: 1, borderColor: 'rgba(241,121,121,.35)'}, fieldGrid: {flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'space-between', rowGap: 10, paddingTop: 8}, label: {flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', minHeight: 18}, chips: {flexDirection: 'row', flexWrap: 'wrap', padding: 8, borderRadius: 10, gap: 6, backgroundColor: '#071018', borderWidth: 1, borderColor: '#28404f'}, summary: {gap: 3, paddingHorizontal: 12, paddingVertical: 10, borderRadius: 12, backgroundColor: 'rgba(64,159,212,.1)', borderWidth: 1, borderColor: 'rgba(92,179,226,.3)'}, summaryHead: {flexDirection: 'row', justifyContent: 'space-between', marginBottom: 4}, buttons: {flexDirection: 'row', gap: 10}, notice: {marginTop: 8, padding: 12, borderRadius: 10, backgroundColor: 'rgba(237,245,250,.05)', gap: 4}, resultRow: {flexDirection: 'row', alignItems: 'center', gap: 14, paddingVertical: 9, borderBottomWidth: 1, borderBottomColor: '#28404f'}});
