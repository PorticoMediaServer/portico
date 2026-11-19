// Stand-ins for the Live TV page's drawn header parts in hand-built screen maps (tablet-play6, states 17c).
// The tab pair keeps its real test ids: `live-recordings` is the Recordings tab, `live-tab-guide` the Guide tab,
// each pressable only where the page gave it a handler, as the real Segmented calls only the other tab's. The conflict dot
// keeps its real test id (`live-recordings-conflict`), on the tab and wherever the page draws it.
export function liveTabsFixture(react) {
  const LiveConflictDot = ({style}) => react.createElement('ConflictDot', {testID: 'live-recordings-conflict', style});
  const NativeLiveTabs = ({value, onGuide, onRecordings, conflicts = 0, children}) => react.createElement('LiveTabs', {testID: 'live-header', value, conflicts},
    onGuide || onRecordings ? [
      react.createElement('Tab', {key: 'guide', testID: 'live-tab-guide', selected: value === 'guide', onPress: value === 'guide' ? () => {} : onGuide}),
      react.createElement('Tab', {key: 'recordings', testID: 'live-recordings', selected: value === 'recordings', onPress: value === 'recordings' ? () => {} : onRecordings},
        conflicts > 0 ? react.createElement(LiveConflictDot, null) : null),
    ] : null, children);
  return {'./LiveTabs': {NativeLiveTabs, LiveConflictDot}, ...liveFrameFixture(react)};
}
// The Live pages draw on the kit LiveFrame: its stand-in lays its regions out in the frame's order (title, the source picker,
// the buttons, the tabs, the chips, the toolbar, then the guide or list) so the maps can read them.
export function liveFrameFixture(react) {
  const LiveFrame = ({title, switcher, actions, tabs, chips, toolbar, list, children, testID}) => react.createElement('LiveFrame', {testID, title},
    switcher ?? null, actions ?? null, tabs || null, chips ?? null, toolbar ?? null, list ?? null, children ?? null);
  return {'../../kit/frames': {LiveFrame}};
}
export const ownerTagFixture = {'../../kit/shell/ServerIdentity': {OwnerTag: 'OwnerTag'}};
