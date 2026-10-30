import React from 'react';
import {Text, View} from 'react-native';
import type {DAVConnectionResult} from '../../../../../../packages/client-core/src/remote-sources';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Icon} from '../../../kit/Icon';
import {alpha} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import {connectionTestTone} from './reading-presentation';

/** Only the facts returned by the dry run are shown: no invented steps or counts. */
export function ConnectionTestCard({testing, result, error, t, disabled, onTestAgain}: {
  testing: boolean; result?: DAVConnectionResult; error?: string; t: typeof defaultI18n.t; disabled?: boolean; onTestAgain?: () => void;
}) {
  const theme = useTheme();
  if (!testing && !result && !error) return null;
  const tone = result ? connectionTestTone(result) : 'success';
  const color = testing ? theme.colors.accentStrong : error ? theme.colors.record : tone === 'success' ? theme.colors.healthy : theme.colors.amber;
  const title = testing ? t('next.remoteSources.testing') : error ? t('next.dock.case.error') : tone === 'limited' ? t('next.remoteSources.testedLimitedTitle') : t('web.general.connectedBadge');
  const detail = error ?? (result ? tone === 'success' ? t('next.remoteSources.tested', {origin: result.origin}) : tone === 'limited' ? t('next.remoteSources.testedLimited', {origin: result.origin}) : t('next.remoteSources.testedUnknown') : undefined);
  return <View accessibilityLiveRegion="polite" style={{padding: 14, borderRadius: 14, backgroundColor: alpha(color, .1), borderWidth: 1, borderColor: alpha(color, .3), flexDirection: 'row', gap: 10}}>
    <Icon name={testing ? 'i-info' : error || tone !== 'success' ? 'i-alert' : 'i-check'} size={20} color={color}/>
    <View style={{flex: 1, gap: 4}}><Text style={[theme.type.bodyStrong, {color}]}>{title}</Text>{detail && <Text accessibilityRole={error ? 'alert' : undefined} style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{detail}</Text>}{!testing && onTestAgain && <Button variant="glass" size="small" disabled={disabled} onPress={onTestAgain}>{t('next.remoteSources.testAgain')}</Button>}</View>
  </View>;
}
