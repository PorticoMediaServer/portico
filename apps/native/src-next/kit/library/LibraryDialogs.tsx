import React, {useRef, useSyncExternalStore} from 'react';
import {Text, TextInput, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {Button, TextField} from '../controls';
import {Banner, BottomSheet, Dialog} from '../overlays';
import {useTheme} from '../theme';
import type {NativeLibraryMarkAll} from '../../screens/library/mark-all';
import type {NativeLibrarySaveView} from '../../screens/library/save-view';

/** The menu's Mark all confirmation: the view's own count first, then the durable job's
 * own captured total, progress and failures. Nothing here counts titles itself. */
export function NativeLibraryMarkAllDialog({controller}: {controller: NativeLibraryMarkAll}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme(), t = defaultI18n.t;
  const cancel = useRef<View>(null), working = state.phase === 'working', done = state.phase === 'done';
  const label = controller.read() ? t('next.browse.native.markAllRead') : t('next.browse.native.markAllWatched');
  const count = state.viewTotal === undefined ? undefined
    : t(state.viewAtLeast ? 'lib.countAtLeast' : 'lib.count', {count: state.viewTotal, pivot: controller.pivot()});
  const progress = working
    ? state.totalKnown && state.total !== undefined ? t('next.browse.native.markAllProgress', {done: state.done, total: state.total}) : t('next.browse.native.markAllPreparing')
    : done ? t('next.browse.native.markAllMarked', {count: state.done}) : undefined;
  const leftover = done && state.failed > 0 ? t('next.browse.native.markAllNotMarked', {count: state.failed}) : undefined;
  const error = controller.errorBody();
  const footer = done
    ? <Button testID="library-mark-all-done" focusId="mark-all-done" variant="primary" onPress={controller.close}>{t('action.done')}</Button>
    : <>
      <Button ref={cancel} testID="library-mark-all-cancel" focusId="mark-all-cancel" variant="glass" disabled={working} onPress={controller.close}>{t('action.cancel')}</Button>
      <Button testID="library-mark-all-submit" focusId="mark-all-submit" variant="primary" loading={working} disabled={working}
        onPress={() => {void controller.start();}}>{state.phase === 'failed' ? t('next.shell.tryAgain') : label}</Button>
    </>;
  return <Dialog open={state.open} onOpenChange={open => {if (!open && !working) controller.close();}} title={label}
    description={t(controller.body())} testID="library-mark-all-dialog" initialFocusRef={cancel} showClose={false}
    dismissOnBack={!working} dismissOnOutsidePress={!working} dismissOnGesture={!working}
    footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: theme.space.sm - 4}}>{footer}</View>}>
    <View style={{gap: theme.space.sm - 4}}>
      {count !== undefined && <Text testID="library-mark-all-count" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{count}</Text>}
      {progress !== undefined && <Text testID="library-mark-all-progress" accessibilityLiveRegion="polite"
        style={[theme.type.bodyStrong, {color: done ? theme.colors.text : theme.colors.textSecondary}]}>{progress}</Text>}
      {leftover !== undefined && <Text testID="library-mark-all-leftover" style={[theme.type.body, {color: theme.colors.textTertiary}]}>{leftover}</Text>}
      {error !== undefined && <Banner testID="library-mark-all-error" variant="notice" tone="error" placement="inline" message={error}/>}
    </View>
  </Dialog>;
}

/** Phone naming sheet: collections13c composition and today's SaveViewDialog
 * fields/copy, explicitly authorized by ruling 37. No Description field. */
export function NativeLibrarySaveViewSheet({controller}: {controller: NativeLibrarySaveView}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), input = useRef<TextInput>(null), theme = useTheme();
  const t = defaultI18n.t;
  return <BottomSheet open={state.open} onOpenChange={open => {if (!open) controller.close();}} testID="library-save-view-sheet"
    title={t('lib.saveViewTitle')} description={t('lib.saveViewLede')} initialFocusRef={input} showClose={false} fitContent keyboardAvoiding
    dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} dismissOnGesture={!state.busy}
    footer={<View style={{flexDirection: 'row', gap: theme.space.sm - 4}}>
      <Button testID="library-save-view-cancel" focusId="save-view-cancel" variant="glass" disabled={state.busy} onPress={controller.close} style={{flex: 1}}>{t('action.cancel')}</Button>
      <Button testID="library-save-view-save" focusId="save-view-save" variant="primary" loading={state.busy} disabled={state.busy || !state.name.trim()}
        onPress={() => {void controller.save();}} style={{flex: 1}}>{t('lib.saveView')}</Button>
    </View>}>
    <View style={{gap: theme.space.sm - 4}}>
      {state.error !== undefined && <Banner testID="library-save-view-error" variant="notice" tone="error" placement="inline" message={presentError(state.error, 'library', {operation: 'save'}).body}/>}
      <TextField ref={input} testID="library-save-view-name" focusId="save-view-name" label={t('lib.saveViewName')} placeholder={t('lib.saveViewPlaceholder')}
        autoFocus value={state.name} onChangeText={controller.setName} editable={!state.busy && !state.nameLocked} maxLength={200} returnKeyType="done"
        onSubmitEditing={() => {void controller.save();}}/>
      <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('lib.saveViewHelp')}</Text>
    </View>
  </BottomSheet>;
}
