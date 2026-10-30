import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';

const t = defaultI18n.t;

/**
 * SET-4AA18AB: the action control on the account-profile Sign-in rows. The work
 * happens here, in the page's own canonical Hosted SecurityTask dialog, so this is
 * a plain glass/small button carrying only what the owner hands it — wording,
 * disabled state and one callback. No URL, navigation, identity lookup, API,
 * hidden request or click-side authority: the owner's callback opens the dialog it
 * guards, in place.
 */
export function HostedSecurityAction({label, hasPassword, disabled, onOpen}: {
  label?: string;
  /** Provider-only accounts say Set a password; an absent value keeps the drawn
   * Change password action, so existing callers read exactly as before. */
  hasPassword?: boolean;
  disabled: boolean;
  onOpen: () => void;
}) {
  const wording = label ?? t(hasPassword === false ? 'account.setPassword' : 'next.settingsSpec.account.profile.groups.5.rows.1.control.label');
  return <Button size="small" variant="glass" disabled={disabled} onClick={onOpen}>{wording}</Button>;
}
