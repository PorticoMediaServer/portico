import {screenModule} from './screen-harness.mjs';
import {stubsFrom} from './stubs-from.mjs';

/** The player's chrome page loaded with stubs keyed as they were for screens/watch/WatchChrome, drawn through its kit view
 * in the same call, so a test reads the chrome's tree as it did before the move. */
export async function loadWatchChrome(stubs) {
  // The kit view types its text through shellType and textLines; the old screen's stubs drew nativeType as empty styles.
  const theme = stubs['../../kit/theme'];
  stubs = {'../../kit/shell/visual': {shellType: () => ({})}, ...stubs,
    ...(theme && !theme.textLines ? {'../../kit/theme': {...theme, textLines: lines => ({numberOfLines: lines})}} : {})};
  const {WatchChromePage} = await screenModule(new URL('../../src-next/pages/shared/player/WatchChromePage.tsx', import.meta.url), stubsFrom('screens/watch', 'pages/shared/player', stubs));
  return {NativeWatchChrome: props => {const tree = WatchChromePage(props); return typeof tree?.type === 'function' ? tree.type(tree.props) : tree;}};
}
