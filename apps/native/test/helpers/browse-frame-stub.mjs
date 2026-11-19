// A shallow stand-in for kit/frames BrowseFrame in screen tests that walk the rendered element tree. The real frame places
// the hero and the rows and hands them to the page's own scroller; expandFrames draws that same order, scroller(hero +
// rows), in place of the frame element, so a test reads the screen's own parts where it always has.
export const browseFrameStub = {BrowseFrame: 'BrowseFrame'};
// Every element the frame does not touch is returned as it is, so a test that checks identity still finds it.
export function expandFrames(node) {
  if (Array.isArray(node)) {const next = node.map(expandFrames); return next.every((child, index) => child === node[index]) ? node : next;}
  if (!node || typeof node !== 'object' || !('props' in node)) return node;
  if (node.type === 'BrowseFrame') {
    const content = {type: 'BrowseFrameRows', props: {onLayout: node.props.onRowsLayout, children: [node.props.hero ?? null, node.props.children]}};
    return expandFrames(node.props.scroller ? node.props.scroller(content, {paddingBottom: 0}) : content);
  }
  const children = expandFrames(node.props.children);
  return children === node.props.children ? node : {...node, props: {...node.props, children}};
}
