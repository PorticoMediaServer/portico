import React, {useRef} from 'react';
import {StyleSheet, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Button} from '../controls/Button';
import {TextField} from '../controls/TextField';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {selectionText} from './copy';
import {selectionControlID as controlID} from './focus-ids';
import {deleteSelectionGate} from './presentation';
import {selectionFooterProps, SelectionSurface, SelectionText} from './surface';
import type {DeleteMediaDialogProps} from './types';
import {NativeQueryDeleteMediaDialog, type NativeQueryActionSurfaceProps} from './QueryDeleteMediaDialog';

type QueryDeleteProps = {queryAction: NativeQueryActionSurfaceProps['action']; ownerHeader?: React.ReactNode; onClose: () => void};
export function DeleteMediaDialog(props: DeleteMediaDialogProps | QueryDeleteProps) {
  const theme = useTheme();
  const {isTV, colors} = theme;
  const phone = theme.platform === 'phone';
  const footerButton = selectionFooterProps(theme);
  const cancelRef = useRef<View>(null);
  if ('queryAction' in props) return <NativeQueryDeleteMediaDialog action={props.queryAction} ownerHeader={props.ownerHeader} onClose={props.onClose}/>;
  if (!props.owner) return null;
  const gate = deleteSelectionGate(props);
  const preview = props.preview;
  const result = props.result;
  const title = result ? selectionText('next.selection.native.done') : preview?.confirmationKind === 'title' ? selectionText('next.selection.native.deleteTitle', {title: preview.confirmation}) : selectionText('next.selection.native.deleteTitles', {count: props.selectionCount});
  const restricted = gate.reason === 'limit' || gate.reason === 'libraries' || gate.reason === 'blocked';
  const close = () => props.onOpenChange(false, 'action');
  const totals = new Map<string, number>();
  for (const target of preview?.targets ?? []) for (const dependent of target.dependents) totals.set(dependent.kind, (totals.get(dependent.kind) ?? 0) + dependent.count);
  const dependencies = [...totals].filter(([kind]) => kind !== 'progress').map(([kind, count]) => selectionText(kind === 'playlist' ? 'next.selection.native.dependent.playlist' : kind === 'collection' ? 'next.selection.native.dependent.collection' : kind === 'progress' ? 'next.selection.native.dependent.progress' : kind === 'saved' ? 'next.selection.native.dependent.saved' : kind === 'library-channel' ? 'next.selection.native.dependent.channel' : 'next.selection.native.dependent.recording', {count}));
  const phoneSummary = phone && (preview?.targets.length ?? 0) > 1 && [...totals.keys()].every(kind => kind === 'playlist' || kind === 'collection' || kind === 'progress');
  const kept = result?.receipts.filter(receipt => !receipt.removed) ?? [];
  // The rebuilt server counts what goes with the titles across the whole selection (definition 03 rule 65).
  const goesWith = preview?.listEntries === undefined ? [] : [
    ...(preview.listEntries > 0 ? [defaultI18n.t('next.selection.listEntriesGo', {count: preview.listEntries})] : []),
    ...(preview.otherPeople ? [defaultI18n.t('next.selection.othersSavedGo', {count: preview.otherPeople})] : []),
    ...(preview.alsoRemoves?.length ? [defaultI18n.t('next.selection.alsoRemoves', {titles: defaultI18n.list(preview.alsoRemoves.map(ref => ref.title))})] : [])];
  const descendants = preview ? preview.targets.length > 1 && preview.titlesTotal !== undefined ? preview.titlesTotal : preview.targets.reduce((sum, target) => sum + target.titles, 0) : 0;
  const footer = <View style={[styles.buttons, phone ? {paddingHorizontal: 4} : undefined, isTV ? {flexDirection: 'column', gap: 12} : undefined]}>
    <Button {...footerButton} testID={controlID(props.testID, 'dismiss')} ref={cancelRef} size="compact" variant={result ? 'primary' : 'glass'} style={[footerButton.style, {flex: isTV ? undefined : 1, alignSelf: 'stretch'}]} onPress={close}>{selectionText(result || restricted ? 'next.selection.native.close' : 'next.selection.native.cancel')}</Button>
    {!result ? <Button {...footerButton} testID={controlID(props.testID, 'delete')} size="compact" variant="text" disabled={!gate.allowed || !!props.error} loading={props.pending} style={[footerButton.style, {flex: isTV ? undefined : 1, alignSelf: 'stretch', backgroundColor: colors.record}]} textStyle={[footerButton.textStyle, {color: '#2a0808'}]} onPress={() => {if (preview && gate.allowed && !props.error) props.onDelete(preview, props.confirmation, props.removeFiles);}}>{props.selectionCount === 1 || restricted ? selectionText('next.selection.native.delete') : selectionText('next.selection.native.deleteCount', {count: props.selectionCount})}</Button> : null}
  </View>;
  return <SelectionSurface open={props.open} onOpenChange={props.onOpenChange} title={title} headerContext={phone && props.ownerHeader != null ? <View style={{paddingBottom: 10}}>{props.ownerHeader}</View> : props.ownerHeader} footer={footer} footerPadding={phone ? {top: 0, bottom: 34, keyboardBottom: 12} : undefined} showFooterSeparator={phone ? false : undefined} initialFocusRef={props.initialFocusRef ?? cancelRef} returnFocusRef={props.returnFocusRef} testID={props.testID} titleType={isTV ? undefined : {size: 21, weight: 800, tracking: -.025, lineHeight: 25.2}} titleTextStyle={phone ? {includeFontPadding: false} : undefined} contentStyle={{paddingHorizontal: isTV ? 0 : 20}}>
    <View style={[styles.stack, isTV ? {gap: 22, paddingTop: 26} : undefined]}>
      {result ? <>
        <SelectionText weight={phone ? 400 : undefined} style={phone ? {lineHeight: 21 * theme.textScale, color: colors.textSecondary, includeFontPadding: false} : undefined}>{selectionText('next.selection.native.deleteResult', {removed: result.removed, failed: result.failed})}</SelectionText>
        {kept.slice(0, 5).map(receipt => <View key={receipt.itemId} style={[styles.warning, phone ? {paddingVertical: 10, paddingHorizontal: 11} : undefined]}>
          <Icon name="i-warn" size={isTV ? 28 : 18} color={colors.amber} />
          <View style={{flex: 1, gap: phone ? 0 : 3}}><SelectionText weight={700} style={phone ? {lineHeight: 20.3 * theme.textScale, includeFontPadding: false} : undefined}>{receipt.title}</SelectionText><SelectionText size={isTV ? 20 : 13.5} weight={phone ? 400 : undefined} secondary style={{lineHeight: isTV ? 29 : phone ? 19.575 * theme.textScale : 19.575, ...(phone ? {color: colors.textSecondary, includeFontPadding: false} : {})}}>{selectionText(receipt.code === 'file_changed' ? 'next.selection.native.fileChanged' : 'next.selection.native.fileNotRemoved')}</SelectionText></View>
        </View>)}
        {kept.length > 5 ? <SelectionText secondary>{selectionText('next.selection.native.andMore', {count: kept.length - 5})}</SelectionText> : null}
        {result.kept?.length ? <View style={styles.warning}><Icon name="i-warn" size={isTV ? 28 : 18} color={colors.amber} /><View style={{flex: 1, gap: 3}}>
          <SelectionText weight={700}>{defaultI18n.t('next.selection.keptFiles', {count: result.kept.length})}</SelectionText>
          {result.kept.slice(0, 5).map(file => <SelectionText key={file.path} size={isTV ? 20 : 13} secondary>{file.path}: {file.reason}</SelectionText>)}
        </View></View> : null}
      </> : restricted ? <View style={styles.warning} accessibilityLiveRegion="polite"><Icon name="i-warn" size={isTV ? 28 : 18} color={gate.reason === 'limit' ? colors.record : colors.amber} /><SelectionText size={isTV ? 22 : 13.5} style={{flex: 1}}>{gate.reason === 'blocked' && preview?.refusal ? preview.refusal : selectionText(gate.reason === 'limit' ? 'next.selection.native.deleteLimit' : gate.reason === 'libraries' ? 'next.selection.native.deleteLibraries' : 'next.selection.native.deleteBlocked', {library: preview?.targets.find(target => !target.allowMediaDeletion)?.libraryName ?? preview?.blocked[0] ?? ''})}</SelectionText></View>
        : !preview && props.error ? null : props.loading || !preview ? <SelectionText secondary>{selectionText('next.selection.native.checking')}</SelectionText> : <>
          {descendants > preview.targets.length ? <>
            <SelectionText weight={700}>{selectionText('next.selection.native.descendants', {count: descendants})}</SelectionText>
            <SelectionText secondary>{selectionText('next.selection.native.everythingBeneath')}</SelectionText>
          </> : null}
          {!phone && preview.targets.length > 1 ? <View style={{gap: 8}}>{preview.targets.slice(0, 5).map(target => <View key={target.itemId} style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
            {props.renderArtwork ? <View pointerEvents="none" style={{width: 24, height: 36}}>{props.renderArtwork(target.itemId)}</View> : null}
            <View style={{flex: 1}}><SelectionText size={isTV ? 24 : 13.5} weight={700}>{target.title}</SelectionText><SelectionText size={isTV ? 20 : 12} secondary>{selectionText('next.selection.native.fileFacts', {count: target.files.length, size: defaultI18n.bytes(target.bytes)})}</SelectionText></View>
          </View>)}{preview.targets.length > 5 ? <SelectionText size={isTV ? 22 : 13.5} secondary>{selectionText('next.selection.native.andMore', {count: preview.targets.length - 5})}</SelectionText> : null}</View> : null}
          <SelectionText size={isTV ? undefined : 14} weight={phone ? 400 : undefined} secondary style={{lineHeight: isTV ? undefined : phone ? 21 * theme.textScale : 21, ...(phone ? {color: colors.textSecondary, includeFontPadding: false} : {})}}><SelectionText weight={700}>{selectionText('next.selection.native.fileFacts', {count: preview.totalFiles, size: preview.totalBytesText})}</SelectionText>{phoneSummary ? totals.size ? ` ${defaultI18n.t('next.selection.dependentsMany')}` : '' : <>{dependencies.length ? selectionText('next.selection.native.dependencies', {dependencies: defaultI18n.list(dependencies)}) : ''}{totals.get('progress') ? selectionText('next.selection.native.savedPlaces', {count: totals.get('progress')!}) : ''}</>}</SelectionText>
          {goesWith.length ? <SelectionText size={isTV ? undefined : 14} secondary>{goesWith.join(' ')}</SelectionText> : null}
          {/* Delete always deletes the files (definition 03, decision O03): there is no "only Portico's record". */}
          {phone && phoneSummary ? null : <SelectionText size={isTV ? 20 : 12} weight={phone ? 400 : undefined} secondary style={phone ? {lineHeight: 18 * theme.textScale, includeFontPadding: false} : undefined}>{selectionText('next.selection.native.irrecoverable')}</SelectionText>}
          <TextField appearance="selection" testID={controlID(props.testID, 'confirmation')} value={props.confirmation} onChangeText={props.onConfirmationChange} editable={!props.pending} fieldStyle={phone ? {height: 46} : undefined} style={phone ? {fontSize: 16 * theme.textScale} : undefined}
            label={preview.confirmationKind === 'title' ? selectionText('next.selection.native.typeTitle') : selectionText('next.selection.native.typeCount', {count: preview.confirmation})}
            accessibilityLabel={preview.confirmationKind === 'title' ? selectionText('next.selection.native.typeTitle') : selectionText('next.selection.native.typeCount', {count: preview.confirmation})}
            keyboardType={!phone && preview.confirmationKind === 'count' ? 'number-pad' : 'default'} autoCapitalize="none" autoCorrect={false}
            description={preview.confirmationKind === 'title' ? preview.confirmation : undefined} />
        </>}
      {props.error ? <SelectionText style={{color: colors.danger}}>{props.error}</SelectionText> : null}
      {gate.reason === 'limit' ? <SelectionText size={isTV ? 20 : 12} secondary>{selectionText('next.selection.native.neverSplit')}</SelectionText> : null}
    </View>
  </SelectionSurface>;
}
const styles = StyleSheet.create({buttons: {flexDirection: 'row', gap: 10}, stack: {gap: 12, paddingTop: 0}, warning: {padding: 12, borderRadius: 10, flexDirection: 'row', gap: 10, backgroundColor: 'rgba(232,188,106,.1)', borderWidth: 1, borderColor: 'rgba(232,188,106,.35)'}});
