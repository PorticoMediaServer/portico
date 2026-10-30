// agent-device 0.21.25 CLI snapshot --json: success/data + RawSnapshotNode facts.
// Pure metadata validation only. Never accepts labels, masks, selection offsets or secrets.
export function securePasswordTarget(snapshot, {appBundleId, refsGeneration}) {
  const refuse = () => { throw new Error('secure password metadata unavailable or ambiguous'); };
  const data = snapshot?.success === true ? snapshot.data : null;
  if (!data || data.appBundleId !== appBundleId || appBundleId !== 'tv.getportico.portico' ||
      !Number.isSafeInteger(refsGeneration) || refsGeneration < 0 ||
      data.refsGeneration !== refsGeneration || data.truncated !== false || !Array.isArray(data.nodes)) refuse();
  const focused = data.nodes.filter(node => node?.focused === true && node.editable === true);
  if (focused.length !== 1) refuse();
  const node = focused[0];
  if (node.password !== true || node.enabled !== true || node.visibleToUser !== true ||
      node.hittable !== true || node.interactionBlocked === true || node.bundleId !== appBundleId ||
      typeof node.ref !== 'string' || !/^e[0-9]+$/.test(node.ref)) refuse();
  const rect = node.rect;
  if (!rect || !['x', 'y', 'width', 'height'].every(key => Number.isFinite(rect[key])) ||
      rect.x < 0 || rect.y < 0 || rect.width <= 0 || rect.height <= 0) refuse();
  return {ref: node.ref, refsGeneration, rect: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}};
}
