import React, {useRef, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {Banner, Dialog} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';
import type {NativeLibraryMarkAll} from './mark-all';

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
    footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 12}}>{footer}</View>}>
    <View style={{gap: 12}}>
      {count !== undefined && <Text testID="library-mark-all-count" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{count}</Text>}
      {progress !== undefined && <Text testID="library-mark-all-progress" accessibilityLiveRegion="polite"
        style={[theme.type.bodyStrong, {color: done ? theme.colors.text : theme.colors.textSecondary}]}>{progress}</Text>}
      {leftover !== undefined && <Text testID="library-mark-all-leftover" style={[theme.type.body, {color: theme.colors.textTertiary}]}>{leftover}</Text>}
      {error !== undefined && <Banner testID="library-mark-all-error" variant="notice" tone="error" placement="inline" message={error}/>}
    </View>
  </Dialog>;
}
