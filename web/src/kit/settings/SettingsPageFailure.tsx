import {defaultI18n} from '@i18n';
import {OwnerTag} from '../shell/ServerIdentity';
import {StateLayout} from '../states';
import styles from './settings.module.css';

/** Why the server refused a page's read, when it did:
 * - `owner`: this sign-in may no longer manage the server (its role or capability changed). States frame 13c.
 * - `pin`: a remembered main profile past its management hour (PR-1, `profile_pin_manage`). The shell already
 *   asks for the PIN; entering it reopens the page, and Try again reads once more, which asks again. */
export type SettingsRefusal = 'owner' | 'pin';
const ownerCodes = new Set(['forbidden', 'not_permitted', 'owner_required']);
export function settingsRefusal(error: unknown): SettingsRefusal | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const {status, code} = error as {status?: unknown; code?: unknown};
  if (code === 'profile_pin_manage') return 'pin';
  if (typeof code !== 'string') return status === 403 ? 'owner' : undefined;
  // The console transport reads every 403 as `unauthorized`; a 401 is a sign-in that ended, which the shell handles.
  return ownerCodes.has(code) || status === 403 && code === 'unauthorized' ? 'owner' : undefined;
}

type FailureProps = Readonly<{retry: () => void; home: () => void;
  /** What failed. A refusal draws its own state instead of "Something didn't load". */
  error?: unknown;
  /** An owner page (Server settings or the Dashboard): only there does a refusal mean "Only the owner can open this". */
  server?: Readonly<{name: string; openMySettings: () => void}>;
}>;

/** Section 17 uses the drawn page failure while Settings keeps its own heading and navigation. */
export function SettingsPageFailure({retry, home, error, server}: FailureProps) {
  const t = defaultI18n.t, refusal = settingsRefusal(error);
  if (refusal === 'owner' && server) return <SettingsOwnerOnly serverName={server.name} openMySettings={server.openMySettings} home={home} />;
  if (refusal === 'pin') return <StateLayout backdrop={null} className={styles.pageFailure} icon="i-lock"
    title={t('accounts.profiles.pin')} body={t('error.profilePinManage')}
    primary={{id: 'retry', label: t('next.states.tryAgain'), icon: 'i-retry', onPress: retry}}
    secondary={{id: 'home', label: t('next.states.goHome'), onPress: home}} />;
  return <StateLayout backdrop={null} className={styles.pageFailure} icon="i-warn" tone="warning"
    title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
    primary={{id: 'retry', label: t('next.states.tryAgain'), icon: 'i-retry', onPress: retry}}
    secondary={{id: 'home', label: t('next.states.goHome'), onPress: home}} />;
}

/** States frame 13c: an owner page this sign-in may not open. The owner's mark says whose area it is, and the
 * first button goes to the viewer's own settings. */
export function SettingsOwnerOnly({serverName, openMySettings, home}: Readonly<{serverName: string; openMySettings: () => void; home: () => void}>) {
  const t = defaultI18n.t;
  return <StateLayout backdrop={null} className={styles.pageFailure} icon="i-lock"
    eyebrow={<OwnerTag variant="state" identity={t('next.shell.serverSettings')} />}
    title={t('next.states.ownerRequired')} body={t('next.states.ownerRequiredBody', {server: serverName})}
    primary={{id: 'settings', label: t('next.states.mySettings'), onPress: openMySettings}}
    secondary={{id: 'home', label: t('next.states.goHome'), onPress: home}} />;
}
