import {defaultI18n} from '@i18n';
import styles from './reader.module.css';

/** Phone reader sheets have no drawn close action; keep one in their focus scope. */
export function ReaderSheetClose({onClose}: Readonly<{onClose: () => void}>) {
  return <button type="button" className={styles.srOnly} onClick={onClose}>{defaultI18n.t('next.overlays.close')}</button>;
}
