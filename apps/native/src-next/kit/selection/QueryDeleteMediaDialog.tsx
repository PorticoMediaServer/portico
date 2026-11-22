import React, {useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {isSelectionActionTerminal} from '../../../../../packages/client-core/src/selection-actions';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {Button} from '../controls/Button';
import {Switch} from '../controls/Switch';
import {TextField} from '../controls/TextField';
import {useTheme} from '../theme';
import {SelectionSurface, SelectionText, selectionFooterProps} from './surface';
import type {NativeSelectionActionPresentation} from '../../screens/library/selection-actions';

export type NativeQueryActionSurfaceProps = Readonly<{
  action: NativeSelectionActionPresentation;
  onClose: () => void;
  ownerHeader?: React.ReactNode;
}>;

export function NativeQueryDeleteMediaDialog({action, onClose, ownerHeader}: NativeQueryActionSurfaceProps) {
  const state = useSyncExternalStore(action.subscribe, action.getSnapshot);
  const theme = useTheme(), footerButton = selectionFooterProps(theme), cancelRef = useRef<View>(null), t = defaultI18n.t;
  const [choiceError, setChoiceError] = useState<unknown>();
  if (!state || !action.current() || state.phase === 'retired') return null;
  const current = () => action.current() && action.controller.getSnapshot() === state;
  const preview = state.preparation?.kind === 'selection-action-preview' ? state.preparation : undefined;
  const job = state.job, terminal = job && isSelectionActionTerminal(job.state);
  const ready = state.phase === 'ready' && !!preview;
  const removeFiles = state.input?.options.deleteFiles === true;
  const editable = !job && ['preparing', 'ready', 'error'].includes(state.phase);
  const close = () => {if (current()) onClose();};
  const cancel = () => {if (current()) void action.controller.cancel();};
  const confirm = () => {
    if (!current() || !ready || !preview) return;
    void action.controller.confirm();
  };
  const retry = () => {if (current()) void action.controller.retry();};
  const open = (run: () => Promise<void>) => {
    if (!current()) return;
    setChoiceError(undefined);
    void run().catch(error => {if (action.current()) setChoiceError(error);});
  };
  const errorCode = state.errorCode ?? job?.errorCode;
  const error = errorCode ? presentError({code: errorCode}, 'detail', {operation: 'action'}).body
    : choiceError ? presentError(choiceError, 'downloads', {operation: 'load'}).body : undefined;
  const title = preview?.confirmationKind === 'title' ? t('next.selection.native.deleteTitle', {title: preview.confirmation})
    : t('next.selection.native.deleteTitles', {count: preview?.total ?? action.target.total});
  const footer = <View style={{flexDirection: theme.isTV ? 'column' : 'row', gap: 12}}>
    <Button {...footerButton} ref={cancelRef} testID="query-action-close" variant="glass" onPress={close}>{t('action.close')}</Button>
    {!terminal && state.phase !== 'idle' && <Button {...footerButton} testID="query-action-cancel" variant="glass" disabled={state.phase === 'cancelling'} onPress={cancel}>{t('action.cancel')}</Button>}
    {state.phase === 'uncertain' && <Button {...footerButton} testID="query-action-retry" onPress={retry}>{t('action.tryAgain')}</Button>}
    {!job && state.phase !== 'uncertain' && state.phase !== 'idle' && <Button {...footerButton} testID="query-action-confirm"
      // select.html#22: Delete is the same red button as the explicit selection's dialog.
      variant="text" style={[footerButton.style, {backgroundColor: theme.colors.record}]} textStyle={[footerButton.textStyle, {color: '#2a0808'}]}
      disabled={!ready || state.confirmation !== preview?.confirmation}
      loading={state.phase === 'submitting' || state.phase === 'confirming'} onPress={confirm}>{t('next.selection.native.delete')}</Button>}
  </View>;
  return <SelectionSurface open title={title} headerContext={ownerHeader} footer={footer} initialFocusRef={cancelRef}
    onOpenChange={open => {if (!open) close();}} testID="library-query-delete">
    <View style={{gap: 12}}>
      {state.phase === 'preparing' && <SelectionText secondary>{t('next.selection.native.checking')}</SelectionText>}
      {preview && !job && <>
        <SelectionText>{t('next.selection.native.fileFacts', {count: preview.files, size: preview.bytesKnown ? defaultI18n.bytes(preview.bytes) : '—'})}</SelectionText>
        {preview.sample.map(item => <SelectionText key={item.itemId}>{item.title}</SelectionText>)}
        <Switch checked={removeFiles} disabled={!editable} testID="query-delete-files"
          description={t(removeFiles ? 'next.selection.native.irrecoverable' : 'next.selection.native.filesStay')}
          onCheckedChange={value => open(() => action.openDelete(value))}>{t('next.selection.native.removeFiles')}</Switch>
        <TextField testID="query-action-confirmation" value={state.confirmation} editable={ready} autoCapitalize="none" autoCorrect={false}
          appearance="selection" keyboardType={theme.platform !== 'phone' && preview.confirmationKind === 'count' ? 'number-pad' : 'default'}
          label={t(preview.confirmationKind === 'title' ? 'next.selection.native.typeTitle' : 'next.selection.native.typeCount', {count: preview.confirmation})}
          description={preview.confirmationKind === 'title' ? preview.confirmation : undefined}
          onChangeText={value => {if (current()) action.controller.setConfirmation(value);}}/>
      </>}
      {job && <View accessibilityLiveRegion="polite" style={{gap: 8}}>
        {terminal && (job.state === 'cancelled' || job.errorCode === 'cancelled') && <SelectionText>{t('download.state.canceled')}</SelectionText>}
        {job.state === 'capturing' && <SelectionText secondary>{t('next.selection.jobPreparing')}</SelectionText>}
        <SelectionText>{t('next.selection.jobProgress', {done: job.state === 'capturing' ? job.admitted : job.done + job.failed, total: job.total})}</SelectionText>
        <SelectionText>{t('next.selection.native.deleteResult', {removed: job.done, failed: job.failed})}</SelectionText>
        <SelectionText secondary>{t('next.selection.native.fileFacts', {count: job.filesDeleted, size: defaultI18n.bytes(job.bytesDeleted)})}</SelectionText>
        {!terminal && <SelectionText secondary>{t('downloads.checking')}</SelectionText>}
      </View>}
      {error && <SelectionText style={{color: theme.colors.danger}}>{error}</SelectionText>}
    </View>
  </SelectionSurface>;
}
