import {forwardRef, useState, type ButtonHTMLAttributes, type ReactNode} from 'react';
import {Icon, type IconName} from '../Icon';
import styles from './controls.module.css';

export type ChipProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'default' | 'toolbar' | 'quick' | 'facet' | 'applied' | 'pill' | 'search' | 'download';
  selected?: boolean;
  defaultSelected?: boolean;
  onSelectedChange?: (selected: boolean) => void;
  count?: ReactNode;
  countStyle?: 'text' | 'badge';
  prefix?: ReactNode;
  dim?: boolean;
  retry?: boolean;
  icon?: IconName;
  trailingIcon?: IconName;
  phone?: boolean;
  iconSize?: number;
  trailingSize?: number;
};

export const Chip = forwardRef<HTMLButtonElement, ChipProps>(function Chip({variant = 'default', selected, defaultSelected, onSelectedChange, count, countStyle = 'text', prefix, dim, retry, icon, trailingIcon, phone, iconSize = 16, trailingSize, type = 'button', children, className, onClick, ...props}, ref) {
  const [localSelected, setLocalSelected] = useState(defaultSelected ?? false);
  const checkbox = props.role === 'checkbox' || variant === 'facet';
  const toggle = checkbox || selected !== undefined || defaultSelected !== undefined || onSelectedChange !== undefined;
  const checked = selected ?? localSelected;
  const leading = retry && variant === 'download' ? 'i-retry' : icon;
  const trailing = retry && variant !== 'download' ? 'i-retry' : trailingIcon;
  return <button {...props} ref={ref} type={type} role={checkbox ? 'checkbox' : props.role} aria-checked={checkbox ? checked : props['aria-checked']} aria-pressed={checkbox || props.role === 'radio' ? undefined : toggle ? checked : props['aria-pressed']}
    data-variant={variant} data-selected={(toggle && checked) || undefined} data-dim={dim || undefined} data-phone={phone || undefined} data-retry={retry || undefined}
    onClick={event => {onClick?.(event); if (toggle && !event.defaultPrevented) {if (selected === undefined) setLocalSelected(!checked); onSelectedChange?.(!checked);}}}
    className={[styles.chip, className].filter(Boolean).join(' ')}>
    {leading && <Icon name={leading} size={iconSize} />}
    {prefix && <span className={styles.dim}>{prefix}</span>}
    <span className={styles.chipLabel}>{children}</span>
    {count !== undefined && count !== null && <span className={countStyle === 'badge' ? styles.countBadge : styles.count}>{count}</span>}
    {trailing && (variant === 'applied'
      ? <span className={styles.removeGlyph}><Icon name={trailing} size={14} /></span>
      : <Icon className={styles.trailing} name={trailing} size={trailingSize ?? (variant === 'default' && !phone ? 18 : 16)} />)}
  </button>;
});
