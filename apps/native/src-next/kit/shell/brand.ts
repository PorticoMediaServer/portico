/** Shared native header wordmarks: handheld/tablet +25%, expanded TV +40%.
 * Keep the chrome slots and their center lines unchanged. Collapsed symbols and boot marks
 * retain their own geometry. Android and Apple consume these same values. */
export const headerWordmark = {
  phone: {width: 86.25, height: 20},
  desktop: {width: 107.5, height: 25},
  tabletRail: {width: 107.5, height: 21.25},
  tvRail: {width: 182, height: 42},
} as const;
