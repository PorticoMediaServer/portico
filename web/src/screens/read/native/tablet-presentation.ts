export type ReaderEnvironment = 'web' | 'native';
export type ReaderPanelKind = 'display' | 'contents' | 'search';

/** Native tablet-read frames 4/6: touch chrome at every tablet width,
 * a display popover when expanded, and reflowing docked content panels.
 * The window's shape changes presentation only; the engine retains the reading location. */
export function readerBookPresentation(width: number, environment: ReaderEnvironment = 'web', height = Infinity) {
  const compact = width < 600, expanded = width >= 1024;
  // phone-read 10/12: a turned phone keeps the phone's controls. This is the engine's own
  // compact-landscape window (reader-core/src/layout.ts `compactLandscape`), so the chrome
  // and the page agree. An unknown height keeps the width-only rule.
  const shortLandscape = width < 1024 && height < 600 && width > height, phone = compact || shortLandscape;
  const nativeTablet = environment === 'native' && !phone;
  const presentation = phone ? 'phone' : nativeTablet || !expanded ? 'tablet' : 'desktop';
  const displayPopover = nativeTablet && expanded;
  return {
    presentation,
    displayPopover,
    showPageArrows: expanded && !nativeTablet,
    fullscreen: presentation === 'desktop',
    panelReflows: (panel: ReaderPanelKind | undefined) => !!panel && !phone && !(displayPopover && panel === 'display'),
  } as const;
}
