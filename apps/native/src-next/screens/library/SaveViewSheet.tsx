import React, {useRef, useSyncExternalStore} from 'react';
import {Text, TextInput, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {Button, TextField} from '../../kit/controls';
import {Banner, BottomSheet} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';
import type {NativeLibrarySaveView} from './save-view';

/** Phone naming sheet: collections13c composition and today's SaveViewDialog
 * fields/copy, explicitly authorized by ruling 37. No Description field. */
export function NativeLibrarySaveViewSheet({controller}: {controller: NativeLibrarySaveView}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), input = useRef<TextInput>(null), theme = useTheme();
  const t = defaultI18n.t;
  return <BottomSheet open={state.open} onOpenChange={open => {if (!open) controller.close();}} testID="library-save-view-sheet"
    title={t('lib.saveViewTitle')} description={t('lib.saveViewLede')} initialFocusRef={input} showClose={false} fitContent keyboardAvoiding
    dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} dismissOnGesture={!state.busy}
    footer={<View style={{flexDirection: 'row', gap: 12}}>
      <Button testID="library-save-view-cancel" focusId="save-view-cancel" variant="glass" disabled={state.busy} onPress={controller.close} style={{flex: 1}}>{t('action.cancel')}</Button>
      <Button testID="library-save-view-save" focusId="save-view-save" variant="primary" loading={state.busy} disabled={state.busy || !state.name.trim()}
        onPress={() => {void controller.save();}} style={{flex: 1}}>{t('lib.saveView')}</Button>
    </View>}>
    <View style={{gap: 12}}>
      {state.error !== undefined && <Banner testID="library-save-view-error" variant="notice" tone="error" placement="inline" message={presentError(state.error, 'library', {operation: 'save'}).body}/>}
      <TextField ref={input} testID="library-save-view-name" focusId="save-view-name" label={t('lib.saveViewName')} placeholder={t('lib.saveViewPlaceholder')}
        autoFocus value={state.name} onChangeText={controller.setName} editable={!state.busy && !state.nameLocked} maxLength={200} returnKeyType="done"
        onSubmitEditing={() => {void controller.save();}}/>
      <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('lib.saveViewHelp')}</Text>
    </View>
  </BottomSheet>;
}
