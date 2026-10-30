/** Screen-harness stand-ins for the viewport list (MusicScrollView/MusicWindow). Element-tree
 * tests don't lay out, so `expandWindows` draws every row a window would mount, in order. */
export const musicWindowStub = {MusicScrollView: 'MusicScrollView', MusicWindow: 'MusicWindow'};

export function expandWindows(node: any): any {
  if (Array.isArray(node)) return node.map(expandWindows);
  if (!node || typeof node !== 'object' || !node.props) return node;
  if (node.type === 'MusicWindow') {
    const {items, renderItem, ...rest} = node.props;
    return {...node, props: {...rest, items, renderItem, children: items.map((item: unknown, index: number) => expandWindows(renderItem(item, index)))}};
  }
  return node.props.children === undefined ? node : {...node, props: {...node.props, children: expandWindows(node.props.children)}};
}
