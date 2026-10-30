import {defaultI18n, type MessageId} from '@i18n';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import styles from './reading-choice-fold.module.css';

const t = defaultI18n.t;

/** The authored collapsed Reading choice line while Advanced is open. It draws
 * only the choice the caller actually selected and the original Change
 * callback; a Custom choice keeps its own summary above this presenter. */
export function ReadingChoiceFold({label, hint, recommended, disabled, phone = false, onChange}: Readonly<{
  label: MessageId;
  hint: MessageId;
  recommended: boolean;
  disabled: boolean;
  phone?: boolean;
  onChange: () => void;
}>) {
  return <div className={styles.fold} data-phone={phone || undefined}>
    <span className={styles.icon} aria-hidden="true"><Icon name="i-sliders" size={22}/></span>
    <span className={styles.text}>
      <b>{t(label)}{recommended && <span className={styles.tag}>{t('next.remoteSources.choiceRecommended')}</span>}</b>
      <span className={styles.hint}>{t(hint)}</span>
    </span>
    <Button size="xs" variant="glass" className={styles.change} disabled={disabled} onClick={onChange}>{t('next.remoteSources.change')}</Button>
  </div>;
}
