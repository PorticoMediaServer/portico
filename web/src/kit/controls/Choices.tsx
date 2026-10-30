import {useId, type ComponentPropsWithoutRef, type ReactNode} from 'react';
import {Tabs as TabsPrimitive, RadioGroup} from 'radix-ui';
import {Icon, type IconName} from '../Icon';
import styles from './controls.module.css';

export type ChoiceItem = Readonly<{value: string; label: ReactNode; disabled?: boolean}>;
export type TabsProps = Omit<ComponentPropsWithoutRef<typeof TabsPrimitive.Root>, 'children' | 'defaultValue' | 'onChange'> & {
  items: readonly (ChoiceItem & {panel?: ReactNode; /** Preserve a draft in a controlled, inactive tab. */ keepMounted?: boolean})[];
  value?: string;
  defaultValue?: string;
  onValueChange?: (value: string) => void;
  phone?: boolean;
  listClassName?: string;
  /** For title pages whose sticky strip and existing panel have separate parents. */
  externalPanelId?: string;
};

export function Tabs({items, value, defaultValue, onValueChange, phone, className, listClassName, externalPanelId, 'aria-label': label, ...props}: TabsProps) {
  const initial = defaultValue ?? items.find(item => !item.disabled)?.value;
  return <TabsPrimitive.Root {...props} value={value} defaultValue={initial} onValueChange={onValueChange} className={className}>
    <div className={styles.tabStrip}><TabsPrimitive.List className={[styles.tabs, listClassName].filter(Boolean).join(' ')} data-phone={phone || undefined} aria-label={label}>
      {items.map(item => <TabsPrimitive.Trigger key={item.value} value={item.value} {...(externalPanelId ? {id: `${externalPanelId}-tab-${item.value}`, 'aria-controls': externalPanelId} : {})} disabled={item.disabled} className={styles.tab} onFocus={event => {
        const list = event.currentTarget.parentElement;
        if (!list) return;
        const tab = event.currentTarget.getBoundingClientRect(), viewport = list.getBoundingClientRect();
        const left = tab.left < viewport.left ? tab.left - viewport.left : tab.right > viewport.right ? tab.right - viewport.right : 0;
        if (left) list.scrollBy({left, behavior: 'instant'});
      }}>{item.label}</TabsPrimitive.Trigger>)}
    </TabsPrimitive.List></div>
    {items.map(item => item.panel !== undefined && <TabsPrimitive.Content key={item.value} value={item.value} className={styles.tabPanel}
      {...(item.keepMounted && value !== undefined ? {forceMount: true as const, hidden: item.value !== value} : {})}>{item.panel}</TabsPrimitive.Content>)}
  </TabsPrimitive.Root>;
}

export type SegmentedProps = Omit<ComponentPropsWithoutRef<typeof RadioGroup.Root>, 'children' | 'defaultValue' | 'onChange'> & {
  items: readonly (ChoiceItem & {icon?: IconName; accessibleLabel?: string})[];
  value?: string;
  defaultValue?: string;
  onValueChange?: (value: string) => void;
  variant?: 'glass' | 'browse' | 'phone' | 'view';
  name?: string;
  disabled?: boolean;
};

export function Segmented({items, value, defaultValue, onValueChange, variant = 'glass', name, disabled, className, onKeyDownCapture, ...props}: SegmentedProps) {
  const id = useId();
  return <RadioGroup.Root {...props} id={props.id ?? id} orientation="horizontal" value={value} defaultValue={defaultValue ?? items.find(item => !item.disabled)?.value}
    onValueChange={onValueChange} disabled={disabled} name={name} className={[styles.segmented, className].filter(Boolean).join(' ')} data-variant={variant}
    onKeyDownCapture={event => {
      onKeyDownCapture?.(event);
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || disabled) return;
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
      const choices = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="radio"]')].filter(choice => !choice.disabled);
      const index = choices.indexOf(event.target as HTMLButtonElement);
      if (index < 0) return;
      const rtl = getComputedStyle(event.currentTarget).direction === 'rtl';
      const direction = (event.key === 'ArrowRight' ? 1 : -1) * (rtl ? -1 : 1);
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? choices.length - 1 : props.loop === false ? Math.min(choices.length - 1, Math.max(0, index + direction)) : (index + direction + choices.length) % choices.length;
      // Commit before keyup: Radix's deferred roving focus can miss a rapid key press.
      event.preventDefault(); choices[next]?.focus(); choices[next]?.click();
    }}>
    {items.map(item => <RadioGroup.Item key={item.value} value={item.value} disabled={item.disabled} aria-label={item.accessibleLabel} className={styles.segment}>
      {item.icon && <Icon name={item.icon} size={18} />}{item.label}
    </RadioGroup.Item>)}
  </RadioGroup.Root>;
}

/** Unstyled parts share the same Radix keyboard and panel lifecycle. */
export {TabsPrimitive as TabsParts};
