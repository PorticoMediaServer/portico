import React from 'react';
import {Text, type StyleProp, type TextStyle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {porticoPublicLinks} from '../../../../../packages/client-core/src/public-links';

/** Keep the translated sentence intact while each surface owns link failures and styling. */
export function LegalAgreement({style, linkStyle, onOpenLink}: {
  style?: StyleProp<TextStyle>; linkStyle?: StyleProp<TextStyle>; onOpenLink: (url: string) => void;
}) {
  const t = defaultI18n.t;
  const agreement = t('services.signup.agreement', {terms: '{terms}', privacy: '{privacy}'});
  return <Text style={style}>{agreement.split(/(\{terms\}|\{privacy\})/).map((part, index) => {
    const kind = part === '{terms}' ? 'terms' : part === '{privacy}' ? 'privacy' : undefined;
    return kind ? <Text key={index} accessibilityRole="link" suppressHighlighting onPress={() => onOpenLink(porticoPublicLinks[kind])}
      style={linkStyle}>{t(kind === 'terms' ? 'auth.terms' : 'auth.privacyPolicy')}</Text> : part;
  })}</Text>;
}
