import {useCallback, useId, useLayoutEffect, useRef, type CSSProperties, type KeyboardEvent, type RefObject} from 'react';
import {RadioGroup} from 'radix-ui';
import {Icon, type IconName} from '../Icon';
import {Segmented, Switch} from '../controls';
import {Popover} from '../overlays/Popover';
import {BottomSheet} from '../overlays/BottomSheet';
import type {OverlayChange} from '../overlays/types';
import {focusElement, focusable, useOverlayKeys, usePresence, useSurfaceMotion} from '../overlays/shared';
import {ReaderSheetClose} from './ReaderSheetClose';
import styles from './display.module.css';

type DisplayOption = Readonly<{value: string; label: string; disabled?: boolean}>;
type DisplayGroup<Option extends DisplayOption> = Readonly<{
  label: string; value: string; options: readonly Option[]; onChange: (value: string) => void;
}>;
export type ReaderThemeOption = DisplayOption & Readonly<{tone: 'dark' | 'black' | 'sepia' | 'light'}>;
export type ReaderFontOption = DisplayOption & Readonly<{
  face: 'publisher' | 'literata' | 'manrope'; compactLabel: string; description?: string;
}>;
export type ReaderDisplayRow = DisplayGroup<DisplayOption & Readonly<{icon?: IconName}>> & Readonly<{
  id: string; hideOnPhone?: boolean;
}>;
export type ReaderDisplaySize = Readonly<{
  label: string; sliderLabel: string; smallerLabel: string; largerLabel: string;
  value: number; min: number; max: number; step: number; valueText: string; summary?: string;
  /** Tick values and bounds come from the canonical Reading facade. */
  ticks?: readonly number[]; disabled?: boolean; onChange: (value: number) => void;
}>;
export type ReaderTwoPages = Readonly<{
  label: string; description?: string; checked: boolean; disabled?: boolean;
  onChange: (checked: boolean) => void;
}>;
export type ReaderDisplayControlsProps = Readonly<{
  themes: DisplayGroup<ReaderThemeOption>; fonts: DisplayGroup<ReaderFontOption>;
  size: ReaderDisplaySize; rows: readonly ReaderDisplayRow[];
  copy: Readonly<{sample: string; sizeSample: string; sessionNote: string; defaultsNote: string; openSettings?: string}>;
  onOpenSettings?: () => void;
  /** Tablet frames use font tiles and touch targets in both orientations. */
  layout?: 'desktop' | 'tablet';
  twoPages?: ReaderTwoPages;
}>;

/** Commit before keyup, including when a fast next key would overtake roving focus. */
function moveChoice(event: KeyboardEvent<HTMLDivElement>) {
  if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || !['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
  const choices = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="radio"]')].filter(choice => !choice.disabled);
  const index = choices.indexOf(event.target as HTMLButtonElement);
  if (index < 0) return;
  const rtl = getComputedStyle(event.currentTarget).direction === 'rtl';
  const direction = event.key === 'ArrowDown' ? 1 : event.key === 'ArrowUp' ? -1 : (event.key === 'ArrowRight' ? 1 : -1) * (rtl ? -1 : 1);
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? choices.length - 1 : (index + direction + choices.length) % choices.length;
  event.preventDefault(); choices[next]?.focus(); choices[next]?.click();
}

/** Frame-backed controls only: the core supplies values and applies book-local overrides. */
export function ReaderDisplayControls({themes, fonts, size, rows, copy, onOpenSettings, layout = 'desktop', twoPages}: ReaderDisplayControlsProps) {
  const id = useId();
  const fraction = size.max > size.min ? Math.max(0, Math.min(1, (size.value - size.min) / (size.max - size.min))) : 0;
  const markerPercent = Math.trunc(fraction * 100);
  const changeSize = (value: number) => {
    if (!size.disabled && Number.isFinite(value)) size.onChange(Math.max(size.min, Math.min(size.max, value)));
  };
  return <div className={styles.controls} data-layout={layout}>
    <section className={styles.group} aria-labelledby={`${id}-page`}><h3 id={`${id}-page`}>{themes.label}</h3>
      <RadioGroup.Root className={styles.themes} value={themes.value} onValueChange={themes.onChange} onKeyDownCapture={moveChoice} aria-labelledby={`${id}-page`} orientation="horizontal">
        {themes.options.map(option => <RadioGroup.Item key={option.value} className={styles.theme} value={option.value} disabled={option.disabled} data-tone={option.tone}>
          <span aria-hidden="true">{copy.sample}</span>{option.label}
        </RadioGroup.Item>)}
      </RadioGroup.Root>
    </section>
    <section className={styles.group} aria-labelledby={`${id}-font`}><h3 id={`${id}-font`}>{fonts.label}</h3>
      <RadioGroup.Root className={styles.fonts} value={fonts.value} onValueChange={fonts.onChange} onKeyDownCapture={moveChoice} aria-labelledby={`${id}-font`} orientation={layout === 'tablet' ? 'horizontal' : 'vertical'}>
        {fonts.options.map(option => <RadioGroup.Item key={option.value} className={styles.font} value={option.value} disabled={option.disabled} data-face={option.face}>
          <span className={styles.fontDesktop}>{option.label}{option.description && <small>{option.description}</small>}</span>
          <span className={styles.fontCompact}><span aria-hidden="true">{copy.sample}</span><small>{option.compactLabel}</small></span>
          <Icon className={styles.check} name="i-check" size={18} />
        </RadioGroup.Item>)}
      </RadioGroup.Root>
    </section>
    <section className={styles.group} aria-labelledby={`${id}-size`}><h3 id={`${id}-size`}>{size.label}{size.summary && <span>{size.summary}</span>}</h3>
      <div className={styles.sizer}>
        <button type="button" className={styles.smaller} aria-label={size.smallerLabel} disabled={size.disabled || size.value <= size.min} onClick={() => changeSize(size.value - size.step)}>{copy.sizeSample}</button>
        <div className={styles.range}><span className={styles.track} aria-hidden="true" style={{'--reader-marker-whole': `${markerPercent}%`, '--reader-marker-decimal': `${Math.round(fraction * 1000) / 10}%`} as CSSProperties}><i />
          {size.ticks?.filter(value => Number.isFinite(value) && value >= size.min && value <= size.max).map(value => <u key={value} style={{left: `${size.max > size.min ? (value - size.min) / (size.max - size.min) * 100 : 0}%`}} />)}
          <b /></span>
          <input type="range" min={size.min} max={size.max} step={size.step} value={size.value} disabled={size.disabled || size.max <= size.min} aria-label={size.sliderLabel} aria-valuetext={size.valueText} onChange={event => changeSize(event.currentTarget.valueAsNumber)} />
        </div>
        <button type="button" className={styles.larger} aria-label={size.largerLabel} disabled={size.disabled || size.value >= size.max} onClick={() => changeSize(size.value + size.step)}>{copy.sizeSample}</button>
        <span className={styles.value}>{size.valueText}</span>
      </div>
    </section>
    <div className={`${styles.group} ${styles.rows}`}>{rows.map(row => <div key={row.id} className={styles.row} data-phone-hidden={row.hideOnPhone || undefined}>
      <span>{row.label}</span><Segmented className={styles.segments} aria-label={row.label} value={row.value} onValueChange={row.onChange}
        items={row.options} />
    </div>)}</div>
    {twoPages && <label className={styles.twoPages}><span>{twoPages.label}{twoPages.description && <small>{twoPages.description}</small>}</span>
      <Switch className={styles.twoPagesSwitch} checked={twoPages.checked} disabled={twoPages.disabled} onCheckedChange={twoPages.onChange} aria-label={twoPages.label} />
    </label>}
    <p className={styles.note}><Icon name="i-info" size={18} /><span><b>{copy.sessionNote}</b>{' '}{copy.defaultsNote}</span>
      {onOpenSettings && copy.openSettings && <button type="button" onClick={onOpenSettings}>{copy.openSettings}<Icon name="i-right" size={16} /></button>}
    </p>
  </div>;
}

export type ReaderDisplayProps = ReaderDisplayControlsProps & Readonly<{
  label: string; open: boolean; onOpenChange: OverlayChange;
  anchorRef: RefObject<HTMLElement | null>;
}>;

/** Desktop Aa is authored against the viewport (desktop-read 7): 404 wide,
 * 20 from the right and 80 from the top, independent of its opener. */
const desktopDisplayGeometry = {getBoundingClientRect: () =>
  new DOMRect(document.documentElement.clientWidth - 20, 80, 0, 0)};

/** Desktop popover and compact sheet share the same controlled values and callbacks. */
export function ReaderDisplayPopover({label, open, onOpenChange, anchorRef, ...controls}: ReaderDisplayProps) {
  const desktop = controls.layout !== 'tablet';
  return <Popover open={open} onOpenChange={onOpenChange} title={label} accessibilityLabel={label} anchorRef={anchorRef}
    geometry={desktop ? desktopDisplayGeometry : undefined} width={desktop ? 404 : 448} sideOffset={desktop ? 0 : 8}
    className={`${styles.popover} ${desktop ? styles.desktopPopover : styles.tabletPopover}`}>
    <ReaderDisplayControls {...controls} />
  </Popover>;
}

/** Sheet assembly is explicit so an unmounted desktop overlay never owns phone focus. */
export function ReaderDisplaySheet({label, open, onOpenChange, anchorRef, ...controls}: ReaderDisplayProps) {
  return <BottomSheet open={open} onOpenChange={onOpenChange} title={label} accessibilityLabel={label} returnFocusRef={anchorRef}
    showCloseButton={false} fitContent className={styles.sheet} headerClassName={styles.sheetHeader} bodyClassName={styles.sheetBody}>
    <ReaderSheetClose onClose={() => onOpenChange(false, 'close-button')}/>
    <ReaderDisplayControls {...controls} />
  </BottomSheet>;
}

/** tablet-read 6b: an in-reader, non-modal panel with no undrawn heading or close button. */
export function ReaderDisplaySidePanel({label, open, onOpenChange, anchorRef, ...controls}: ReaderDisplayProps) {
  const panel = useRef<HTMLElement | null>(null), outside = useRef(false);
  const motionRef = useSurfaceMotion(open, 'panel'), present = usePresence(open, 160);
  const isTop = useOverlayKeys(open, onOpenChange, true);
  const current = useRef({onOpenChange, isTop}); current.current = {onOpenChange, isTop};
  const ref = useCallback((element: HTMLElement | null) => { panel.current = element; motionRef(element); }, [motionRef]);
  // The trigger can attach after this panel's layout effect in the same commit.
  const restoreOpener = useCallback((fallback: HTMLElement | null) => focusElement(anchorRef.current ?? fallback), [anchorRef]);
  useLayoutEffect(() => {
    if (!open) return;
    const surface = panel.current, opener = anchorRef.current, owner = surface?.ownerDocument;
    outside.current = false;
    if (!surface || !owner) return;
    focusElement(surface.querySelector<HTMLElement>('[role="radio"][data-state="checked"]') ?? focusable(surface)[0] ?? surface);
    const dismiss = (event: MouseEvent) => {
      if (!current.current.isTop() || !(event.target instanceof Node) || surface.contains(event.target) || (anchorRef.current ?? opener)?.contains(event.target)) return;
      // Consume a page click so dismissing Display cannot also turn the page.
      if (event.target instanceof Element && event.target.closest('[data-reader-surface]')) event.preventDefault();
      outside.current = true; current.current.onOpenChange(false, 'outside');
    };
    owner.addEventListener('click', dismiss, true);
    return () => {
      owner.removeEventListener('click', dismiss, true);
      if (!outside.current && (owner.activeElement === owner.body || surface.contains(owner.activeElement))) restoreOpener(opener);
    };
  }, [open, anchorRef, restoreOpener]);
  if (!present) return null;
  return <aside ref={ref} className={styles.sidePanel} role="region" aria-label={label} tabIndex={-1} data-reader-panel
    data-state={open ? 'open' : 'closed'} inert={!open} aria-hidden={!open}>
    <ReaderDisplayControls {...controls} layout="tablet" />
  </aside>;
}
