type MediaRuntime<Element> = Readonly<{mount: (element: Element) => () => void}>;

/** The app's one exclusive device: awaited before any new runtime's factory or mount so a
 * previous owner's actual retirement settles first. Rejection refuses the adoption. */
export type PlaybackAdoptionRetirement = () => Promise<void>;

let installedRetirement: PlaybackAdoptionRetirement | undefined;

/** Install the platform's device retirement; returns its removal. */
export function installPlaybackAdoptionRetirement(next: PlaybackAdoptionRetirement | undefined): () => void {
  installedRetirement = next;
  return () => { if (installedRetirement === next) installedRetirement = undefined; };
}

export type PlaybackAdoptionLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;

const always: PlaybackAdoptionLifetime = {signal: new AbortController().signal, isCurrent: () => true};

/** Owns attachment only. React owns the one persistent element; the actual runtime owns
 * disposal. Adoption is awaitable: the previous device owner must have retired before this
 * runtime's factory runs, and the slot stays unattached while that wait is in progress. */
export class PlaybackMediaOwner<Element> {
  private element?: Element;
  private current?: {runtime: MediaRuntime<Element>; unmount: () => void; release: () => void};
  private serial = 0;

  /** `null` disables the installed gate for a runtime that is itself the exclusive owner
   * (the launch page's local player); omitted uses the app-installed retirement. */
  constructor(private readonly retirement: PlaybackAdoptionRetirement | null | undefined = undefined) {}

  async adopt(runtime: MediaRuntime<Element> | undefined, element: Element, lifetime: PlaybackAdoptionLifetime = always): Promise<(() => void) | undefined> {
    const token = ++this.serial;
    if (this.element !== undefined && this.element !== element) throw new Error('playback-element-replaced');
    this.element = element;
    if (runtime && this.current?.runtime === runtime) return this.current.release;
    this.detach();
    if (!runtime) return undefined;
    const retire = this.retirement === null ? undefined : this.retirement ?? installedRetirement;
    if (retire) {
      try { await retire(); }
      catch { this.abandon(token, element); throw new Error('playback_retirement_failed'); }
    }
    // A superseded or cancelled adoption must not mount into the element.
    if (this.serial !== token || lifetime.signal.aborted || !lifetime.isCurrent()) {
      this.abandon(token, element);
      throw new DOMException('Playback adoption retired', 'AbortError');
    }
    const unmount = runtime.mount(element);
    const attachment = {runtime, unmount, release: () => {
      if (this.current !== attachment) return;
      this.current = undefined;
      unmount();
    }};
    this.current = attachment;
    return attachment.release;
  }

  /** Release this element's attachment synchronously and cancel a pending adoption for it.
   * A newer adoption of a different element is untouched. */
  release(element: Element): void {
    if (this.element !== element) return;
    ++this.serial;
    this.element = undefined;
    this.detach();
  }

  private detach(): void {
    const current = this.current;
    if (!current) return;
    this.current = undefined;
    current.unmount();
  }

  /** Only this exact attempt's element is cleared; a newer adoption keeps its own. */
  private abandon(token: number, element: Element): void {
    if (this.serial === token && this.element === element) this.element = undefined;
  }
}
