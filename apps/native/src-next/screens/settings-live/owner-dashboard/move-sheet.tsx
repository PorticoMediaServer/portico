import React, {useEffect, useMemo, useRef, useState} from 'react';
import {PanResponder, Text, View} from 'react-native';
import {clampToLength, positionClock, sessionViewer} from '../../../../../../packages/client-core/src/server-admin/now-playing';
import type {AdminSession} from '../../../../../../packages/client-core/src/playback-v1/types';
import {Button} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays';
import {useTheme} from '../../../kit/theme';
import {alpha, shellType} from '../../../kit/shell/visual';
import type {Translate} from './model';

/**
 * Move to…: the real bar that jumps someone else's playback (owner-dashboard frame 6a). The bar
 * sets where playback would land; the thin grey tick stays on where it is now. Move here sends
 * the absolute position through sendSeekTo, so nothing here guesses a position.
 */
export function OwnerMoveSheet({session, t, busy, onCancel, onMove}: {
  session?: AdminSession;
  t: Translate;
  busy: boolean;
  onCancel: () => void;
  onMove: (positionMs: number) => void;
}) {
  const theme = useTheme(), [target, setTarget] = useState(0), width = useRef(0);
  const span = session?.durationMs && session.durationMs > 0 ? session.durationMs : 0;
  const current = session?.positionMs ?? 0;
  useEffect(() => {setTarget(current);}, [session?.id, current]);
  const gesture = useMemo(() => PanResponder.create({
    onStartShouldSetPanResponder: () => span > 0,
    onMoveShouldSetPanResponder: () => span > 0,
    onPanResponderGrant: event => setTarget(span > 0 ? clampToLength(Math.max(0, Math.min(1, event.nativeEvent.locationX / Math.max(1, width.current))) * span, span) : 0),
    onPanResponderMove: event => setTarget(span > 0 ? clampToLength(Math.max(0, Math.min(1, event.nativeEvent.locationX / Math.max(1, width.current))) * span, span) : 0),
  }), [span]);
  const fraction = (position: number) => span > 0 ? Math.max(0, Math.min(1, position / span)) : 0;
  return <BottomSheet open={Boolean(session)} onOpenChange={open => {if (!open && !busy) onCancel();}} testID="owner-move-sheet"
    title={session ? t('web.nowPlaying.moveTitle', {title: session.title || t('web.nowPlaying.untitled')}) : t('web.nowPlaying.moveTo')}
    description={session ? t('next.dashboard.moveBody', {viewer: sessionViewer(session, t), device: session.device}) : undefined}
    footer={<View style={{flexDirection: 'row', gap: 8}}>
      <Button variant="glass" disabled={busy} onPress={onCancel}>{t('action.cancel')}</Button>
      <Button variant="primary" disabled={busy || !session || span <= 0} loading={busy} onPress={() => onMove(target)}>{t('web.nowPlaying.moveConfirm')}</Button>
    </View>}>
    <View onLayout={event => {width.current = event.nativeEvent.layout.width;}} style={{height: 44, justifyContent: 'center', marginTop: 10}}>
      <View {...gesture.panHandlers} accessible accessibilityRole="adjustable" accessibilityLabel={t('player.position')}
        accessibilityValue={{min: 0, max: Math.max(0, span), now: Math.max(0, Math.min(target, span)), text: t('player.clockOf', {position: positionClock(target), duration: positionClock(span)})}}
        accessibilityActions={[{name: 'increment'}, {name: 'decrement'}]}
        onAccessibilityAction={event => {if (span <= 0 || !['increment', 'decrement'].includes(event.nativeEvent.actionName)) return; const step = event.nativeEvent.actionName === 'increment' ? 30_000 : -30_000; setTarget(clampToLength(target + step, span));}}
        testID="owner-move-track" style={{height: 44, justifyContent: 'center'}}>
        <View pointerEvents="none" style={{height: 6, borderRadius: 3, backgroundColor: alpha(theme.colors.text, .16)}}>
          <View style={{height: 6, borderRadius: 3, width: `${Math.round(fraction(target) * 1000) / 10}%`, backgroundColor: theme.colors.text, opacity: .9}}/>
          {span > 0 && <View style={{position: 'absolute', top: -6, left: `${Math.round(fraction(target) * 1000) / 10}%`, marginLeft: -9, width: 18, height: 18, borderRadius: 9, backgroundColor: theme.colors.text}}/>}
          {span > 0 && <View style={{position: 'absolute', top: -3, left: `${Math.round(fraction(current) * 1000) / 10}%`, width: 2, height: 12, borderRadius: 1, backgroundColor: theme.colors.textTertiary}}/>}
        </View>
      </View>
    </View>
    <View style={{flexDirection: 'row', justifyContent: 'space-between', marginTop: 8}}>
      <Text style={[shellType(13, 600), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{t('player.clockOf', {position: positionClock(target), duration: positionClock(span)})}</Text>
      <Text style={[shellType(13, 600), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{t('next.dashboard.nowAt', {position: positionClock(current)})}</Text>
    </View>
  </BottomSheet>;
}
