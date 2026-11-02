import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import {Dialog} from '../overlays';
import {useTheme} from '../theme';

const t = defaultI18n.t;
export type RatingDialogProps = Readonly<{
  /** The TV focus id of the surface; its stars, Clear, Try again and Close derive from it. */
  testID?: string;
  title: string;
  /** Out of 10 (a star is 2); `null` is not rated; absent while the rating is still being read. */
  value: number | null | undefined;
  phase: 'loading' | 'ready' | 'saving' | 'result' | 'uncertain' | 'error';
  /** The already-localized reason, when the read or write failed. */
  error?: string;
  onRate: (value: number | null) => void;
  onRetry: () => void;
  onClose: () => void;
}>;

/** The one star-rating surface for a single title (Home's Rate… and the Watch menu's). */
export function RatingDialog({testID, title, value, phase, error, onRate, onRetry, onClose}: RatingDialogProps) {
  const theme = useTheme(), id = (suffix: string) => testID ? `${testID}-${suffix}` : undefined;
  return <Dialog testID={testID} open onOpenChange={open => {if (!open) onClose();}} title={t('title.rate', {title})}
    footer={<Button testID={id('close')} variant="glass" size="compact" onPress={onClose}>{t('action.close')}</Button>}>
    <View style={{paddingTop: 16, gap: 16}}>
      {value === undefined ? <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.states.loading')}</Text>
        : <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: theme.isTV ? 12 : 0}} accessibilityRole="radiogroup" accessibilityLabel={t('player.rateThis')}>
          {[1, 2, 3, 4, 5].map(stars => <Focusable key={stars} testID={id(`stars-${stars}`)} accessibilityRole="radio" accessibilityLabel={t('title.stars', {count: stars})}
            accessibilityState={{checked: value === stars * 2, disabled: phase !== 'ready'}} disabled={phase !== 'ready'}
            preferredFocus={theme.isTV && stars === (typeof value === 'number' && value > 0 ? Math.ceil(value / 2) : 1)}
            onPress={() => onRate(stars * 2)} focusRadius={8} hitSlop={6} style={{width: theme.isTV ? 64 : 32, height: theme.isTV ? 64 : 32, alignItems: 'center', justifyContent: 'center'}}>
            <View style={{width: theme.isTV ? 28 : 22, height: theme.isTV ? 28 : 22}}>
              <Icon name="i-star" size={theme.isTV ? 28 : 22} color={theme.colors.textTertiary}/>
              {typeof value === 'number' && value > (stars - 1) * 2 && <View pointerEvents="none" style={{position: 'absolute', left: 0, top: 0, bottom: 0, width: (theme.isTV ? 28 : 22) * Math.min(1, (value - (stars - 1) * 2) / 2), overflow: 'hidden'}}><Icon name="i-star-f" size={theme.isTV ? 28 : 22} color={theme.colors.accentStrong}/></View>}
            </View>
          </Focusable>)}
        </View>}
      {phase === 'ready' && <Button testID={id('clear')} variant="text" size="compact" onPress={() => onRate(null)}>{t('title.clearRating')}</Button>}
      {phase === 'result' && <Text accessibilityLiveRegion="polite" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.selection.native.saved')}</Text>}
      {error && <Text accessibilityLiveRegion="polite" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{error}</Text>}
      {phase === 'uncertain' && <Button testID={id('retry')} variant="glass" size="compact" onPress={onRetry}>{t('action.tryAgain')}</Button>}
    </View>
  </Dialog>;
}
