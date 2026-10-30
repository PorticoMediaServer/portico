/** Existing measured full episode-player frame: intro prompt, visible controls,
 * 9:52 / 53:06 and the authored 59.64% seek hover. Reference only: the product
 * owner supplies the genuine current media host/DEV entry and command ports.
 * No replacement player, media state or illustrative artwork is created here.
 */
export const watchEpisodeIntroReference = {
  selector: '#f1.screen.pv',
  windowViewport: {width: 1520, height: 1014, deviceScaleFactor: 1},
  viewport: {width: 1440, height: 810, deviceScaleFactor: 1},
  anchors: {
    screen: '$root', header: '.ptop', title: '.ptop h2', episode: '.ptop p',
    minimize: '.ptop button[aria-label="Minimize player"]',
    cast: '.ptop button[aria-label="Play on"]', more: '.ptop button[aria-label="More"]',
    close: '.ptop button[aria-label="Close player"]', intro: '.skipw', skip: '.skipb',
    seek: '.seek.hot', tooltip: '.seek .tp', times: '.times', controls: '.ctrl',
  },
};
