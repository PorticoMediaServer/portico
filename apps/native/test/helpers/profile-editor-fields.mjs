import {screenModule} from './screen-harness.mjs';
import * as values from '../../src-next/screens/accounts/account-form-values.ts';

/** Real shared controls under the same hook host as the owning sheet. */
export function profileEditorFields(react, theme) {
  return screenModule(new URL('../../src-next/screens/settings-live/ProfileEditorFields.tsx', import.meta.url), {
    react: {...react, default: react}, 'react-native': {Text: 'Text', View: 'View'},
    '@portico/design': {profileArtColors: {blue: 'blue', violet: 'violet', mint: 'mint', coral: 'coral', gold: 'gold', slate: 'slate', rose: 'rose', sky: 'sky'}},
    '../../../../../packages/i18n/src': {defaultI18n: {t: key => key}},
    '../../../../../packages/client-core/src/profile-management': {PROFILE_ART: ['blue', 'violet', 'mint', 'coral', 'gold', 'slate', 'rose', 'sky']},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', TextField: 'TextField'}, '../../kit/overlays': {Dialog: 'Dialog'}, '../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../kit/theme': {useTheme: () => theme}, '../accounts/account-form-values': values,
  });
}
