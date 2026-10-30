import {forwardRef, useId, useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {presentError} from '@core/presentation/errors.ts';
import {ColourBackdrop} from '../artwork';
import {TextField, type TextFieldProps} from '../controls/Fields';
import {Icon} from '../Icon';
import wordmark from '../../shell/wordmark.svg';
import styles from './accounts.module.css';

const t = defaultI18n.t;
/** The public auth surface has colour only. It never accepts library artwork. */
/** `shape` and `phoneShape` are kept for callers but no longer change anything: every stage draws
 * the one standard backdrop (kit/backdrop.css), which already carries the phone band. */
export function AuthStage({children, layout = 'column', service = false, topRight, legal = true, bare = false, className, contentClassName}: {
  children: ReactNode; shape?: 'side' | 'round' | 'top'; phoneShape?: 'side' | 'round' | 'top'; layout?: 'column' | 'center'; service?: boolean; topRight?: ReactNode; legal?: boolean; bare?: boolean;
  className?: string; contentClassName?: string;
}) {
  // A bare stage keeps the same content and controls inside a dialog, without the
  // page backdrop, wordmark or legal footer it already sits within.
  if (bare) return <section className={[styles.stage, className].filter(Boolean).join(' ')} data-layout={layout} data-bare={true}>
    <div className={[styles.content, contentClassName].filter(Boolean).join(' ')} data-auth-content>{children}</div>
  </section>;
  return <section className={[styles.stage, className].filter(Boolean).join(' ')} data-layout={layout}>
    <ColourBackdrop className={styles.standardBackdrop} />
    <header className={styles.brand}><img src={wordmark} alt={t('accounts.brand')} />{service && <span>{t('web.brand.player')}</span>}</header>
    {topRight && <div className={styles.topRight}>{topRight}</div>}
    <div className={[styles.content, contentClassName].filter(Boolean).join(' ')} data-auth-content>{children}</div>
    {legal && <footer className={styles.legal}><a href="https://getportico.tv/terms">{t('auth.terms')}</a><a href="https://getportico.tv/privacy">{t('accounts.legal.privacy')}</a></footer>}
  </section>;
}
export function AuthLinks({children}: {children: ReactNode}) { return <div className={styles.links}>{children}</div>; }

/** The auth1d preparing state shares the public stage's colour-only backdrop. */
export function AuthLoading({children}: {children: ReactNode}) {
  return <div className={styles.loadingCard} role="status"><span className={styles.loadingSpinner} aria-hidden="true"/><h1>{children}</h1></div>;
}

/** One real password input retains browser autofill and native password-manager support. */
export const AuthPasswordField = forwardRef<HTMLInputElement, Omit<TextFieldProps, 'type' | 'trailing'>>(function AuthPasswordField(props, ref) {
  const [shown, setShown] = useState(false);
  return <TextField {...props} ref={ref} variant="auth" type={shown ? 'text' : 'password'} trailing={<button type="button" className={styles.reveal}
    aria-label={t(shown ? 'field.hidePassword' : 'field.showPassword')} aria-pressed={shown} onClick={() => setShown(value => !value)}>
    <Icon name="i-eye" size={20} />{!shown && <span>{t('accounts.signIn.showPassword')}</span>}
  </button>} />;
});

/** A single accessible input owns typing, paste, selection and one-time-code autofill.
 * The boxes are visual only; completing a value never moves focus to another field. */
export function AuthCodeBoxes({length, value, onChange, onComplete, label, error, disabled = false, autoFocus = false, grouped = false}: {
  length: 4 | 6; value: string; onChange: (value: string) => void; onComplete: (value: string) => void;
  label: string; error?: ReactNode; disabled?: boolean; autoFocus?: boolean; grouped?: boolean;
}) {
  const id = useId(), input = useRef<HTMLInputElement>(null), [focused, setFocused] = useState(false);
  return <div className={styles.codeGroup}>
    <div className={styles.codeBoxes} data-pin={length === 4 || undefined} data-error={Boolean(error) || undefined} data-focused={focused || undefined} data-grouped={length === 6 && grouped || undefined} onClick={() => input.current?.focus()}>
      <input ref={input} type="text" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]*" maxLength={length}
        autoFocus={autoFocus} disabled={disabled} aria-label={label} aria-invalid={Boolean(error) || undefined} aria-describedby={error ? id : undefined}
        value={value} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)} onChange={event => {
          const next = event.currentTarget.value.replace(/[^0-9]/g, '').slice(0, length); onChange(next);
          if (next.length === length && next !== value) onComplete(next);
        }} />
      {Array.from({length}, (_, index) => <span key={index} aria-hidden="true" data-filled={length === 4 && index < value.length || undefined} data-current={focused && index === value.length || undefined}>{length === 6 ? value[index] : null}</span>)}
    </div>
    {error && <p className={styles.codeError} id={id} role="alert">{error}</p>}
  </div>;
}
export function AuthError({error}: {error: unknown}) {
  if (!error) return null;
  const result = presentError(error, 'sign-in', {operation: 'action', i18n: defaultI18n});
  return result.silent ? null : <p className={styles.error} role="alert"><Icon name="i-warn" size={20} />{result.body}</p>;
}
