import {existsSync, readdirSync, readFileSync, realpathSync, statSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import ts from 'typescript';

const sourceFile = /\.[cm]?[jt]sx?$/;
// Metro's static image extensions. Assets must name an exact file, without source-module fallbacks.
const imageFile = /\.(?:bmp|gif|jpg|jpeg|png|psd|svg|webp)$/;
const inside = (file, directory) => file === directory || file.startsWith(directory + path.sep);
const real = file => existsSync(file) ? realpathSync(file) : path.resolve(file);

/** Follow the actual graph, including helpers outside src-next and symlink destinations. */
export function auditBoundary({appRoot, configPath = path.join(appRoot, 'tsconfig.json')}) {
  appRoot = path.resolve(appRoot);
  const old = real(path.join(appRoot, 'src'));
  const next = path.join(appRoot, 'src-next');
  const config = ts.readConfigFile(configPath, ts.sys.readFile);
  if (config.error) throw new Error(ts.flattenDiagnosticMessageText(config.error.messageText, '\n'));
  const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, path.dirname(configPath));
  if (parsed.errors.some(e => e.code !== 18003)) throw new Error(parsed.errors.map(e => ts.flattenDiagnosticMessageText(e.messageText, '\n')).join('\n'));
  const visited = new Set(), directories = new Set(), violations = [];
  const fail = (file, node, message, ast) => {
    const line = node ? ast.getLineAndCharacterOfPosition(node.getStart(ast)).line + 1 : 1;
    violations.push(`${path.relative(appRoot, file)}:${line}: ${message}`);
  };
  const roots = [];
  function walk(directory) {
    const actual = real(directory);
    if (directories.has(actual)) return;
    directories.add(actual);
    for (const entry of readdirSync(directory, {withFileTypes: true})) {
      const at = path.join(directory, entry.name);
      if (entry.name === 'node_modules') continue;
      if (statSync(at).isDirectory()) walk(at);
      else if (sourceFile.test(at)) roots.push(at);
    }
  }
  if (existsSync(next)) walk(next);
  for (const entry of ['App.tsx', 'index.js']) if (existsSync(path.join(appRoot, entry))) roots.push(path.join(appRoot, entry));
  function constant(node) {
    if (ts.isStringLiteralLike(node)) return node.text;
    if (ts.isParenthesizedExpression(node)) return constant(node.expression);
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
      const a = constant(node.left), b = constant(node.right);
      if (a !== undefined && b !== undefined) return a + b;
    }
  }
  function visit(file, chain = []) {
    file = real(file);
    if (inside(file, old)) {fail(file, null, `old src is forbidden (${chain.join(' -> ')})`); return;}
    if (visited.has(file) || file.includes(`${path.sep}node_modules${path.sep}`) || !sourceFile.test(file)) return;
    visited.add(file);
    const ast = ts.createSourceFile(file, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true);
    function dependency(expression, node) {
      const specifier = constant(expression);
      if (specifier === undefined) {fail(file, node, 'module path must be statically resolvable', ast); return;}
      if ((specifier.startsWith('./') || specifier.startsWith('../') || path.isAbsolute(specifier)) && imageFile.test(specifier)) {
        const assetPath = path.resolve(path.dirname(file), specifier), asset = real(assetPath);
        if (inside(assetPath, path.join(appRoot, 'src')) || inside(asset, old)) {
          fail(file, node, `old src is forbidden (${specifier})`, ast);
        } else if (!existsSync(assetPath) || !statSync(assetPath).isFile()) {
          fail(file, node, `cannot resolve ${specifier}`, ast);
        }
        return;
      }
      const resolved = ts.resolveModuleName(specifier, file, parsed.options, ts.sys).resolvedModule?.resolvedFileName;
      if (!resolved) {
        if (specifier.startsWith('.') || specifier.startsWith('/') || Object.keys(parsed.options.paths ?? {}).some(alias => specifier.startsWith(alias.replace(/\*.*$/, '')))) fail(file, node, `cannot resolve ${specifier}`, ast);
        return;
      }
      visit(resolved, [...chain, path.relative(appRoot, file), specifier]);
      // Metro prefers native/platform files over an unsuffixed module. Inspect every variant.
      const base = resolved.replace(/(?:\.(?:native|android|ios|tv))?\.[cm]?[jt]sx?$/, '');
      for (const platform of ['native', 'android', 'ios', 'tv']) for (const extension of ['ts', 'tsx', 'js', 'jsx']) {
        const variant = `${base}.${platform}.${extension}`;
        if (existsSync(variant)) visit(variant, [...chain, specifier]);
      }
    }
    function scan(node) {
      if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier) dependency(node.moduleSpecifier, node);
      if (ts.isImportEqualsDeclaration(node) && ts.isExternalModuleReference(node.moduleReference) && node.moduleReference.expression) dependency(node.moduleReference.expression, node);
      if (ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument)) dependency(node.argument.literal, node);
      if (ts.isCallExpression(node)) {
        const callee = node.expression;
        if (callee.kind === ts.SyntaxKind.ImportKeyword || ts.isIdentifier(callee) && callee.text === 'require' || ts.isPropertyAccessExpression(callee) && ts.isIdentifier(callee.expression) && callee.expression.text === 'require' && callee.name.text === 'resolve') {
          if (node.arguments[0]) dependency(node.arguments[0], node);
          else fail(file, node, 'module path is required', ast);
        }
        if (ts.isPropertyAccessExpression(callee) && ts.isIdentifier(callee.expression) && callee.expression.text === 'require' && callee.name.text === 'context') {
          const directory = node.arguments[0] && constant(node.arguments[0]);
          if (!directory || !inside(real(path.resolve(path.dirname(file), directory)), real(path.join(next, 'dev')))) fail(file, node, 'gallery context must stay inside src-next/dev', ast);
        }
      }
      ts.forEachChild(node, scan);
    }
    scan(ast);
  }
  roots.forEach(file => visit(file));
  return {violations: [...new Set(violations)], files: [...visited].sort()};
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const appRoot = process.argv[2] ? path.resolve(process.argv[2]) : path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
  const result = auditBoundary({appRoot});
  if (result.violations.length) {console.error(result.violations.join('\n')); process.exitCode = 1;}
  else console.log(`native next boundary clean (${result.files.length} source modules)`);
}
