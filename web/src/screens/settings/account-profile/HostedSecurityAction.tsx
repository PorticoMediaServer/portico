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
export function HostedSecurityAction({label, disabled, onOpen}: {
  /** The drawn Change password wording unless the row names its own. A Google- or Apple-only
   * account never gets Set a password here: its Add a password row sits behind More sign-in options. */
  label?: string;
  disabled: boolean;
  onOpen: () => void;
}) {
  const wording = label ?? t('next.settingsSpec.account.profile.groups.5.rows.1.control.label');
  return <Button size="small" variant="glass" disabled={disabled} onClick={onOpen}>{wording}</Button>;
}
