/** Select a complete authored screen embedded in a contact sheet, never a UI crop.
 * Element/run bounds are already root-relative. No element coordinate is changed. */
export function authoredReferenceScreen(measured, viewport) {
  if (measured.measurement?.renderer !== 'dom' || measured.viewport?.deviceScaleFactor !== 1 ||
    viewport?.deviceScaleFactor !== 1 || measured.root?.width !== viewport.width || measured.root?.height !== viewport.height)
    throw new Error('Reference must be a complete authored DPR1 screen at its exact natural logical size');
  const screen = structuredClone(measured);
  screen.measurement.sourceRole = 'reference-frame';
  screen.referencePresentation = {windowViewport: measured.viewport, windowBounds: measured.root, fullWindowViewport: measured.fullViewport};
  screen.root = {...measured.root, x: 0, y: 0};
  screen.viewport = {...viewport};
  return screen;
}
