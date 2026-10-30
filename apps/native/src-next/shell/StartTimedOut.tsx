import React, {useRef} from 'react';
import {View} from 'react-native';
import {Button} from '../kit/controls';
import {Dialog} from '../kit/overlays';
import type {NativePlaybackStartTimedOut} from '../platform/playback/types';
import type {NativeShellRuntime} from './runtime';

/** O-159: a Play whose real dock or player was not drawn within NATIVE_START_DEADLINE_MS. The runtime has
 * already stopped it and written the local diagnostic; this says so plainly and offers it again. */
export function StartTimedOut({timedOut, runtime}: {timedOut: NativePlaybackStartTimedOut; runtime: NativeShellRuntime}) {
  const t = runtime.text.t, retry = useRef<View>(null);
  return <Dialog testID="playback-start-timed-out" open onOpenChange={open => {if (!open) timedOut.close();}} initialFocusRef={retry}
    title={t('web.player.error.startTitle')} description={t('web.player.error.startBody')}
    footer={<View style={{flexDirection: 'row', gap: 10, justifyContent: 'flex-end'}}>
      <Button variant="glass" onPress={timedOut.close}>{t('action.close')}</Button>
      <Button ref={retry} variant="primary" hasTVPreferredFocus onPress={() => {void timedOut.retry().catch(runtime.reportError);}}>{t('action.tryAgain')}</Button>
    </View>}>
    {null}
  </Dialog>;
}
