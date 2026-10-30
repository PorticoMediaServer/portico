import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Switch} from '../../../kit/controls/Switch';
import {useTheme} from '../../../kit/theme';

/** One source's recorded advanced media-analysis consent, as a pure view.
 *
 * Undefined means this server published no consent (unsupported or unknown), so
 * nothing is drawn and nothing is implied to be false. A known value draws the
 * one existing kit switch beside its plain warning. The parent owns the draft,
 * the source save and the original opening; this component has no state, effects,
 * API, controller or operation identity of its own. */
export function SourceAnalysisConsent({value, disabled, onChange, t}: {
  value: boolean | undefined;
  disabled: boolean;
  onChange: (value: boolean) => void;
  t: typeof defaultI18n.t;
}) {
  const theme = useTheme();
  if (value === undefined) return null;
  return <View testID="remote-source-analysis-consent" style={{gap: 12}}>
    <Switch variant="phone" checked={value} disabled={disabled} onCheckedChange={onChange}>
      {t('next.remoteSources.allowAdvancedMediaAnalysis')}
    </Switch>
    <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.amber, borderTopWidth: 1,
      borderTopColor: theme.colors.lineStrong, paddingTop: 10}]}>{t('next.remoteSources.allowAdvancedMediaAnalysisWarning')}</Text>
  </View>;
}
