/** Only existing controlled DEV routes are eligible; no production/auth routing. */
export function controlledEntry(entry) {
  if (typeof entry !== 'string') throw new Error('An explicit controlled DEV entry is required');
  const url = new URL(entry, 'http://fixture.invalid');
  if (url.origin !== 'http://fixture.invalid') throw new Error('External specimen entry is forbidden');
  if (/^\/src\/dev\/entries\/[^/]+\.html$/.test(url.pathname) || url.pathname.startsWith('/test/')) return {gallery: false};
  const id = value => typeof value === 'string' && /^[a-z0-9][a-z0-9-]*$/.test(value);
  if (url.pathname !== '/dev/kit' || url.searchParams.get('capture') !== 'true' ||
    !id(url.searchParams.get('family')) || !id(url.searchParams.get('case')) ||
    [...url.searchParams.keys()].some(key => !['family', 'case', 'capture', 'width', 'height'].includes(key))) throw new Error('Controlled Kit family/case/capture entry is required');
  return {gallery: true, family: url.searchParams.get('family'), case: url.searchParams.get('case'),
    width: url.searchParams.has('width') ? Number(url.searchParams.get('width')) : undefined,
    height: url.searchParams.has('height') ? Number(url.searchParams.get('height')) : undefined};
}

export async function verifyGalleryEntry(browser, entry, viewport) {
  const selected = controlledEntry(entry);
  if (!selected.gallery) return;
  const observed = await browser.evaluate(`(async()=>{const {loadGalleryFamilies}=await import('/src/dev/gallery-registry.ts');const families=(await loadGalleryFamilies(${JSON.stringify(selected.family)})).filter(value=>value.id===${JSON.stringify(selected.family)});const cases=families.flatMap(value=>value.cases.filter(item=>item.id===${JSON.stringify(selected.case)}));return{families:families.length,cases:cases.length,width:cases[0]?.width,height:cases[0]?.height}})()`);
  if (observed.families !== 1 || observed.cases !== 1 || (selected.width ?? observed.width) !== viewport.width || (selected.height ?? observed.height) !== viewport.height) throw new Error('Requested genuine gallery case/viewport does not exist; fallback rendering is not a specimen');
}

/** Await real finite animations; never remove transitions or change styles. */
export async function settleMotion(browser, {selector, timeoutMs = 5000} = {}) {
  if (selector !== undefined && (typeof selector !== 'string' || !selector) || !Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 30000) throw new Error('Explicit bounded motion readiness required');
  await browser.evaluate(`(async()=>{const roots=${selector === undefined ? '[document.documentElement]' : `document.querySelectorAll(${JSON.stringify(selector)})`};if(roots.length!==1)throw new Error('Motion specimen root must be unique');const root=roots[0];await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));const animations=document.getAnimations().filter(value=>!['finished','idle'].includes(value.playState)&&value.effect?.target&&(root===value.effect.target||root.contains(value.effect.target)));if(animations.some(value=>!Number.isFinite(value.effect?.getComputedTiming().endTime)))throw new Error('An infinite animation needs an explicit measured phase');let timer;try{await Promise.race([Promise.all(animations.map(value=>value.finished)),new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error('Finite specimen motion did not settle')),${timeoutMs})})]);}finally{clearTimeout(timer);}await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));})()`);
}
