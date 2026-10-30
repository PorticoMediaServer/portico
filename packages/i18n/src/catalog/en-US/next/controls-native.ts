/** Native controls copy; existing product actions reuse the shared catalogue. */
export const controlsNativeMessages = {
  'next.controls.native.choose': 'Choose',
  'next.controls.native.galleryTitle': 'Controls',
  'next.controls.native.buttonsTitle': 'Buttons',
  'next.controls.native.resumeTitle': 'Resume',
  'next.controls.native.chipsTitle': 'Filters',
  'next.controls.native.tabsTitle': 'Tabs',
  'next.controls.native.segmentedTitle': 'Choices',
  'next.controls.native.switchesTitle': 'Switches',
  'next.controls.native.fieldsTitle': 'Search',
  'next.controls.native.selectTitle': 'Playback',
  'next.controls.native.richSelectTitle': 'Quality & data',
  // TV key hints drawn on design/ui-v1/tv-more.html frame 22 (ds-native-live-r2).
  'next.controls.native.up': 'Up',
  'next.controls.native.down': 'Down',
  'next.controls.native.left': 'Left',
  'next.controls.native.right': 'Right',
} as const satisfies Record<`next.controls.native.${string}`, string>;
