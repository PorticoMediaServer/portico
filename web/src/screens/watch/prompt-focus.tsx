import {Component, createContext, useContext, type ReactNode, type RefObject} from 'react';
import {applyRemovalFocus} from '../../kit/layout/focus-context';

export type PromptFocusHost = Readonly<{
  source: object;
  root: RefObject<HTMLDivElement | null>;
  seek: RefObject<HTMLInputElement | null>;
  seekPresent: boolean;
  focusAllowed: boolean;
  controlsVisible: boolean;
  current: () => boolean;
}>;
export const WatchPromptFocus = createContext<PromptFocusHost | undefined>(undefined);
/** The actual player's render, not the parent's later visibility notification. */
export function useWatchPromptControls(fallback: boolean) {return useContext(WatchPromptFocus)?.controlsVisible ?? fallback;}
type Props = Readonly<{
  host?: PromptFocusHost; active: boolean; paintKey?: string;
  element: RefObject<HTMLDivElement | null>; children: ReactNode;
}>;
type Committed = Readonly<{host: PromptFocusHost; root: HTMLDivElement; seek: HTMLInputElement | null; prompt: HTMLDivElement}>;

/** Actual committed DOM only. The mutation boundary calls this before a prompt is inert. */
export class PromptFocusOwner {
  private committed: Committed | undefined;
  private keyboard = false;
  private detach: (() => void) | undefined;
  commit(host: PromptFocusHost | undefined, prompt: HTMLDivElement | null) {
    const root = host?.root.current;
    if (!host || !host.focusAllowed || !root?.isConnected || !prompt?.isConnected || !root.contains(prompt) || !host.current()) {this.dispose();return;}
    if (this.committed?.host.source !== host.source || this.committed.root !== root) {
      this.dispose();
      const active = root.ownerDocument.activeElement;
      this.keyboard = active instanceof HTMLElement && root.contains(active) && active.matches(':focus-visible');
      const key = () => {this.keyboard = true;}, pointer = () => {this.keyboard = false;};
      const focus = (event: FocusEvent) => {if (event.target instanceof HTMLElement) this.keyboard = event.target.matches(':focus-visible');};
      root.addEventListener('keydown', key, true);root.addEventListener('focusin', focus, true);
      root.ownerDocument.addEventListener('pointerdown', pointer, true);
      this.detach = () => {root.removeEventListener('keydown', key, true);root.removeEventListener('focusin', focus, true);root.ownerDocument.removeEventListener('pointerdown', pointer, true);};
    }
    this.committed = {host, root, prompt, seek: host.seek.current};
  }
  beforeRetire(host: PromptFocusHost | undefined) {
    const original = this.committed;
    if (!original || !host || !host.focusAllowed || original.host.source !== host.source || !this.keyboard || !original.host.current() || host.root.current !== original.root
      || !original.root.isConnected || !original.root.contains(original.prompt)) return;
    const active = original.root.ownerDocument.activeElement;
    if (!(active instanceof Node) || !original.prompt.contains(active)) return;
    // A replaced/disabled/hidden seek is never adopted. The original player region is
    // already a real focus stop; the shared resolver refuses hidden/inert/modal contexts.
    const seek = host.seekPresent && original.seek && host.seek.current === original.seek && original.root.contains(original.seek) ? original.seek : null;
    applyRemovalFocus({root: original.root, order: [original.prompt, ...(seek ? [seek] : [])], surviving: seek ? [seek] : []}, original.prompt);
  }
  dispose() {this.detach?.();this.detach = undefined;this.committed = undefined;this.keyboard = false;}
}

/** React's before-mutation lifecycle sees the OLD focused prompt before inert/removal.
 * Layout effects would be too late. No render publishes refs or opening state. */
class PromptFocusBoundary extends Component<Props> {
  private owner = new PromptFocusOwner();
  componentDidMount() {this.owner.commit(this.props.host, this.props.element.current);}
  getSnapshotBeforeUpdate(previous: Props) {
    if (previous.active && (!this.props.active || previous.paintKey !== this.props.paintKey)) this.owner.beforeRetire(this.props.host);
    return null;
  }
  componentDidUpdate() {this.owner.commit(this.props.host, this.props.element.current);}
  componentWillUnmount() {this.owner.dispose();}
  render() {return this.props.children;}
}
export function WatchPromptRetirement(props: Omit<Props, 'host'>) {
  return <PromptFocusBoundary {...props} host={useContext(WatchPromptFocus)}/>;
}
