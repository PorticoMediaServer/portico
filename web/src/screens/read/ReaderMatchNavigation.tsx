import {defaultI18n} from '@i18n';
import {Icon} from '../../kit/Icon';
import styles from './match-navigation.module.css';

/** Only retained results of this publication's real search feed these callbacks. */
export function ReaderMatchNavigation({query, onStep, onClose}: Readonly<{
  query: string; onStep: (direction: 'previous' | 'next') => void; onClose: () => void;
}>) {
  const {t} = defaultI18n;
  return <nav className={styles.navigation} aria-label={t('next.reader.search')}>
    <span>{query}</span>
    <button type="button" aria-label={t('next.reader.previousMatch')} onClick={() => onStep('previous')}><Icon name="i-left" size={20}/></button>
    <button type="button" aria-label={t('next.reader.nextMatch')} onClick={() => onStep('next')}><Icon name="i-right" size={20}/></button>
    <button type="button" aria-label={t('next.reader.closeSearch')} onClick={onClose}><Icon name="i-x" size={20}/></button>
  </nav>;
}
