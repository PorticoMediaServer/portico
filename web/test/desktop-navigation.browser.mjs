import assert from 'node:assert/strict';

// Real Shell/React/More routes over the explicit synthetic size-shell fixture.
// This is only the fine desktop fit regression, not authentication or Home acceptance.
export async function checkDesktopNavigationFit(page, origin, onCase = async () => {}) {
  const inspect = () => page.evaluate(() => {
    const nav = document.querySelector('nav[aria-label="Main"]'), list = nav.querySelector('[data-desktop-destinations]');
    const rect = element => element.getBoundingClientRect().toJSON();
    const controls = [...nav.querySelectorAll('a,button')].filter(element => !element.closest('[aria-hidden="true"]')).map(element => {
      const range = document.createRange(); range.selectNodeContents(element);
      return {name: element.getAttribute('aria-label') || element.textContent.trim(), bounds: rect(element), text: range.getBoundingClientRect().toJSON(),
        clientWidth: element.clientWidth, scrollWidth: element.scrollWidth, href: element.getAttribute('href')};
    });
    return {width: innerWidth, navigation: document.querySelector('[data-navigation]').dataset.navigation,
      pointer: document.querySelector('[data-pointer]').dataset.pointer, list: rect(list), controls,
      libraries: [...list.querySelectorAll(':scope > li > a')].filter(element => element.getAttribute('href')?.startsWith('/library/')).map(element => ({name: element.textContent, href: element.getAttribute('href')})),
      expected: window.sizeShellProof.bindings.shell.identity.libraries.map(({id,name}) => ({id,name})),
      documentWidth: document.documentElement.scrollWidth, sameHome: !window.fitHome || window.fitHome === document.querySelector('[data-home-document]')};
  });
  const check = async () => {
    await page.evaluate(() => document.fonts.ready);
    await page.waitForFunction(() => [...document.querySelectorAll('[data-desktop-destinations] > li > a')].every(element => element.getBoundingClientRect().right <= document.querySelector('[data-desktop-destinations]').getBoundingClientRect().right + .5));
    const value = await inspect();
    assert.equal(value.navigation, 'topbar'); assert.equal(value.pointer, 'fine'); assert.equal(value.documentWidth, value.width);
    for (const control of value.controls) {
      assert.ok(control.bounds.left >= 0 && control.bounds.right <= value.width, JSON.stringify(control));
      assert.ok(control.scrollWidth <= control.clientWidth + 1, JSON.stringify(control));
      assert.ok(control.text.left >= control.bounds.left - 1 && control.text.right <= control.bounds.right + 1, JSON.stringify(control));
    }
    assert.deepEqual(value.libraries.map(item => item.name), value.expected.slice(0, value.libraries.length).map(item => item.name));
    const nav = page.getByRole('navigation', {name:'Main', exact:true});
    for (const [name,path] of [['Home','/'],['Live TV','/live'],['Search','/search'],['Saved','/saved'],['Notifications, 3 unread','/notifications']]) {
      assert.equal(new URL(await nav.getByRole('link',{name,exact:true}).getAttribute('href'),origin).pathname,path);
    }
    await nav.getByRole('button',{name:'Test profile, account menu',exact:true}).waitFor();
    await nav.getByRole('button',{name:'Server activity: 2 streams',exact:true}).waitFor();
    return value;
  };
  const checkMore = async value => {
    const overflow = value.expected.slice(value.libraries.length), more = page.getByRole('navigation',{name:'Main',exact:true}).getByRole('button',{name:'More',exact:true});
    assert.equal(await more.count(), Number(overflow.length > 0));
    if (!overflow.length) return [];
    await more.click();
    const items = page.locator('[data-menu-item]'); await items.first().waitFor();
    await page.getByRole('menu',{name:'Libraries',exact:true}).evaluate(async element => {
      await Promise.all(element.getAnimations({subtree:true}).map(animation => animation.finished.catch(() => {})));
    });
    assert.deepEqual(await items.allTextContents(), overflow.map(item => item.name));
    for (const item of overflow) await page.getByRole('menuitemradio',{name:item.name,exact:true}).waitFor();
    const labels = await items.evaluateAll(elements => elements.map(element => {
      const label=element.querySelector('span');return {name:element.textContent, width:label.clientWidth, scrollWidth:label.scrollWidth, height:element.clientHeight};
    }));
    for (const label of labels) assert.ok(label.scrollWidth <= label.width + 1, JSON.stringify(label));
    return overflow;
  };
  const results=[];
  await page.setViewportSize({width:1024,height:900});
  await page.goto(`${origin}/test/size-shell.html`,{waitUntil:'networkidle'});
  await page.getByRole('button',{name:'Server activity: 2 streams',exact:true}).waitFor();
  await page.evaluate(()=>{window.fitHome=document.querySelector('[data-home-document]');});
  const narrow = await check(); assert.ok(narrow.libraries.length < 6); assert.ok(narrow.libraries.length > 0);
  results.push(narrow); await onCase({id:'1024-6',result:narrow});
  const overflow=await checkMore(narrow); await page.keyboard.press('Escape');
  await page.locator('[data-menu-item]').first().waitFor({state:'detached'});
  await page.setViewportSize({width:1440,height:900});
  await page.waitForFunction(()=>document.querySelectorAll('[data-desktop-destinations] > li > a[href^="/library/"]').length===6);
  const normal=await check(); assert.equal(normal.libraries.length,6); assert.equal(normal.sameHome,true);
  results.push(normal); await onCase({id:'normal-1440',result:normal});
  await page.setViewportSize({width:1024,height:900});
  const returned=await check();assert.deepEqual(returned.libraries,narrow.libraries);assert.equal(returned.sameHome,true);
  results.push(returned);await onCase({id:'1024-8',result:returned});await onCase({id:'expanded-fine',result:returned});
  await checkMore(returned);
  await page.getByRole('menuitemradio',{name:overflow[0].name,exact:true}).click();
  await page.waitForFunction(id=>window.sizeShellProof.router.state.location.pathname==='/library/'+encodeURIComponent(id),overflow[0].id);
  const route=await page.evaluate(()=>window.sizeShellProof.router.state.location.pathname);
  await page.goto(`${origin}/test/size-shell.html?libraryTitles=short`,{waitUntil:'networkidle'});
  const short=await check(); assert.deepEqual(short.libraries.map(item=>item.name),['A','B']);await checkMore(short);
  results.push(short); await onCase({id:'short-1024',result:short});
  await page.goto(`${origin}/test/size-shell.html?libraryTitles=long`,{waitUntil:'networkidle'});
  const long=await check();assert.equal(long.libraries.length,0);const longOverflow=await checkMore(long);assert.equal(longOverflow.length,3);
  results.push(long);await onCase({id:'long-more-1024',result:long});await page.keyboard.press('Escape');
  return {results,overflowRoute:route,realReact:true,syntheticBoundary:true,actualAuthenticationVerified:false};
}
