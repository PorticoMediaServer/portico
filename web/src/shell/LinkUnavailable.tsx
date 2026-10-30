import {useRouter, useSearch} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {StateLayout} from '../kit/states';
import {useShellWidth} from './viewport';
import styles from './states.module.css';

const t = defaultI18n.t;
export type LinkUnavailableReason = 'access' | 'offline';
export const linkUnavailableSearch = (raw: Record<string, unknown>): {reason: LinkUnavailableReason} => ({reason: raw.reason === 'offline' ? 'offline' : 'access'});

/** O-161: a shared link for a server this account cannot open, or one that is not answering. The
 * not-found layout (states.html#12): one plain sentence, the colour backdrop and the way Home. */
export function LinkUnavailableState({reason, onHome}: {reason: LinkUnavailableReason; onHome: () => void}) {
  const width = useShellWidth();
  return <StateLayout className={styles.page} backdrop={{kind: 'colour', shape: 'side'}} contentStyle={{top: width >= 1024 ? 330 : 250}}
    title={t(reason === 'offline' ? 'next.states.linkServerOffline' : 'next.states.linkNoAccess')}
    body={t(reason === 'offline' ? 'next.states.linkServerOfflineBody' : 'next.states.linkNoAccessBody')} icon="i-unlink"
    primary={{id: 'home', label: t('next.states.goHome'), icon: 'i-home', onPress: onHome}} />;
}

export function LinkUnavailableScreen() {
  const router = useRouter(), {reason} = useSearch({strict: false}) as {reason: LinkUnavailableReason};
  return <LinkUnavailableState reason={reason} onHome={() => { void router.navigate({to: '/'}); }} />;
}
