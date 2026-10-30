/** Reference-only film/not-started state. No application renderer or artwork
 * sources; an actual comparison requires the screen owner's genuine DEV entry. */
export const detailMovieReference = {
  selector: '.frame[data-f="1"] > .screen',
  windowViewport: {width: 1520, height: 1800, deviceScaleFactor: 1},
  viewport: {width: 1440, height: 1660, deviceScaleFactor: 1},
  motion: 'settled', motionTimeoutMs: 15000,
  anchors: {screen: '$root', navigation: ':scope > .nav', hero: ':scope > .t-hero',
    collection: '.copy > .coll', title: '.copy > h1', metadata: '.copy > .meta',
    tagline: '.copy > .tagline', description: '.copy > .blurb', credits: '.copy > .starring',
    actions: '.copy > .actions', play: '.actions > button.play', 'my-list': '.actions > button:nth-child(2)',
    watched: '.actions > button[aria-label="Mark as watched"]', more: '.actions > button[aria-label="More"]',
    tabs: ':scope > .tabs', 'selected-tab': ':scope > .tabs > [aria-selected="true"]',
    recommendations: ':scope > .panel'},
};
