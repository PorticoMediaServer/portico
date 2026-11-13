import {forwardRef, useCallback, useLayoutEffect, useRef, useState, type ButtonHTMLAttributes, type ReactNode} from 'react';
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
/** What a Resume button leaves out when its label would be cut (a phone's row beside round buttons, phone-detail and
 * phone-read 3): first the time left or percent, then the icon; only then does the label wrap (3). The label and the
 * progress line always stay. */
type ResumeDrop = 0 | 1 | 2 | 3;
export const ResumeButton = forwardRef<HTMLButtonElement, ResumeButtonProps>(function ResumeButton({progress, remaining, children, variant = 'primary', icon = 'i-play', className, ...props}, ref) {
  const own = useRef<HTMLButtonElement | null>(null), [drop, setDrop] = useState<ResumeDrop>(0);
  const attach = useCallback((node: HTMLButtonElement | null) => {
    own.current = node;
    if (typeof ref === 'function') ref(node); else if (ref) ref.current = node;
  }, [ref]);
  useLayoutEffect(() => {
    const button = own.current, label = button?.querySelector<HTMLElement>('[data-resume-label]');
    if (!button || !label) return;
    // Measured with each part shown in turn, widest first; a button sized to its content always fits.
    const fit = () => {
      let level: ResumeDrop = 0;
      button.dataset.drop = '0';
      while (level < 3 && label.scrollWidth > label.clientWidth + .5) { level = (level + 1) as ResumeDrop; button.dataset.drop = String(level); }
      setDrop(level);
    };
    fit();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(fit); observer.observe(button);
    return () => observer.disconnect();
  }, [children, remaining, icon]);
  // The remaining time is a separate visual span, so the rendered text alone reads as
  // "Resume1 h 26 min left". Name the control with one space between the two parts while
  // leaving the drawn layout unchanged.
  const named = typeof children === 'string' && typeof remaining === 'string' ? `${children} ${remaining}` : undefined;
  return <Button {...props} ref={attach} data-drop={drop} aria-label={props['aria-label'] ?? named} variant={variant} icon={icon} className={[styles.resume, className].filter(Boolean).join(' ')}>
    <span className={styles.resumeLabel} data-resume-label>{children}{remaining !== undefined && remaining !== null && <span className={styles.remaining}>{remaining}</span>}</span>
    <span className={styles.progress} aria-hidden="true"><span style={{transform: `scaleX(${progressFraction(progress)})`}} /></span>
  </Button>;
});
