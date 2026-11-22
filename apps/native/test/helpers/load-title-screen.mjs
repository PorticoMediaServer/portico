import {screenModule} from './screen-harness.mjs';
import {stubsFrom} from './stubs-from.mjs';

/** The kit view types its text through shellType and textLines. An old screen stub that drew nativeType keeps drawing
 * through it: shellType(size, weight, lineHeight, tracking, scale) is nativeType({size, weight, lineHeight, tracking}, scale). */
function withViewType(stubs) {
  const theme = stubs['../../kit/theme'];
  const shellType = (size, weight = 600, lineHeight = 'normal', tracking = 0, scale = 1) => theme?.nativeType ? theme.nativeType({size, weight, lineHeight, tracking}, scale) : {};
  return {'../../kit/shell/visual': {shellType}, ...stubs,
    ...(theme && !theme.textLines ? {'../../kit/theme': {...theme, textLines: lines => ({numberOfLines: lines})}} : {})};
}

/** The retained title route loaded with stubs keyed as they were for screens/watch/TitleScreen: the page drawn through
 * its kit view in the same call, and the view's own card wrappers. */
export async function loadTitleScreen(stubs) {
  const typed = withViewType(stubs);
  // A fixture Proxy that answers any path keeps answering the paths it was never told, as it did for the screen.
  const from = (folder, extra = {}) => new Proxy({...stubsFrom('screens/watch', folder, typed), ...extra}, {get: (moved, name) => Object.hasOwn(moved, name) ? moved[name] : stubs[name]});
  // One view module, so the page draws the same TitleEpisode and TitleRelated a test compares with.
  const view = await screenModule(new URL('../../src-next/kit/player/TitleScreenView.tsx', import.meta.url), from('kit/player'));
  const {TitleScreenPage} = await screenModule(new URL('../../src-next/pages/shared/player/TitleScreenPage.tsx', import.meta.url),
    from('pages/shared/player', {'../../../kit/player/TitleScreenView': view}));
  const {TitleEpisode, TitleRelated} = view;
  return {TitleEpisode, TitleRelated,
    NativeTitleScreen: props => {const tree = TitleScreenPage(props); return typeof tree?.type === 'function' ? tree.type(tree.props) : tree;}};
}
