import {useRef, useState} from 'react';
import {Chip, type SelectOption} from '../../kit/controls';
import type {IconName} from '../../kit/Icon';
import {GuidePicker} from '../../kit/live';
import {Menu, MenuItem} from '../../kit/overlays';
import styles from './pick-chip.module.css';

/** A choice drawn as a kit chip with its menu (desktop-live: "Today ⌄", "Sort: By number ⌄"; the source picker). At phone width
 * the day and the sort are the guide's quiet pickers (`quiet`: phone-guide g1) and the source an outlined pill (`pill`). */
export function PickChip({label, prefix, icon, value, options, onChange, quiet = false, pill = false}: Readonly<{label: string; prefix?: string; icon?: IconName; value: string;
  options: readonly SelectOption[]; onChange: (value: string) => void; quiet?: boolean; pill?: boolean}>) {
  const [open, setOpen] = useState(false), trigger = useRef<HTMLButtonElement>(null);
  const current = options.find(option => option.value === value);
  const detail = typeof current?.description === 'string' ? current.description : undefined;
  const items = options.map(option => <MenuItem key={option.value} selected={option.value === value} disabled={option.disabled} description={option.description} onSelect={() => {setOpen(false); if (option.value !== value) onChange(option.value);}}>{option.label}</MenuItem>);
  if (quiet) return <>
    <GuidePicker icon={icon} label={current?.label ?? label} value={detail} onPress={() => setOpen(true)}/>
    <Menu open={open} onOpenChange={setOpen} title={label}>{items}</Menu>
  </>;
  return <Menu presentation="anchored" open={open} onOpenChange={setOpen} title={label} placement="bottom-start" returnFocusRef={trigger}
    trigger={<Chip ref={trigger} variant={pill ? 'pill' : undefined} icon={icon} prefix={prefix} trailingIcon="i-chdown" trailingSize={16} aria-label={current ? `${label}: ${current.label}` : label} aria-expanded={open}>{current?.label ?? label}{detail && <span className={styles.detail}>{detail}</span>}</Chip>}>
    {items}
  </Menu>;
}
