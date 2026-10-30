import {useOverlayPortalTarget} from '../overlays/portal-target';
import {Fragment, forwardRef, useCallback, useEffect, useId, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode, type CSSProperties, type RefObject, type RefCallback} from 'react';
import {Select as SelectPrimitive} from 'radix-ui';
import {defaultI18n} from '@i18n';
import {Dialog} from '../overlays/Dialog';
import {Icon} from '../Icon';
import {moveOption, matchingOption} from './model';
import styles from './controls.module.css';

export type SelectOption = Readonly<{value: string; label: string; description?: ReactNode; isDefault?: boolean; disabled?: boolean; separatorBefore?: boolean}>;
export type SelectProps = {
  options: readonly SelectOption[];
  value?: string;
  defaultValue?: string;
  onValueChange?: (value: string) => void;
  open?: boolean;
  defaultOpen?: boolean;
  onOpenChange?: (open: boolean) => void;
  label?: ReactNode;
  description?: ReactNode;
  error?: ReactNode;
  'aria-label'?: string;
  'aria-labelledby'?: string;
  'aria-describedby'?: string;
  disabled?: boolean;
  required?: boolean;
  name?: string;
  form?: string;
  id?: string;
  placeholder?: string;
  /** A compact filter caption; option text and submitted values stay unchanged. */
  selectedLabel?: string;
  sheetTitle?: ReactNode;
  className?: string;
  containerClassName?: string;
  style?: CSSProperties;
};

type ChoiceSelectProps = SelectProps & {rich?: boolean};
const phoneQuery = '(max-width: 599px)';
function subscribePhone(change: () => void) {
  const media = window.matchMedia(phoneQuery);
  media.addEventListener('change', change);
  return () => media.removeEventListener('change', change);
}
const phoneSnapshot = () => window.matchMedia(phoneQuery).matches;
const desktopSnapshot = () => false;
function tabFrom(trigger: HTMLElement, direction: number) {
  const stops = [...document.querySelectorAll<HTMLElement>('a[href],button,input,select,textarea,[tabindex]')].filter(element =>
    element.tabIndex >= 0 && !element.hasAttribute('disabled') && !element.hasAttribute('data-radix-focus-guard') && !element.closest('[inert],[hidden],[aria-hidden="true"]') && element.getClientRects().length > 0);
  const index = stops.indexOf(trigger);
  if (index >= 0) stops[index + direction]?.focus();
}

const ChoiceSelect = forwardRef<HTMLButtonElement, ChoiceSelectProps>(function ChoiceSelect({options, value, defaultValue, onValueChange, open, defaultOpen, onOpenChange, label, description, error, disabled, required, name, form, id, placeholder, selectedLabel, sheetTitle, className, containerClassName, style, rich, 'aria-label': accessibleLabel, 'aria-labelledby': labelledBy, 'aria-describedby': describedBy}, ref) {
  const portalTarget = useOverlayPortalTarget();
  const generated = useId();
  const phone = useSyncExternalStore(subscribePhone, phoneSnapshot, desktopSnapshot);
  const [localValue, setLocalValue] = useState(defaultValue ?? '');
  const [localOpen, setLocalOpen] = useState(defaultOpen ?? false);
  const initialValue = useRef(defaultValue ?? value ?? '');
  const trigger = useRef<HTMLButtonElement | null>(null);
  const [menu, setMenu] = useState<HTMLDivElement | null>(null), [menuFit, setMenuFit] = useState({width: 304, stacked: false});
  const tabDirection = useRef<number | null>(null);
  const currentValue = value ?? localValue;
  const currentOpen = open ?? localOpen;
  useLayoutEffect(() => {
    if (!currentOpen || phone || !rich) return;
    const surface = menu; if (!surface) return;
    const measure = () => {
      const canvas = document.createElement('canvas').getContext('2d'); if (!canvas) return;
      const widthOf = (element: HTMLElement | null) => {if (!element) return 0; canvas.font = getComputedStyle(element).font; return canvas.measureText(element.textContent ?? '').width;};
      const sizes = [...surface.querySelectorAll<HTMLElement>('[data-option-value]')].map(option => {
        const name = option.querySelector<HTMLElement>(`.${styles.optionName}`), tag = name?.querySelector<HTMLElement>(`.${styles.defaultTag}`);
        return {name: widthOf(name?.firstElementChild as HTMLElement) + (tag ? widthOf(tag) + 22 : 0), hint: widthOf(option.querySelector<HTMLElement>(`.${styles.optionDescription}`))};
      });
      const natural = Math.max(264, ...sizes.map(size => size.name + size.hint + 76)), stacked = natural > 480;
      const width = Math.min(480, stacked ? Math.max(264, ...sizes.map(size => Math.max(size.name, size.hint) + 66)) : natural);
      setMenuFit(old => old.width === width && old.stacked === stacked ? old : {width, stacked});
    };
    measure(); let active = true; void document.fonts?.ready.then(() => {if (active) measure();});
    return () => {active = false;};
  }, [currentOpen, phone, rich, options, menu]);
  const changeValue = (next: string) => {if (value === undefined) setLocalValue(next); if (next !== currentValue) onValueChange?.(next);};
  const changeOpen = (next: boolean) => {if (open === undefined) setLocalOpen(next); onOpenChange?.(next);};
  const bindTrigger = useCallback((element: HTMLButtonElement | null) => {
    trigger.current = element;
    const callback = typeof ref === 'function' ? ref as RefCallback<HTMLButtonElement> : undefined;
    const cleanup = callback?.(element);
    if (ref && typeof ref !== 'function') ref.current = element;
    if (element) return () => {
      trigger.current = null;
      if (typeof cleanup === 'function') cleanup();
      else if (typeof ref === 'function') ref(null);
      else if (ref) ref.current = null;
    };
  }, [ref]);
  const inputId = id ?? generated;
  const labelId = `${inputId}-label`;
  const helpId = `${inputId}-help`;
  const errorId = `${inputId}-error`;
  const triggerProps = {
    id: inputId, className: [styles.select, className].filter(Boolean).join(' '), style,
    'aria-label': accessibleLabel, 'aria-labelledby': labelledBy ?? (label ? labelId : undefined),
    'aria-invalid': Boolean(error) || undefined,
    'aria-describedby': [describedBy, description ? helpId : undefined, error ? errorId : undefined].filter(Boolean).join(' ') || undefined,
  } as const;
  return <div className={[styles.selectField, containerClassName].filter(Boolean).join(' ')} data-rich={rich || undefined}>
    {label && <label htmlFor={inputId} id={labelId} className={styles.fieldLabel}>{label}</label>}
    {phone ? <PhoneSelect options={options} value={currentValue} onValueChange={changeValue} open={currentOpen} onOpenChange={changeOpen}
      title={sheetTitle ?? label ?? accessibleLabel} rich={rich} trigger={trigger} bindTrigger={bindTrigger} triggerProps={triggerProps}
      disabled={disabled} required={required} name={name} form={form} placeholder={placeholder} selectedLabel={selectedLabel} resetValue={initialValue.current} onReset={() => changeValue(initialValue.current)} />
      : <SelectPrimitive.Root value={currentValue} onValueChange={changeValue} open={currentOpen} onOpenChange={changeOpen} disabled={disabled} required={required} name={name} form={form}>
      <SelectPrimitive.Trigger {...triggerProps} ref={bindTrigger}>
        <span className={styles.selectValue}><SelectPrimitive.Value placeholder={placeholder}>{selectedLabel}</SelectPrimitive.Value></span>
        <SelectPrimitive.Icon><Icon name="i-down" size={18} /></SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal container={portalTarget}><div className={styles.selectPortal}>
        <SelectPrimitive.Content ref={setMenu} position="popper" align="end" sideOffset={6} collisionPadding={8} className={styles.selectMenu} data-rich={rich || undefined} data-stacked-hints={rich && menuFit.stacked || undefined} style={rich ? {width: menuFit.width} : undefined}
          onKeyDown={event => {
            if (event.key !== 'Tab') return;
            event.preventDefault();
            const focusedValue = document.activeElement?.getAttribute('data-option-value');
            if (focusedValue && options.some(option => option.value === focusedValue && !option.disabled)) changeValue(focusedValue);
            tabDirection.current = event.shiftKey ? -1 : 1;
            changeOpen(false);
          }}
          onCloseAutoFocus={event => {
            if (tabDirection.current === null) return;
            event.preventDefault();
            if (trigger.current) tabFrom(trigger.current, tabDirection.current);
            tabDirection.current = null;
          }}>
          <SelectPrimitive.ScrollUpButton className={styles.scrollButton}><Icon name="i-up" size={16} /></SelectPrimitive.ScrollUpButton>
          <SelectPrimitive.Viewport className={styles.selectViewport}>
            {options.map(option => <Fragment key={option.value}>{option.separatorBefore && <SelectPrimitive.Separator className={styles.optionSeparator} />}<SelectPrimitive.Item value={option.value} textValue={option.label} disabled={option.disabled}
              className={styles.option} data-option-value={option.value} aria-describedby={[rich && option.isDefault ? `${inputId}-${encodeURIComponent(option.value)}-default` : undefined, rich && option.description ? `${inputId}-${encodeURIComponent(option.value)}-description` : undefined].filter(Boolean).join(' ') || undefined}>
              <span className={styles.optionCheck}><SelectPrimitive.ItemIndicator><Icon name="i-check" size={16} /></SelectPrimitive.ItemIndicator></span>
              <span className={styles.optionName}><SelectPrimitive.ItemText>{option.label}</SelectPrimitive.ItemText><DefaultTag show={rich && option.isDefault} id={`${inputId}-${encodeURIComponent(option.value)}-default`} /></span>
              {rich && option.description && <span id={`${inputId}-${encodeURIComponent(option.value)}-description`} className={styles.optionDescription}>{option.description}</span>}
            </SelectPrimitive.Item></Fragment>)}
          </SelectPrimitive.Viewport>
          <SelectPrimitive.ScrollDownButton className={styles.scrollButton}><Icon name="i-down" size={16} /></SelectPrimitive.ScrollDownButton>
        </SelectPrimitive.Content>
      </div></SelectPrimitive.Portal>
    </SelectPrimitive.Root>}
    {description && <p id={helpId} className={styles.help}>{description}</p>}
    {error && <p id={errorId} className={styles.error}>{error}</p>}
  </div>;
});

function DefaultTag({show, id}: {show?: boolean; id?: string}) {
  return show ? <span id={id} className={styles.defaultTag}>{defaultI18n.t('next.controls.default')}</span> : null;
}

type PhoneSelectProps = {
  options: readonly SelectOption[];
  value: string;
  onValueChange: (value: string) => void;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onReset: () => void;
  resetValue: string;
  title?: ReactNode;
  rich?: boolean;
  trigger: RefObject<HTMLButtonElement | null>;
  bindTrigger: (element: HTMLButtonElement | null) => void;
  triggerProps: {id: string; className: string; style?: CSSProperties; 'aria-label'?: string; 'aria-labelledby'?: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean};
  disabled?: boolean;
  required?: boolean;
  name?: string;
  form?: string;
  placeholder?: string;
  selectedLabel?: string;
};

function PhoneSelect({options, value, onValueChange, open, onOpenChange, onReset, resetValue, title, rich, trigger, bindTrigger, triggerProps, disabled, required, name, form, placeholder, selectedLabel}: PhoneSelectProps) {
  const selected = options.findIndex(option => option.value === value && !option.disabled);
  const [active, setActive] = useState(selected >= 0 ? selected : moveOption(options, -1, 1));
  const optionRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const initialFocus = useRef<HTMLElement | null>(null);
  const nativeSelect = useRef<HTMLSelectElement | null>(null);
  const resetCallback = useRef(onReset); resetCallback.current = onReset;
  const search = useRef({text: '', time: 0});
  const listId = `${triggerProps.id}-options`;
  useEffect(() => {
    const owner = nativeSelect.current?.form;
    const reset = () => resetCallback.current();
    owner?.addEventListener('reset', reset);
    return () => owner?.removeEventListener('reset', reset);
  }, [form]);
  useEffect(() => {if (!open) search.current = {text: '', time: 0};}, [open]);
  const begin = (direction = 1) => {
    setActive(selected >= 0 ? selected : moveOption(options, direction > 0 ? -1 : 0, direction));
    search.current = {text: '', time: 0};
    onOpenChange(true);
  };
  const focusOption = (index: number) => {
    if (index < 0) return;
    setActive(index); optionRefs.current[index]?.focus();
  };
  return <>
    <button {...triggerProps} ref={bindTrigger} type="button" role="combobox" aria-haspopup="listbox" aria-expanded={open} aria-controls={listId} aria-required={required || undefined} disabled={disabled}
      data-state={open ? 'open' : 'closed'} onClick={() => open ? onOpenChange(false) : begin()}
      onKeyDown={event => {if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(event.key)) {event.preventDefault(); begin(event.key === 'ArrowUp' ? -1 : 1);}}}>
      <span className={styles.selectValue}>{selectedLabel ?? options.find(option => option.value === value)?.label ?? placeholder}</span><span><Icon name="i-down" size={18} /></span>
    </button>
    <select ref={element => {nativeSelect.current = element; if (element) {for (const option of element.options) option.defaultSelected = option.value === resetValue; element.value = value;}}}
      className={styles.formSelect} tabIndex={-1} aria-hidden="true" name={name} form={form} required={required} disabled={disabled} value={value}
      onChange={event => onValueChange(event.currentTarget.value)} onInvalid={event => {event.preventDefault(); trigger.current?.focus();}}>
      <option value="">{placeholder}</option>{options.map(option => <option key={option.value} value={option.value} disabled={option.disabled}>{option.label}</option>)}
    </select>
    <Dialog open={open} onOpenChange={onOpenChange} title={title} accessibilityLabel={triggerProps['aria-label']} initialFocusRef={initialFocus} returnFocusRef={trigger} bodyClassName={styles.phonePickerBody}>
      <div id={listId} className={styles.phoneListbox} role="listbox" aria-label={triggerProps['aria-label']} aria-labelledby={triggerProps['aria-labelledby']}
        tabIndex={active < 0 ? 0 : -1} ref={element => {if (active < 0) initialFocus.current = element;}}
        onKeyDown={event => {
          if (event.altKey || event.ctrlKey || event.metaKey) return;
          const direction = event.key === 'ArrowDown' ? 1 : event.key === 'ArrowUp' ? -1 : 0;
          if (direction || event.key === 'Home' || event.key === 'End') {
            event.preventDefault();
            focusOption(moveOption(options, event.key === 'Home' ? -1 : event.key === 'End' ? 0 : active, event.key === 'End' ? -1 : direction || 1));
          } else if (event.key.length === 1 && event.key !== ' ') {
            const time = performance.now();
            const text = time - search.current.time > 1000 ? event.key : search.current.text + event.key;
            search.current = {text, time};
            event.preventDefault(); focusOption(matchingOption(options, text, active));
          }
        }}>
        {options.map((option, index) => <Fragment key={option.value}>{option.separatorBefore && <div className={styles.optionSeparator} role="separator" />}<button
          ref={element => {optionRefs.current[index] = element; if (index === active) initialFocus.current = element;}}
          type="button" role="option" aria-selected={option.value === value} aria-label={option.label} disabled={option.disabled} aria-disabled={option.disabled || undefined}
          aria-describedby={[rich && option.isDefault ? `${listId}-${index}-default` : undefined, rich && option.description ? `${listId}-${index}-description` : undefined].filter(Boolean).join(' ') || undefined} tabIndex={index === active ? 0 : -1}
          className={[styles.option, styles.phoneOption].join(' ')} data-state={option.value === value ? 'checked' : 'unchecked'} data-disabled={option.disabled || undefined}
          data-option-value={option.value} onFocus={() => setActive(index)} onClick={() => {onValueChange(option.value); onOpenChange(false);}}>
          <span className={styles.optionCheck}>{option.value === value && <Icon name="i-check" size={16} />}</span>
          <span className={styles.optionName}><span>{option.label}</span><DefaultTag show={rich && option.isDefault} id={`${listId}-${index}-default`} /></span>
          {rich && option.description && <span id={`${listId}-${index}-description`} className={styles.optionDescription}>{option.description}</span>}
        </button></Fragment>)}
      </div>
    </Dialog>
  </>;
}

export const Select = forwardRef<HTMLButtonElement, SelectProps>(function Select(props, ref) {return <ChoiceSelect {...props} ref={ref} />;});
export const RichSelect = forwardRef<HTMLButtonElement, SelectProps>(function RichSelect(props, ref) {return <ChoiceSelect {...props} ref={ref} rich />;});
