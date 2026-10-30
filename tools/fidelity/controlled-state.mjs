import {colorDifference, parseColor} from './color.mjs';

/** Ordinary browser input on an explicit public DEV specimen, never DOM rewrites. */
export async function applyControlledState(browser, steps = []) {
  for (const step of steps) {
    if (!['click', 'insertText', 'tab'].includes(step.action) || typeof step.selector !== 'string' || !step.selector || step.action === 'insertText' && (typeof step.text !== 'string' || step.text.length > 1000)) throw new Error('Invalid controlled fixture interaction');
    const target = await browser.evaluate(`(()=>{const nodes=document.querySelectorAll(${JSON.stringify(step.selector)});if(nodes.length!==1)return{count:nodes.length};const e=nodes[0],r=e.getBoundingClientRect();return{count:1,x:r.x+r.width/2,y:r.y+r.height/2,visible:r.width>0&&r.height>0,disabled:Boolean(e.disabled),focused:document.activeElement===e}})()`);
    if (target.count !== 1 || !target.visible || target.disabled) throw new Error('Controlled fixture target is missing, ambiguous or unavailable');
    if (step.action === 'tab') {
      await browser.call('Input.dispatchKeyEvent', {type: 'keyDown', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9});
      await browser.call('Input.dispatchKeyEvent', {type: 'keyUp', key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9});
      const focused = await browser.evaluate(`document.activeElement===document.querySelector(${JSON.stringify(step.selector)})`);
      if (!focused) throw new Error('Ordinary Tab did not focus the declared fixture target');
    } else if (step.action === 'click') {
      await browser.call('Input.dispatchMouseEvent', {type: 'mousePressed', button: 'left', clickCount: 1, x: target.x, y: target.y});
      await browser.call('Input.dispatchMouseEvent', {type: 'mouseReleased', button: 'left', clickCount: 1, x: target.x, y: target.y});
    } else {
      if (!target.focused) throw new Error('Controlled fixture text input is not actually focused');
      await browser.call('Input.insertText', {text: step.text});
    }
  }
}

/** Expected public input facts are explicit adapter state, not auth4 assumptions. */
export function checkControlledState(actual, expectations = []) {
  const differences = [], carets = [];
  for (const expectation of expectations) {
    const nodes = actual.elements.filter(node => node.frame === expectation.frame), node = nodes[0];
    const valid = nodes.length === 1 && (expectation.focused === undefined || node.focused === expectation.focused) &&
      Object.entries(expectation.input ?? {}).every(([property, value]) => ['type', 'length'].includes(property) && node.input?.[property] === value);
    if (!valid) {differences.push({kind: 'fixture-state', label: expectation.frame, reason: 'Explicit controlled input state does not match'}); continue;}
    if (expectation.nativeCaret) {
      const index = expectation.nativeCaret.selection, color = colorDifference(node.style?.color, node.style?.caretColor);
      const caret = {frame: expectation.frame, ruling: 27, focused: Boolean(node.focused && actual.documentFocused),
        collapsedSelection: node.input?.selectionStart === index && node.input?.selectionEnd === index,
        colorFollowsText: Boolean(color && color.deltaE2000 <= 2 && color.alpha <= .02 && parseColor(node.style?.caretColor)?.[3] > 0),
        qualification: 'Native focus, insertion point and CSS colour; platform governs caret x/width/blink. No raster or timing claim.'};
      carets.push(caret);
      if (!caret.focused || !caret.collapsedSelection || !caret.colorFollowsText) differences.push({kind: 'native-caret-state', label: expectation.frame, ...caret});
    }
  }
  return {differences, carets};
}
