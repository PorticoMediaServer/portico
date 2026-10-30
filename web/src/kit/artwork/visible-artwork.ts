/** One observer batches the currently mounted targets in this document. All
 * changed visible memberships are registered before any decoded paint is offered. */
type Visibility = Readonly<{change: (visible: boolean | undefined) => void; flush: () => void}>;
type Registration = {live: boolean; at: number; callbacks: Visibility};
type Registry = {live: boolean; observer: IntersectionObserver; entries: Map<Element, Registration>};
const documents = new WeakMap<Document, Registry>();

export function observeVisibleArtwork(element: Element, callbacks: Visibility): () => void {
  const document = element.ownerDocument, view = document.defaultView;
  const Observer = view?.IntersectionObserver;
  if (!Observer) {
    // There is no actual visibility witness on this browser: keep the image
    // independent rather than inventing visible group membership.
    callbacks.change(undefined); callbacks.flush(); return () => callbacks.change(false);
  }
  let registry = documents.get(document);
  if (!registry) {
    const entries = new Map<Element, Registration>();
    const original: Registry = {live: true, entries, observer: new Observer(changes => {
      if (!original.live) return;
      const latest = new Map<Element, IntersectionObserverEntry>();
      for (const change of changes) latest.set(change.target, change);
      const batch = [...latest.values()].flatMap(change => {
        const entry = entries.get(change.target);
        return entry?.live && change.time >= entry.at ? [{entry, target: change.target, visible: change.isIntersecting && change.intersectionRatio > 0}] : [];
      });
      // New pending predecessors must exist before removing an outgoing head can
      // release an already-ready sibling in the same visibility batch.
      batch.sort((left, right) => Number(right.visible) - Number(left.visible));
      for (const {entry, target, visible} of batch) if (entry.live && entries.get(target) === entry) entry.callbacks.change(visible);
      for (const {entry, target} of batch) if (entry.live && entries.get(target) === entry) entry.callbacks.flush();
    })};
    registry = original; documents.set(document, registry);
  }
  const original = registry, registration: Registration = {live: true, at: view.performance.now(), callbacks};
  original.entries.set(element, registration); original.observer.observe(element);
  return () => {
    if (!registration.live) return;
    registration.live = false;
    if (original.entries.get(element) === registration) {
      original.entries.delete(element); original.observer.unobserve(element);
    }
    callbacks.change(false);
    if (!original.entries.size) {
      original.live = false; original.observer.disconnect();
      if (documents.get(document) === original) documents.delete(document);
    }
  };
}
