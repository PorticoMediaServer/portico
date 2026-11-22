import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {QueuePlaybackView} from '../../../../../../packages/client-core/src/queue-controller';
import type {V1QueuePolicyIntent} from '../../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import type {PlaybackFence} from '../../../../../../packages/player-core/src';
import type {NativeListeningQueuePolicy} from '../../../shell/listening-queue-policy';
import type {AudioControl} from '../../../kit/frames';

/** No optimistic shuffle/repeat state: only the accepted queue projection is drawn. */
export function useTVQueuePolicy({fence, queue, policy, reportError, testID}: {
  fence: PlaybackFence; queue?: QueuePlaybackView; policy?: NativeListeningQueuePolicy;
  reportError: (error: unknown) => void; testID: string;
}) {
  const t = defaultI18n.t, [busy, setBusy] = useState(false);
  const key = JSON.stringify([fence, queue?.queue.id]);
  const lease = useRef<{key: string; abort: AbortController} | undefined>(undefined), flight = useRef<object | undefined>(undefined);
  if (lease.current && lease.current.key !== key) {
    lease.current.abort.abort(); lease.current = undefined; flight.current = undefined;
  }
  useLayoutEffect(() => {
    const at = {key, abort: new AbortController()};
    lease.current = at; flight.current = undefined; setBusy(false);
    return () => {at.abort.abort(); if (lease.current === at) lease.current = undefined;};
  }, [key]);
  if (!queue || !policy?.canApply(fence)) return {};
  const change = async (intent: V1QueuePolicyIntent) => {
    const at = lease.current;
    if (!at || at.key !== key || at.abort.signal.aborted || flight.current) return;
    const token = {}; flight.current = token; setBusy(true);
    const current = () => lease.current === at && !at.abort.signal.aborted;
    try {await policy.apply(fence, {queueId: queue.queue.id, expectedRevision: queue.queue.revision, intent}, {signal: at.abort.signal, isCurrent: current});}
    catch (error) {if (current()) reportError(error);}
    finally {if (current() && flight.current === token) {flight.current = undefined; setBusy(false);}}
  };
  const control = (id: 'shuffle' | 'repeat', active: boolean, label: string, intent: V1QueuePolicyIntent): AudioControl => ({
    icon: id === 'shuffle' ? 'i-shuffle' : 'i-repeat', label, active, busy, focusId: id, testID: `${testID}-${id}`,
    repeatOne: id === 'repeat' && queue.queue.repeat === 'one',
    onPress: () => {void change(intent);},
  });
  const repeat = queue.queue.repeat;
  return {
    shuffle: queue.queue.radio ? undefined : control('shuffle', queue.queue.shuffled, t('next.dock.shuffle'), {action: 'shuffle', shuffled: !queue.queue.shuffled}),
    repeat: control('repeat', repeat !== 'off', t(repeat === 'one' ? 'next.dock.repeatOne' : repeat === 'all' ? 'next.dock.repeatAll' : 'next.dock.repeatOff'),
      {action: 'repeat', repeat: repeat === 'off' ? 'all' : repeat === 'all' ? 'one' : 'off'}),
  };
}
