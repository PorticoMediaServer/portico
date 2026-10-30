/** Public default hero control binding. Reference input is supplied externally. */
export const controlsPrimary = {
  id: 'kit-controls-primary', state: 'non-phone-hero-primary-play-unfocused', component: true,
  viewport: {width: 240, height: 80, deviceScaleFactor: 1},
  referenceWindowViewport: {width: 1520, height: 1014, deviceScaleFactor: 1},
  referenceSelector: '#f1 .btn.play', actualSelector: '[data-gallery-capture] button',
  entry: '/dev/kit?family=controls&case=primary&capture=true', readySelector: '[data-gallery-ready="true"]',
  reference: {'component-root': '$root'},
  actual: {'component-root': '$root', label: ':scope > span[class*="_buttonLabel_"]'},
  textHandledBySlots: ['component-root'], actualContentOnly: ['label'],
  contentSlots: [{reference: 'component-root', actual: 'label'}], preferredKeys: ['next.controls.play'],
};
