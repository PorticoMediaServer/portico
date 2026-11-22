import {screenModule} from './screen-harness.mjs';
import {stubsFrom} from './stubs-from.mjs';

/** The player's chrome page loaded with stubs keyed as they were for screens/watch/WatchChrome, drawn through its kit view
 * in the same call, so a test reads the chrome's tree as it did before the move. */
export async function loadWatchChrome(stubs) {
  // The kit view types its text through shellType and textLines.
  const theme = stubs['../../kit/theme'];
  // shellType(size, weight, lineHeight, tracking, scale) is nativeType({size, weight, lineHeight, tracking}, scale): drawn through the fixture's own.
  const shellType = (size, weight = 600, lineHeight = 'normal', tracking = 0, scale = 1) => theme?.nativeType ? theme.nativeType({size, weight, lineHeight, tracking}, scale) : {};
  stubs = {'../../kit/shell/visual': {shellType}, ...stubs,
    ...(theme && !theme.textLines ? {'../../kit/theme': {...theme, textLines: lines => ({numberOfLines: lines})}} : {})};
  const {WatchChromePage} = await screenModule(new URL('../../src-next/pages/shared/player/WatchChromePage.tsx', import.meta.url), stubsFrom('screens/watch', 'pages/shared/player', stubs));
  return {NativeWatchChrome: props => {const tree = WatchChromePage(props); return typeof tree?.type === 'function' ? tree.type(tree.props) : tree;}};
}
