/** Whole authored guide canvas, verified against the existing natural-size
 * reference extraction. The genuine public entry is live-guide.html; it mounts
 * GuideScreen. This is a reference recipe, not a replacement app renderer.
 *
 * The live badge and recording dot have real infinite opacity pulses. Keep
 * their measured facts and the extractor's determinism failure; this recipe
 * never pauses animation, chooses a phase or claims a stable screen PASS.
 */
export const liveGuideReference = {
  selector: '#live-guide',
  windowViewport: {width: 1520, height: 2200, deviceScaleFactor: 1},
  viewport: {width: 1440, height: 1909, deviceScaleFactor: 1},
  anchors: {
    screen: '$root', navigation: ':scope > nav', header: '.lv-top',
    heading: '.lv-top h1', source: '.lv-top > button',
    tabs: '.lv-tabs', 'selected-tab': '.lv-tabs > button[aria-selected="true"]',
  },
};
