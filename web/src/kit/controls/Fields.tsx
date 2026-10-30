import {forwardRef, useId, type InputHTMLAttributes, type ReactNode} from 'react';
import {Icon, type IconName} from '../Icon';
import styles from './controls.module.css';

export type SwitchProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'type' | 'size'> & {
  onCheckedChange?: (checked: boolean) => void;
  size?: 'compact' | 'phone' | 'episode';
};

export const Switch = forwardRef<HTMLInputElement, SwitchProps>(function Switch({size = 'compact', className, onCheckedChange, onChange, ...props}, ref) {
  return <input {...props} ref={ref} type="checkbox" role="switch" data-size={size} className={[styles.switch, className].filter(Boolean).join(' ')}
    onChange={event => {onChange?.(event); onCheckedChange?.(event.currentTarget.checked);}} />;
});

export type TextFieldProps = InputHTMLAttributes<HTMLInputElement> & {
  label?: ReactNode;
  description?: ReactNode;
  error?: ReactNode;
  errorIcon?: IconName;
  leadingIcon?: IconName;
  trailing?: ReactNode;
  variant?: 'field' | 'compact' | 'setting' | 'search' | 'auth' | 'account-panel' | 'account-panel-code' | 'code' | 'large-search';
  containerClassName?: string;
  /** Presentation only: the field's own shell carries the authored changed ring. */
  changed?: boolean;
};

export const TextField = forwardRef<HTMLInputElement, TextFieldProps>(function TextField({id, label, description, error, errorIcon, leadingIcon, trailing, variant = 'field', containerClassName, className, disabled, changed, 'aria-describedby': describedBy, ...props}, ref) {
  const generated = useId();
  const inputId = id ?? generated;
  const helpId = `${inputId}-help`;
  const errorId = `${inputId}-error`;
  const descriptions = [describedBy, description ? helpId : undefined, error ? errorId : undefined].filter(Boolean).join(' ') || undefined;
  const input = <input {...props} ref={ref} id={inputId} disabled={disabled} className={[styles.input, className].filter(Boolean).join(' ')}
    aria-invalid={error ? true : props['aria-invalid']} aria-describedby={descriptions} />;
  return <div className={[styles.field, containerClassName].filter(Boolean).join(' ')} data-variant={variant} data-disabled={disabled || undefined} data-invalid={Boolean(error) || undefined} data-changed={changed || undefined}>
    {label && <label className={styles.fieldLabel} htmlFor={inputId}>{label}</label>}
    <div className={styles.inputShell}>
      {(leadingIcon || variant === 'search' || variant === 'large-search') && <Icon name={leadingIcon ?? 'i-search'} size={variant === 'large-search' ? 30 : 20} />}
      {variant === 'large-search' ? <div className={styles.searchValue}>{input}<span className={styles.largeSearchPlaceholder} aria-hidden="true">{props.placeholder}</span></div> : input}
      {trailing && <span className={styles.inputTrailing}>{trailing}</span>}
    </div>
    {description && <p id={helpId} className={styles.help}>{description}</p>}
    {error && <p id={errorId} className={styles.error}>{errorIcon && <Icon name={errorIcon} size={16} />}<span>{error}</span></p>}
  </div>;
});
