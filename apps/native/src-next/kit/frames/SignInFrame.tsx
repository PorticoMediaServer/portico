import React, {type ReactNode} from 'react';
import {Image, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {FormError} from '../controls/FormError';
import {TopBar, type TopBarBack} from '../layout/TopBar';
import {headerWordmark} from '../shell/brand';
import {FormScrollView} from '../../platform/keyboard';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

export type SignInFrameProps = Readonly<{
  /** A page after the first has Back; the first page has the wordmark in the same place. */
  back?: TopBarBack;
  /** The accent line over the title ("Sign in to"), with the server's name as the title (auth 6). */
  kicker?: string;
  title: string;
  /** One line under the title. */
  lede?: string;
  /** Said above the fields; the fields and the primary button under them keep their order. */
  error?: string;
  /** The form: fields, then the primary button full width, then lesser choices. The one flexible region. */
  children: ReactNode;
  /** Terms and Privacy, held at the bottom. */
  terms?: ReactNode;
  testID?: string;
}>;

/** Frame 6, the pages before you're in (welcome, sign in, local server, codes, profiles), on a phone and a tablet.
 * Fixed: Back or the wordmark, the title slot, Terms at the bottom. Flexible: the form, which scrolls and stays
 * above the keyboard. On a tablet the same column is centred at 380. TV sign-in is the existing `TVAuthLayout`. */
export function SignInFrame({back, kicker, title, lede, error, children, terms, testID}: SignInFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), column = frameSpec.signIn.column[frame.form];
  return <View testID={testID} style={{flex: 1, paddingBottom: Math.max(insets.bottom, theme.space.sm)}}>
    {back ? <TopBar back={back}/> : <View style={{paddingTop: insets.top, height: insets.top + frameSpec.header.backRow, paddingHorizontal: frame.gutter, justifyContent: 'center', alignItems: column ? 'center' : 'flex-start'}}>
      <Image source={require('../../../assets/wordmark.png')} accessibilityLabel={defaultI18n.t('accounts.brand')} resizeMode="contain" style={headerWordmark.phone}/>
    </View>}
    <FormScrollView keyboardShouldPersistTaps="handled" keyboardDismissMode="on-drag" contentContainerStyle={{flexGrow: 1, paddingHorizontal: frame.gutter, paddingTop: frame.headerGap, paddingBottom: theme.space.md, justifyContent: column ? 'center' : 'flex-start'}}>
      <View style={{width: '100%', maxWidth: column || undefined, alignSelf: 'center'}}>
        {kicker !== undefined && <Text style={[theme.type.rowLabel, {color: theme.colors.accentStrong, marginBottom: theme.space.xs}]}>{kicker}</Text>}
        <Text accessibilityRole="header" textBreakStrategy="balanced" android_hyphenationFrequency="none" style={[theme.type.pageTitle, {color: theme.colors.text, includeFontPadding: false}]}>{title}</Text>
        {lede !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textSecondary, marginTop: frame.headingGap}]}>{lede}</Text>}
        <View style={{marginTop: theme.space.md, gap: theme.space.controlGap}}>
          {error !== undefined && <FormError placement="block">{error}</FormError>}
          {children}
        </View>
      </View>
    </FormScrollView>
    {terms && <View style={{paddingHorizontal: frame.gutter, alignItems: column ? 'center' : 'flex-start'}}>{terms}</View>}
  </View>;
}
