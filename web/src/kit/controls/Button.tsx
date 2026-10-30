import {forwardRef, type ButtonHTMLAttributes, type ReactNode} from 'react';
import {Icon, type IconName} from '../Icon';
import {progressFraction} from './model';
import styles from './controls.module.css';

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'glass' | 'text' | 'icon' | 'ghost';
  size?: 'hero' | 'compact' | 'small' | 'xs' | 'episode' | 'dock';
  icon?: IconName;
  iconSize?: number;
  iconOnly?: boolean;
  loading?: boolean;
  phone?: boolean;
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button({variant = 'glass', size = 'hero', icon, iconSize, iconOnly = false, loading = false, phone = false, children, className, disabled, type = 'button', ...props}, ref) {
  return <button {...props} ref={ref} type={type} disabled={disabled || loading} aria-busy={loading || props['aria-busy'] || undefined}
    data-size={size} data-variant={variant} data-phone={phone || undefined} data-icon-only={iconOnly || undefined}
    className={[styles.button, className].filter(Boolean).join(' ')}>
    {icon && (loading ? <span className={styles.spinner} aria-hidden="true" style={iconSize ? {width: iconSize, height: iconSize} : undefined} /> : <Icon name={icon} className={styles.glyph} size={iconSize} style={iconSize ? {width: iconSize, height: iconSize} : undefined} />)}
    {children !== undefined && children !== null && <span className={styles.buttonLabel}>{children}</span>}
  </button>;
});

export type ResumeButtonProps = ButtonProps & {progress: number; remaining?: ReactNode};
export const ResumeButton = forwardRef<HTMLButtonElement, ResumeButtonProps>(function ResumeButton({progress, remaining, children, variant = 'primary', icon = 'i-play', className, ...props}, ref) {
  // The remaining time is a separate visual span, so the rendered text alone reads as
  // "Resume1 h 26 min left". Name the control with one space between the two parts while
  // leaving the drawn layout unchanged.
  const named = typeof children === 'string' && typeof remaining === 'string' ? `${children} ${remaining}` : undefined;
  return <Button {...props} ref={ref} aria-label={props['aria-label'] ?? named} variant={variant} icon={icon} className={[styles.resume, className].filter(Boolean).join(' ')}>
    <span className={styles.resumeLabel}>{children}{remaining !== undefined && remaining !== null && <span className={styles.remaining}>{remaining}</span>}</span>
    <span className={styles.progress} aria-hidden="true"><span style={{transform: `scaleX(${progressFraction(progress)})`}} /></span>
  </Button>;
});
