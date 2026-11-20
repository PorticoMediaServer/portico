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
import {StartFocusScope} from '../focus';
import {useFrameMetrics} from './metrics';

export type SignInFrameProps = Readonly<{
  /** A page after the first has Back; the first page has the wordmark in the same place. */
  back?: TopBarBack;
  /** The accent line over the title ("Sign in to"), with the server's name as the title (auth 6). */
  kicker?: ReactNode;
  /** Profile pickers with their own heading omit the frame title. */
  title?: ReactNode;
  /** The profile being unlocked. */
  identity?: ReactNode;
  /** A dedicated keypad stays below the scroller. */
  footer?: ReactNode;
  /** One line under the title. */
  lede?: ReactNode;
  /** Said above the fields; the fields and the primary button under them keep their order. */
  error?: string;
  /** The form: fields, then the primary button full width, then lesser choices. The one flexible region. */
  children: ReactNode;
  /** Terms and Privacy, held at the bottom. */
  terms?: ReactNode;
  testID?: string;
  /** TV: the control the page starts on (R1), by its `focusId` or `testID`. Never in the rail. */
  startFocus?: string;
}>;

/** Frame 6, the pages before you're in (welcome, sign in, local server, codes, profiles), on a phone and a tablet.
 * Fixed: Back or the wordmark, the title slot, Terms at the bottom. Flexible: the form, which scrolls and stays
 * above the keyboard. On a tablet the same column is centred at 380. TV sign-in is the existing `TVAuthLayout`. */
export function SignInFrame({startFocus, ...props}: SignInFrameProps) {
  return <StartFocusScope id={startFocus}><SignInFrameBody {...props}/></StartFocusScope>;
}
function SignInFrameBody({back, kicker, title, lede, error, children, terms, identity, footer, testID}: Omit<SignInFrameProps, 'startFocus'>) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), column = frameSpec.signIn.column[frame.form];
  const centered = !!identity;
  return <View testID={testID} style={{flex: 1, paddingBottom: footer ? 0 : Math.max(insets.bottom, theme.space.sm)}}>
    {back ? <TopBar back={back}/> : <View style={{paddingTop: insets.top, height: insets.top + frameSpec.header.backRow, paddingHorizontal: frame.gutter, justifyContent: 'center', alignItems: column ? 'center' : 'flex-start'}}>
      <Image source={require('../../../assets/wordmark.png')} accessibilityLabel={defaultI18n.t('accounts.brand')} resizeMode="contain" style={headerWordmark.phone}/>
    </View>}
    <FormScrollView bottomOffset={frameSpec.signIn.keyboardClearance} keyboardShouldPersistTaps="handled" keyboardDismissMode="on-drag" contentContainerStyle={{flexGrow: 1, paddingHorizontal: frame.gutter, paddingTop: frame.headerGap, paddingBottom: theme.space.md, justifyContent: column ? 'center' : 'flex-start'}}>
      <View style={{width: '100%', maxWidth: column || undefined, alignSelf: 'center'}}>
        {identity && <View style={{alignItems: 'center', marginBottom: theme.space.md}}>{identity}</View>}
        {typeof kicker === 'string' ? <Text style={[theme.type.rowLabel, {color: theme.colors.accentStrong, marginBottom: theme.space.xs}]}>{kicker}</Text> : kicker}
        {title !== undefined && <Text accessibilityRole="header" textBreakStrategy="balanced" android_hyphenationFrequency="none" style={[theme.type.pageTitle, {color: theme.colors.text, includeFontPadding: false, textAlign: centered ? 'center' : undefined}]}>{title}</Text>}
        {typeof lede === 'string' ? <Text style={[theme.type.intro, {color: theme.colors.textSecondary, marginTop: frame.headingGap, textAlign: centered ? 'center' : undefined}]}>{lede}</Text> : lede}
        <View style={{marginTop: title ? theme.space.md : 0, gap: theme.space.controlGap}}>
          {error !== undefined && <FormError placement="block">{error}</FormError>}
          {children}
        </View>
      </View>
    </FormScrollView>
    {footer}
    {terms && <View style={{paddingHorizontal: frame.gutter, alignItems: column ? 'center' : 'flex-start'}}>{terms}</View>}
  </View>;
}
