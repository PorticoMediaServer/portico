// A shallow stand-in for kit/frames BrowseFrame in screen tests that walk the rendered element tree. The real frame places
// the hero and the rows and hands them to the page's own scroller; expandFrames draws that same order, scroller(hero +
// rows), in place of the frame element, so a test reads the screen's own parts where it always has.
export const browseFrameStub = {BrowseFrame: 'BrowseFrame'};
// Every element the frame does not touch is returned as it is, so a test that checks identity still finds it.
export function expandFrames(node) {
  if (Array.isArray(node)) {const next = node.map(expandFrames); return next.every((child, index) => child === node[index]) ? node : next;}
  if (!node || typeof node !== 'object' || !('props' in node)) return node;
  if (node.type === 'BrowseFrame') {
    const {state, notice, foot, scroll} = node.props;
    // A failure keeps the top and draws the page state; the frame's own regions (notice, foot, scroller) as it orders them.
    if (state && 'title' in state) return {type: 'BrowseFrameState', props: {children: [node.props.hero ?? null, {type: 'PageStateFrame', props: state}]}};
    const rows = [notice ?? null, node.props.children, foot ? {type: 'BrowseFoot', props: foot} : null];
    const content = {type: 'BrowseFrameRows', props: {onLayout: node.props.onRowsLayout, children: [node.props.hero ?? null, rows]}};
    if (scroll) return expandFrames({type: 'ScrollView', props: {...scroll, contentContainerStyle: {paddingBottom: scroll.bottomInset ?? 0}, children: content}});
    return expandFrames(node.props.scroller ? node.props.scroller(content, {paddingBottom: 0}) : content);
  }
  const children = expandFrames(node.props.children);
  return children === node.props.children ? node : {...node, props: {...node.props, children}};
}
