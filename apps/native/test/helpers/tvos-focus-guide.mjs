import {readFileSync} from 'node:fs';

// react-native-tvos's own rule for a TVFocusGuideView's native props
// (Libraries/Components/TV/TVFocusGuideView.js). Tests run the app's element props through it,
// and assertTVFocusGuideRule fails if the installed library stops computing them this way.
const source = new URL('../../node_modules/react-native/Libraries/Components/TV/TVFocusGuideView.js', import.meta.url);
export function assertTVFocusGuideRule(assert) {
  const text = readFileSync(source, 'utf8').replace(/\s+/g, ' ');
  assert.match(text, /const tvOSSelectable = destinationsProp \|\| autoFocus \? focusable !== false : false;/);
  assert.match(text, /autoFocus=\{focusable === false \? true : autoFocus\}/);
  assert.match(text, /isTVSelectable=\{tvOSSelectable\}/);
}
export function tvosNativeProps({destinations, autoFocus, focusable}) {
  return {isTVSelectable: destinations || autoFocus ? focusable !== false : false, autoFocus: focusable === false ? true : autoFocus};
}
