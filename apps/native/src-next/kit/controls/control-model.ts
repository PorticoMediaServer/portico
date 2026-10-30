import {buttonSpec, chipSpec, focusSpec, targets, typeScale} from '@portico/design';

export type ControlPlatform = 'phone' | 'tablet' | 'web' | 'tv';
export type ButtonVariant = 'primary' | 'glass' | 'text' | 'icon' | 'danger';
export type ButtonSize = 'hero' | 'compact' | 'small' | 'xs' | 'episode' | 'dock';
export type FocusKind = 'button' | 'row' | 'chip' | 'card';
export type ChipVariant = 'default' | 'quick' | 'facet' | 'applied' | 'pill';

export function progressFraction(value: number): number {
  return Number.isFinite(value) ? Math.max(0, Math.min(1, value)) : 0;
}

/** Page-local measurements are kept in the kit, beside their source map. */
export function buttonGeometry(size: ButtonSize, platform: ControlPlatform, variant: ButtonVariant = 'primary') {
  if (platform === 'web' && variant === 'icon') return {height: 40, radius: 20, paddingX: 0, gap: 0, icon: 22, fontSize: 15};
  if (platform === 'web' && variant === 'text') return {height: 32, radius: 8, paddingX: 10, gap: 6, icon: 16, fontSize: 13.5};
  if (size === 'hero') return {
    height: buttonSpec.hero.height[platform], radius: buttonSpec.hero.radius[platform],
    paddingX: buttonSpec.hero.paddingX[platform], gap: buttonSpec.hero.gap[platform],
    icon: buttonSpec.hero.icon[platform], fontSize: typeScale[platform === 'tv' ? 'tenFoot' : platform === 'web' ? 'desktop' : 'handheld'].button.size,
  };
  if (platform === 'tv') return {height: 64, radius: 14, paddingX: 26, gap: 12, icon: 26, fontSize: 22};
  // kit.css .btn and its page-local utility sizes, also used by web controls.
  if (platform === 'web') {
    const desktop = {
      compact: {...buttonSpec.compact, icon: buttonSpec.hero.icon.web, fontSize: typeScale.desktop.compactButton.size},
      small: {height: 42, radius: 10, paddingX: 18, gap: 8, icon: 18, fontSize: 15},
      xs: {height: 34, radius: 9, paddingX: 13, gap: 6, icon: 16, fontSize: 14},
      episode: {height: 48, radius: 11, paddingX: 22, gap: buttonSpec.hero.gap.web, icon: 20, fontSize: 16},
      dock: {height: 38, radius: 10, paddingX: 14, gap: 8, icon: 18, fontSize: 14},
    };
    return desktop[size];
  }
  const sizes = {
    compact: {height: 46, radius: 12, paddingX: 26, gap: 10, icon: 20, fontSize: 15},
    small: {height: 42, radius: 10, paddingX: 18, gap: 8, icon: 18, fontSize: 15},
    xs: {height: 34, radius: 9, paddingX: 13, gap: 6, icon: 16, fontSize: 14},
    episode: {height: 48, radius: 11, paddingX: 22, gap: 8, icon: 20, fontSize: 16},
    dock: {height: 32, radius: 8, paddingX: 10, gap: 6, icon: 18, fontSize: 13.5},
  };
  return sizes[size];
}

type ChipGeometry = {height: number; radius: number; paddingX: number; paddingRight?: number; gap: number; fontSize: number};
export function chipGeometry(variant: ChipVariant, platform: ControlPlatform): ChipGeometry {
  const isTV = platform === 'tv';
  const standard = {height: chipSpec.height[platform], radius: chipSpec.radius[platform], paddingX: chipSpec.paddingX[platform], gap: chipSpec.gap[platform], fontSize: isTV ? 22 : 13.5};
  if (platform === 'web') {
    if (variant === 'facet') return {...standard, height: chipSpec.facet.height.web, radius: chipSpec.facet.radius.web, paddingX: chipSpec.facet.paddingX.web, gap: 7, fontSize: 14};
    if (variant === 'applied') return {...standard, height: 34, radius: 17, paddingX: 14, paddingRight: 7, gap: 7, fontSize: 14};
    if (variant === 'pill') return {...standard, height: 34, radius: 17, paddingX: 12, gap: 6};
    if (variant === 'quick') return {...standard, ...chipSpec.quickFilter, fontSize: 14.5};
    return {...standard, fontSize: typeScale.desktop.chip.size};
  }
  if (variant === 'facet') return {...standard, height: chipSpec.facet.height[platform], radius: chipSpec.facet.radius[platform], paddingX: chipSpec.facet.paddingX[platform], gap: isTV ? 10 : 6};
  if (variant === 'applied') return {...standard, height: isTV ? 52 : 30, radius: isTV ? 14 : 9, paddingX: isTV ? 20 : 11, fontSize: isTV ? 20 : 13};
  if (variant === 'pill') return {...standard, height: isTV ? 64 : 34, radius: isTV ? 14 : 17, paddingX: isTV ? 26 : 12};
  if (variant === 'quick') return {...standard, radius: isTV ? 14 : 17};
  return standard;
}

export function focusScale(kind: FocusKind, isTV: boolean): number {
  return focusSpec.scale[kind][isTV ? 'tv' : 'phone'];
}

export function minimumHitSlop(height: number, isTV: boolean): number {
  return Math.max(0, ((isTV ? targets.tenFoot : targets.touch) - height) / 2);
}

export function colorAlpha(color: string, opacity: number): string {
  return `${color.slice(0, 7)}${Math.round(progressFraction(opacity) * 255).toString(16).padStart(2, '0')}`;
}

export type ChoiceValue = {value: string; disabled?: boolean};
/** Used only for the supplied bounded choice list, never a media collection. */
export function enabledChoice(items: readonly ChoiceValue[], current?: string): string | undefined {
  return items.find(item => item.value === current && !item.disabled)?.value ?? items.find(item => !item.disabled)?.value;
}

export function switchDragFraction(start: number, translation: number, travel: number): number {
  'worklet';
  return Math.max(0, Math.min(1, start + translation / Math.max(1, travel)));
}

/** Release chooses the nearest resting state after the shared 200ms gesture coast. */
export function switchReleaseValue(position: number, velocity: number, travel: number): boolean {
  'worklet';
  return position + velocity * .2 / Math.max(1, travel) >= .5;
}
