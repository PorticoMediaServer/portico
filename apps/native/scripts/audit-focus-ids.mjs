import {readFileSync, readdirSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import ts from 'typescript';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const tags = new Set(['Focusable', 'Pressable', 'TextInput', 'TextField', 'Button', 'ResumeButton', 'Chip', 'Switch', 'Select', 'RichSelect', 'Tabs', 'Segmented', 'MenuRow', 'CardAction']);
const sites = [];
function scan(directory) {
  for (const entry of readdirSync(directory, {withFileTypes: true})) {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) {scan(file); continue;}
    if (!file.endsWith('.tsx')) continue;
    const source = readFileSync(file, 'utf8'), ast = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    function visit(node) {
      if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
        const tag = node.tagName.getText(ast), attrs = node.attributes.properties;
        const attr = name => attrs.find(value => ts.isJsxAttribute(value) && value.name.getText(ast) === name);
        if (tags.has(tag) && attr('focusable')?.initializer?.getText(ast) !== '{false}') {
          const id = attr('testID')?.initializer?.getText(ast), focusId = attr('focusId')?.initializer?.getText(ast);
          const forwarded = attrs.some(ts.isJsxSpreadAttribute);
          const unstable = id && /\b(?:index|useId|Math\.random|Date\.now)\b/.test(id);
          sites.push({file: path.relative(root, file), line: ast.getLineAndCharacterOfPosition(node.getStart(ast)).line + 1, tag,
            testID: id ?? null, focusId: focusId ?? null,
            status: unstable ? 'unstable-identity' : id ? 'explicit-id-seam' : focusId ? 'requires-region-namespace' : forwarded ? 'requires-forwarded-caller-id' : 'missing-caller-id'});
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(ast);
  }
}
scan(path.join(root, 'src-next/kit'));
const counts = Object.fromEntries([...new Set(sites.map(site => site.status))].map(status => [status, sites.filter(site => site.status === status).length]));
console.log(JSON.stringify({scope: 'Every potential Phase1 kit focusable source site, including conditional phone/TV branches. This is a source inventory, not runtime ID uniqueness proof. Optional/caller seams require actual consumer IDs before device admission.', counts, sites}, null, 2));
if (sites.some(site => site.status !== 'explicit-id-seam')) process.exitCode = 1;
