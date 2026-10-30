import {Component, type ReactNode, type RefObject} from 'react';
import {RemovalFocusOwner} from './removal-focus-owner';
import {captureCardInteractionRetirement} from '../cards/interaction-admission';

export type GridRemovalFocusProps = Readonly<{
  owner: RemovalFocusOwner; root: RefObject<HTMLDivElement | null>; children: ReactNode;
  generation: number; accepted: boolean; keys: ReadonlySet<string>;
  viewport: Readonly<{width: number; height: number; top: number}>;
}>;

/** Sparse windows cannot use a passive/layout removal guess: getSnapshotBeforeUpdate runs while
 * the original bounded DOM is still connected, before React removes it. This retains no items. */
export class GridRemovalFocus extends Component<GridRemovalFocusProps> {
  private commit() {
    const root = this.props.root.current;
    // WindowedGrid's one extent owns only its bounded mounted slots, never the catalogue.
    this.props.owner.commit(root, root?.firstElementChild ? [...root.firstElementChild.children] as HTMLElement[] : []);
  }
  componentDidMount() {this.props.owner.activate();this.commit();}
  getSnapshotBeforeUpdate(previous: GridRemovalFocusProps) {
    const next = this.props;
    const root = previous.root.current;
    const removed = root?.firstElementChild ? [...root.firstElementChild.children].flatMap(node => {
      const element = node as HTMLElement, key = element.dataset.layoutItem;
      return key !== undefined && (previous.owner !== next.owner || !next.keys.has(key)) ? [element] : [];
    }) : [];
    const withdraw = captureCardInteractionRetirement(removed);
    if (previous.owner !== next.owner) {previous.owner.dispose();withdraw();next.owner.activate();return null;}
    const sameViewport = previous.viewport.width === next.viewport.width && previous.viewport.height === next.viewport.height && previous.viewport.top === next.viewport.top;
    try {if (sameViewport && next.accepted && previous.generation !== next.generation) next.owner.beforeLeave(next.keys);}
    finally {withdraw();}
    return null;
  }
  componentDidUpdate() {this.commit();}
  componentWillUnmount() {this.props.owner.dispose();}
  render() {return this.props.children;}
}
