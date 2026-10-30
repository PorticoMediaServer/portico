import {Button, type ButtonProps} from '../controls';
import styles from './settings.module.css';

/** The drawn row action sizes and colours; commands and confirmation stay with their domain owner. */
export function SettingsAction({appearance = 'glass', className, size = 'xs', ...props}: Omit<ButtonProps, 'variant'> & {appearance?: 'glass' | 'primary' | 'danger'}) {
  return <Button {...props} size={size} variant={appearance === 'primary' ? 'primary' : 'glass'} className={[appearance === 'danger' ? styles.dangerAction : undefined, className].filter(Boolean).join(' ')} />;
}
