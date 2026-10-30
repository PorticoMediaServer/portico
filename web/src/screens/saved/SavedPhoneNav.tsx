import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';
import styles from './saved.module.css';

const t = defaultI18n.t;
/** phone-web frame 6: a pushed Saved page carries "‹ Saved", its own title and the
 * page's end action, in place of the desktop heading and tabs. Nothing else. */
export function SavedPhoneNav({title, onBack, action}: {title: string; onBack?: () => void; action?: ReactNode}) {
  return <nav className={styles.phoneNav} aria-label={title}>
    <span className={styles.phoneNavStart}>{onBack ? <Button variant="text" size="small" icon="i-left" onClick={onBack}>{t('saved.title')}</Button> : null}</span>
    <b>{title}</b>
    <span className={styles.phoneNavEnd}>{action}</span>
  </nav>;
}
