import {useRouter} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {StateLayout} from '../kit/states';
import {useShellWidth} from './viewport';
import styles from './states.module.css';

const t = defaultI18n.t;

/** states.html#12. A bad or old link keeps the app's own frame: the shell draws
 * the navigation, and only this page's copy, actions and colour backdrop change.
 * The frame's "Proposed" Continue Watching row is not drawn here: it would need a
 * Home read on every miss, and the drawn page itself already says where to go. */
export function NotFoundState({onHome, onSearch}: {onHome: () => void; onSearch: () => void}) {
  const width = useShellWidth();
  return <StateLayout className={styles.page} backdrop={{kind: 'colour', shape: 'side'}}
    contentStyle={{top: width >= 1024 ? 330 : 250}}
    title={t('next.states.notFound')} body={t('next.states.notFoundBody')} icon="i-unlink"
    primary={{id: 'home', label: t('next.states.goHome'), icon: 'i-home', onPress: onHome}}
    secondary={{id: 'search', label: t('next.home.shell.search'), icon: 'i-search', onPress: onSearch}} />;
}

export function NotFoundScreen() {
  const router = useRouter();
  return <NotFoundState onHome={() => { void router.navigate({to: '/'}); }}
    onSearch={() => { void router.navigate({to: '/search', search: {q: ''}}); }} />;
}
