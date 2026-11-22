import React from 'react';
import {StyleSheet, Text, View} from 'react-native';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {playbackIdentity} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {PlaybackFence} from '../../../../../packages/player-core/src';
import {Button} from '../controls';
import {Icon} from '../Icon';
import {textLines, useTheme} from '../theme';
import {shellType} from '../shell/visual';
import {FocusRegion} from '../focus';
import {SidePanel} from '../overlays';
import type {NativeWatchHostContext} from '../../screens/watch/WatchHost';
import type {NativePlaybackFailureReason} from '../../platform/playback/playback-failure';
import {failureBody, failureTitle, type NativePlaybackEnd} from '../../screens/watch/failure-words';

/** The exact playback that failed: its fence, session and saved place. A callback
 * reads only this, never the live context, so a successor start is never touched. */
export type NativePlaybackFailure = Readonly<{fence: PlaybackFence; sessionId?: string; positionSeconds: number; itemId?: string; reason?: NativePlaybackFailureReason; ended?: NativePlaybackEnd}>;

/** states 11: the ten-foot failure state. The frozen picture stays behind it,
 * the bar below keeps working, and nothing here retries by itself. The retry
 * control exists only when the runtime supplies its own resume port. */
export function NativePlaybackFailed({context, failure, retry, details, differentVersion, chromeVisible}: {
  context: NativeWatchHostContext;
  failure: NativePlaybackFailure;
  retry?: (failure: NativePlaybackFailure) => void;
  details?: (failure: NativePlaybackFailure) => React.ReactNode;
  differentVersion?: (failure: NativePlaybackFailure) => void;
  /** The overlay's own title lockup is drawn once; the state adds it only when the overlay is down. */
  chromeVisible: boolean;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  const identity = context.source.subject ? playbackIdentity(context.source.subject, 'full', context.runtime.text) : undefined;
  // states 11 places the block at left 96, top 486 and centres it on its own height.
  const [blockHeight, setBlockHeight] = React.useState(0);
  const [detailsFor, setDetailsFor] = React.useState<string>();
  const detailsOpener = React.useRef<View>(null), identityKey = JSON.stringify(failure);
  const facts = details?.(failure), detailsOpen = detailsFor === identityKey && !!facts;
  const position = clockText(failure.positionSeconds);
  const body = failureBody(failure.reason, position ?? '', failure.ended);
  return <View testID="watch-playback-failed" pointerEvents="box-none" style={StyleSheet.absoluteFill}>
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, {backgroundColor: '#03080c8c'}]}/>
    {!chromeVisible && identity && <View pointerEvents="none" style={{position: 'absolute', left: 96, right: 96, top: 72}}>
      <Text {...textLines(1)} style={[shellType(44, 800, 48.4, -.03, theme.textScale), {marginBottom: 6, color: theme.colors.text}]}>{identity.title}</Text>
      <View style={{flexDirection: 'row', alignItems: 'baseline', marginTop: 4}}>
        {identity.subtitle.map((part, index) => <Text key={`${part.role}:${index}`} {...textLines(1)}
          style={[shellType(24, part.id === 'episode-title' ? 700 : 600, 32, 0, theme.textScale),
            {marginRight: 22, color: part.id === 'episode-title' ? theme.colors.text : theme.colors.textSecondary}]}>{part.text}</Text>)}
      </View>
    </View>}
    <View pointerEvents="box-none" onLayout={event => {const height = event.nativeEvent.layout.height; if (height !== blockHeight) setBlockHeight(height);}}
      style={{position: 'absolute', left: 96, top: 486, transform: [{translateY: -blockHeight / 2}], width: 1000, maxWidth: '100%'}}>
      <View style={{width: 88, height: 88, borderRadius: 44, marginBottom: 34, alignItems: 'center', justifyContent: 'center',
        backgroundColor: 'rgba(237,245,250,.1)', borderWidth: 1, borderColor: 'rgba(237,245,250,.12)'}}>
        <Icon name="i-warn" size={40} color={theme.colors.amber}/>
      </View>
      <Text accessibilityRole="header" style={[shellType(72, 800, 72, -.045, theme.textScale),
        {marginBottom: 24, color: theme.colors.text, textShadowColor: 'rgba(7,16,24,.5)', textShadowOffset: {width: 0, height: 2}, textShadowRadius: 30}]}>{failureTitle(failure.ended)}</Text>
      <Text style={[shellType(28, 400, 40.6, 0, theme.textScale), {marginBottom: 28, maxWidth: 900, color: theme.colors.textSecondary}]}>{body}</Text>
      {(retry || facts || differentVersion) && <FocusRegion id="watch-failed" orientation="horizontal" edges={{down: 'watch-controls'}} enabled={!detailsOpen}
        entry={retry ? 'watch-failed-retry' : differentVersion ? 'watch-failed-version' : 'watch-failed-details'} entryPolicy="entry" style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: 18}}>
        {retry && <Button focusId="watch-failed-retry" testID="watch-failed-retry" icon="i-restart" preferredFocus disabled={detailsOpen} onPress={() => retry(failure)}>{t('next.states.tryAgain')}</Button>}
        {differentVersion && <Button focusId="watch-failed-version" testID="watch-failed-version" variant="glass" disabled={detailsOpen} onPress={() => differentVersion(failure)}>{t('next.overlays.differentVersion')}</Button>}
        {facts && <Button ref={detailsOpener} focusId="watch-failed-details" testID="watch-failed-details" variant="glass" disabled={detailsOpen} onPress={() => {if (details?.(failure)) setDetailsFor(identityKey);}}>{t('next.states.details')}</Button>}
      </FocusRegion>}
    </View>
    <SidePanel testID="watch-failed-details-panel" open={detailsOpen} onOpenChange={open => setDetailsFor(open ? identityKey : undefined)}
      title={t('next.states.details')} width={700} returnFocusRef={detailsOpener}>{facts}</SidePanel>
  </View>;
}
