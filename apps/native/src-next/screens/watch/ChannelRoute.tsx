import React, {useEffect, useMemo, useState} from 'react';
import {Platform, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {sameFence, type ChannelPlaybackFence} from '../../../../../packages/player-core/src';
import {Button} from '../../kit/controls';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeWatchModuleBindings} from './module';

/** The launch page owns its original route, not the adopted player's lifetime. */
export function NativeWatchChannelRoute({context, route, title}: {context: NativeShellBootstrapContext; route: Extract<NativeShellRoute, {kind: 'channel'}>; title: NativeWatchModuleBindings['title']}) {
  const [attempt, setAttempt] = useState(0), [error, setError] = useState<unknown>();
  const insets = useSafeAreaInsets();
  const page = useMemo(() => {
    const snapshot = context.navigation.getSnapshot(), occurrence = context.navigation.presentationStack().at(-1)?.occurrence;
    const owner = {mounted: true, retired: false, consumed: false};
    const current = () => {
      if (!owner.mounted || owner.retired || owner.consumed) return false;
      if (!context.current() || context.navigation.getSnapshot() !== snapshot || snapshot.route !== route
        || !occurrence || context.navigation.presentationStack().at(-1)?.occurrence !== occurrence) {owner.retired = true; return false;}
      return owner.mounted && !owner.retired && !owner.consumed && context.navigation.getSnapshot() === snapshot;
    };
    const pop = () => {
      if (!current() || !occurrence) return;
      // The navigation owner validates the same original snapshot/occurrence.
      if (context.navigation.commitNativePop(snapshot, occurrence)) owner.consumed = true;
    };
    return {owner, current, pop};
  }, [context, route]);
  useEffect(() => {page.owner.mounted = true; return () => {page.owner.mounted = false;};}, [page]);
  useEffect(() => {
    const controller = new AbortController();
    const current = () => !controller.signal.aborted && page.current() && !controller.signal.aborted;
    const changed = () => {if (!current()) controller.abort();};
    const offRoute = context.navigation.subscribe(changed), offAuthority = context.subscribeAuthority(changed);
    let startCompleted = false, receipt: ChannelPlaybackFence | undefined;
    const consumeOriginal = () => {
      if (!startCompleted || !receipt || !current()) return;
      if (sameFence(context.playback.runtime.getSnapshot().fence, receipt) && current()) page.pop();
    };
    const accepted = (fence: ChannelPlaybackFence) => {
      if (receipt || !current() || fence.sourceId !== route.sourceId || fence.channelId !== route.channelId) return;
      if (!sameFence(context.playback.runtime.getSnapshot().fence, fence) || !current()) return;
      receipt = fence;
      consumeOriginal();
    };
    setError(undefined);
    if (current()) void context.playback.runtime.startChannel({sourceId: route.sourceId, channelId: route.channelId,
      timezone: route.guide?.options.timezone ?? Intl.DateTimeFormat().resolvedOptions().timeZone, guide: route.guide,
      expectedFence: route.tune?.fence, expectedSourceGeneration: route.tune?.sourceGeneration,
      ...(!Platform.isTV ? {onAccepted: accepted} : {}), lifetime: {signal: controller.signal, isCurrent: current}})
      .then(() => {
        // Warm admission may precede this original metadata completion; cold
        // admission may follow it. Neither event alone consumes the launch.
        if (!current()) return;
        startCompleted = true;
        consumeOriginal();
      }).catch(failure => {if (current()) setError(failure);});
    return () => {controller.abort(); offRoute(); offAuthority();};
  }, [context, route, page, attempt]);
  const failure = error ? title.renderError(error, () => {if (page.current()) setAttempt(value => value + 1);}) : null;
  return Platform.isTV ? failure : <View style={{flex: 1, paddingTop: insets.top + 12}}>
    <Button testID="native-channel-back" variant="text" icon="i-left" accessibilityLabel={defaultI18n.t('action.back')} onPress={page.pop}>{defaultI18n.t('action.back')}</Button>
    {failure}
  </View>;
}
