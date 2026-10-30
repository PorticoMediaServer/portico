/** Pointer-edge scrolling, independent of React so cancellation and frame cadence are testable. */
export function orderEdgeVelocity(y: number, top: number, bottom: number): number {
  if (bottom <= top || y < top - 48 || y > bottom + 48) return 0;
  const edge = Math.min(48, (bottom - top) / 3);
  if (y < top + edge) return -600 * Math.min(1, (top + edge - y) / edge);
  if (y > bottom - edge) return 600 * Math.min(1, (y - bottom + edge) / edge);
  return 0;
}

export function createOrderAutoScroll(port: Readonly<{
  request(callback: (time: number) => void): number; cancel(id: number): void;
  bounds(): Readonly<{top: number; bottom: number}>;
  scroll(delta: number): void; retarget(x: number, y: number): void;
}>) {
  let frame: number | undefined, point: {x: number; y: number} | undefined, previous: number | undefined;
  const tick = (time: number) => {
    frame = undefined;
    if (!point) return;
    const {top, bottom} = port.bounds(), velocity = orderEdgeVelocity(point.y, top, bottom);
    const elapsed = previous === undefined ? 1000 / 60 : Math.min(32, Math.max(0, time - previous));
    previous = time;
    if (velocity) {port.scroll(velocity * elapsed / 1000);port.retarget(point.x, point.y);}
    if (point) frame = port.request(tick);
  };
  return {
    update(x: number, y: number) {point = {x, y};if (frame === undefined) frame = port.request(tick);},
    stop() {point = undefined;previous = undefined;if (frame !== undefined) port.cancel(frame);frame = undefined;},
  };
}

/** The nearest independently scrolling list owns the drag; otherwise scroll the page. */
export function orderScrollContainer(button: HTMLElement): HTMLElement {
  for (let element = button.parentElement; element; element = element.parentElement) {
    if (/(auto|scroll)/.test(getComputedStyle(element).overflowY) && element.scrollHeight > element.clientHeight) return element;
  }
  return document.scrollingElement as HTMLElement;
}
