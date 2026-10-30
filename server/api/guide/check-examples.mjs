// check-examples.mjs — validate guide JSON examples against the real OpenAPI Schemas.
//
// Usage: `node server/api/guide/check-examples.mjs`
// Wired into nothing (per M30 handoff); just run it.
//
// What it does:
//  1. Reads every `server/api/guide/*.md`.
//  2. Extracts fenced ```json blocks preceded by a marker comment:
//       <!-- example: <id> file=<openapi-file> operationId=<op>
//            direction=request|response status=<code> contentType=<ct>
//            schema=<ref> -->
//     where <ref> is either `#/components/schemas/<Name>`,
//     `@request <path> <method>` (requestBody schema), or
//     `@response <path> <method> <status>` (that response's schema for
//     <ct>, defaulting to the block's contentType or application/json).
//  3. Loads the generated OpenAPI JSON document and resolves local schemas.
//  4. Validates each example with a small built-in JSON-Schema validator
//     (OpenAPI 3.1 subset: types incl. type-arrays, enum/const, pattern,
//     length/range limits, required/properties/additionalProperties, items,
//     oneOf/anyOf/allOf/not, $ref). `format` is not enforced.
//  5. Checks every cited (file, operationId) exists, and that every
//     `METHOD /vN...` mention in the guide resolves to a real path+method
//     in one of the loaded OpenAPI documents.
//
// Exit 0 when every example validates and every citation resolves.

import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, basename } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const API_DIR = join(HERE, "..");
const GUIDE_DIR = HERE;

const YAML_FILES = [];
const JSON_FILES = ["portico.openapi.json"];

// ---------------------------------------------------------------- YAML load

function loadYamlViaPython(absPath) {
  const script = `
import json, sys
try:
    import yaml
except ImportError as e:
    print("PY-YAML-MISSING:" + str(e), file=sys.stderr)
    sys.exit(3)
with open(sys.argv[1], encoding="utf-8") as f:
    doc = yaml.safe_load(f)
print(json.dumps(doc))
`;
  try {
    const out = execFileSync("python3", ["-c", script, absPath], {
      encoding: "utf-8",
      maxBuffer: 256 * 1024 * 1024,
    });
    return JSON.parse(out);
  } catch (e) {
    const stderr = e?.stderr?.toString?.() ?? String(e);
    if (stderr.includes("PY-YAML-MISSING")) {
      throw new Error(
        `python3 has no 'yaml' module, cannot parse ${absPath}. Install pyyaml for the checker host (no repo dependency added).`
      );
    }
    throw new Error(`failed to parse YAML ${absPath}: ${stderr.slice(0, 2000)}`);
  }
}

function loadDocs() {
  const docs = new Map(); // filename -> parsed doc
  for (const f of YAML_FILES) {
    docs.set(f, loadYamlViaPython(join(API_DIR, f)));
  }
  for (const f of JSON_FILES) {
    try {
      docs.set(f, JSON.parse(readFileSync(join(API_DIR, f), "utf-8")));
    } catch {
      // optional; ignore when absent/unparseable
    }
  }
  return docs;
}

// ------------------------------------------------------------ $ref resolve

function resolvePointer(root, pointer) {
  // pointer like "#/components/schemas/Foo" or "#/paths/~1v1~1x/..."
  if (!pointer.startsWith("#/")) throw new Error(`bad pointer ${pointer}`);
  const parts = pointer
    .slice(2)
    .split("/")
    .map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
  let cur = root;
  for (const p of parts) {
    if (cur == null || typeof cur !== "object" || !(p in cur)) {
      throw new Error(`pointer ${pointer} missing segment ${JSON.stringify(p)}`);
    }
    cur = cur[p];
  }
  return cur;
}

function resolveRef(ref, docs, fromFile, seen = []) {
  // Returns { schema, file } with refs chased (no infinite loops).
  let file = fromFile;
  let pointer = ref;
  const hash = ref.indexOf("#");
  if (hash > 0) {
    file = ref.slice(0, hash);
    pointer = ref.slice(hash);
  } else if (hash === -1) {
    throw new Error(`ref without #: ${ref}`);
  }
  // fromFile-relative external files live in API_DIR
  if (!docs.has(basename(file)) && !docs.has(file)) {
    throw new Error(`external ref file not loaded: ${ref}`);
  }
  const key = docs.has(file) ? file : basename(file);
  const root = docs.get(key);
  const target = resolvePointer(root, pointer);
  const id = `${key}:${pointer}`;
  if (seen.includes(id)) throw new Error(`circular $ref ${id}`);
  if (target && typeof target === "object" && typeof target.$ref === "string") {
    return resolveRef(target.$ref, docs, key, [...seen, id]);
  }
  return { schema: target, file: key };
}

// --------------------------------------------------------------- validator

function deepEqual(a, b) {
  return JSON.stringify(a) === JSON.stringify(b);
}

function jsType(v) {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  if (Number.isInteger(v)) return "integer";
  if (typeof v === "number") return "number";
  return typeof v; // string, boolean, object, undefined
}

function typeMatches(v, t) {
  const jt = jsType(v);
  if (t === "integer") return jt === "integer";
  if (t === "number") return jt === "integer" || jt === "number";
  return jt === t;
}

function validate(value, schema, docs, fromFile, path = "$", seen = []) {
  const errors = [];
  if (schema == null || typeof schema !== "object") return errors;
  if (typeof schema === "boolean") {
    if (!schema) errors.push(`${path}: false schema`);
    return errors;
  }
  if (typeof schema.$ref === "string") {
    const r = resolveRef(schema.$ref, docs, fromFile, seen);
    // merge sibling keywords (3.1 allows them); validate both
    const { $ref: _drop, ...siblings } = schema;
    const sub = validate(value, r.schema, docs, r.file, path, seen);
    errors.push(...sub);
    if (Object.keys(siblings).length) {
      errors.push(...validate(value, siblings, docs, fromFile, path, seen));
    }
    return errors;
  }

  // nullable (3.0 style, just in case)
  if (value === null && schema.nullable === true) return errors;

  if (schema.const !== undefined) {
    if (!deepEqual(value, schema.const)) {
      errors.push(`${path}: expected const ${JSON.stringify(schema.const)}`);
    }
  }
  if (Array.isArray(schema.enum)) {
    if (!schema.enum.some((e) => deepEqual(e, value))) {
      errors.push(`${path}: not in enum ${JSON.stringify(schema.enum)}`);
    }
  }
  if (schema.not && validate(value, schema.not, docs, fromFile, path, seen).length === 0) {
    errors.push(`${path}: matched forbidden 'not' schema`);
  }
  if (Array.isArray(schema.allOf)) {
    for (let i = 0; i < schema.allOf.length; i++) {
      errors.push(...validate(value, schema.allOf[i], docs, fromFile, `${path}⊕allOf[${i}]`, seen));
    }
  }
  if (Array.isArray(schema.anyOf)) {
    const ok = schema.anyOf.some(
      (s) => validate(value, s, docs, fromFile, path, seen).length === 0
    );
    if (!ok) errors.push(`${path}: matched no anyOf branch`);
  }
  if (Array.isArray(schema.oneOf)) {
    const passes = schema.oneOf.filter(
      (s) => validate(value, s, docs, fromFile, path, seen).length === 0
    ).length;
    if (passes < 1) errors.push(`${path}: matched no oneOf branch`);
  }

  const declared = schema.type;
  if (declared !== undefined) {
    const types = Array.isArray(declared) ? declared : [declared];
    if (!types.some((t) => typeMatches(value, t))) {
      errors.push(
        `${path}: expected type ${JSON.stringify(declared)} but got ${jsType(value)}`
      );
      return errors; // further keyword checks would be noise
    }
    if (value === null) return errors;
  }

  // strings
  if (typeof value === "string") {
    if (schema.minLength !== undefined && value.length < schema.minLength) {
      errors.push(`${path}: shorter than minLength ${schema.minLength}`);
    }
    if (schema.maxLength !== undefined && value.length > schema.maxLength) {
      errors.push(`${path}: longer than maxLength ${schema.maxLength}`);
    }
    if (typeof schema.pattern === "string") {
      let re;
      try {
        re = new RegExp(schema.pattern);
      } catch {
        re = null;
      }
      if (re && !re.test(value)) errors.push(`${path}: does not match pattern ${schema.pattern}`);
    }
  }

  // numbers
  if (typeof value === "number") {
    if (schema.minimum !== undefined && value < schema.minimum) {
      errors.push(`${path}: below minimum ${schema.minimum}`);
    }
    if (schema.maximum !== undefined && value > schema.maximum) {
      errors.push(`${path}: above maximum ${schema.maximum}`);
    }
    if (typeof schema.exclusiveMinimum === "number" && value <= schema.exclusiveMinimum) {
      errors.push(`${path}: below exclusiveMinimum ${schema.exclusiveMinimum}`);
    }
    if (typeof schema.exclusiveMaximum === "number" && value >= schema.exclusiveMaximum) {
      errors.push(`${path}: above exclusiveMaximum ${schema.exclusiveMaximum}`);
    }
    if (schema.exclusiveMinimum === true && schema.minimum !== undefined && value <= schema.minimum) {
      errors.push(`${path}: below exclusive minimum ${schema.minimum}`);
    }
    if (schema.exclusiveMaximum === true && schema.maximum !== undefined && value >= schema.maximum) {
      errors.push(`${path}: above exclusive maximum ${schema.maximum}`);
    }
    if (schema.multipleOf !== undefined) {
      const q = value / schema.multipleOf;
      if (!Number.isInteger(Math.round(q * 1e9) / 1e9) || Math.abs(q - Math.round(q)) > 1e-9) {
        errors.push(`${path}: not a multiple of ${schema.multipleOf}`);
      }
    }
  }

  // arrays
  if (Array.isArray(value)) {
    if (schema.minItems !== undefined && value.length < schema.minItems) {
      errors.push(`${path}: fewer than minItems ${schema.minItems}`);
    }
    if (schema.maxItems !== undefined && value.length > schema.maxItems) {
      errors.push(`${path}: more than maxItems ${schema.maxItems}`);
    }
    if (schema.uniqueItems === true) {
      const seenVals = new Set(value.map((v) => JSON.stringify(v)));
      if (seenVals.size !== value.length) errors.push(`${path}: uniqueItems violated`);
    }
    const items = schema.items ?? schema.prefixItems;
    if (Array.isArray(items)) {
      for (let i = 0; i < Math.min(items.length, value.length); i++) {
        errors.push(...validate(value[i], items[i], docs, fromFile, `${path}[${i}]`, seen));
      }
      if (schema.additionalItems === false && value.length > items.length) {
        errors.push(`${path}: additionalItems not allowed`);
      }
    } else if (items && typeof items === "object") {
      for (let i = 0; i < value.length; i++) {
        errors.push(...validate(value[i], items, docs, fromFile, `${path}[${i}]`, seen));
      }
    }
  }

  // objects
  if (value !== null && typeof value === "object" && !Array.isArray(value)) {
    const keys = Object.keys(value);
    if (schema.minProperties !== undefined && keys.length < schema.minProperties) {
      errors.push(`${path}: fewer than minProperties ${schema.minProperties}`);
    }
    if (schema.maxProperties !== undefined && keys.length > schema.maxProperties) {
      errors.push(`${path}: more than maxProperties ${schema.maxProperties}`);
    }
    if (Array.isArray(schema.required)) {
      for (const k of schema.required) {
        if (!(k in value)) errors.push(`${path}: missing required ${JSON.stringify(k)}`);
      }
    }
    const props = schema.properties ?? {};
    for (const [k, sub] of Object.entries(props)) {
      if (k in value) {
        errors.push(...validate(value[k], sub, docs, fromFile, `${path}.${k}`, seen));
      }
    }
    const ap = schema.additionalProperties;
    const declaredProps = new Set(Object.keys(props));
    for (const k of keys) {
      if (declaredProps.has(k)) continue;
      if (ap === false) {
        errors.push(`${path}: unexpected property ${JSON.stringify(k)}`);
      } else if (ap && typeof ap === "object") {
        errors.push(...validate(value[k], ap, docs, fromFile, `${path}.${k}`, seen));
      }
    }
  }

  return errors;
}

// -------------------------------------------------------- operation lookup

function findOperation(docs, file, operationId) {
  const doc = docs.get(file);
  if (!doc) return null;
  for (const [p, ops] of Object.entries(doc.paths ?? {})) {
    for (const [m, op] of Object.entries(ops ?? {})) {
      if (op && typeof op === "object" && op.operationId === operationId) {
        return { path: p, method: m, op };
      }
    }
  }
  return null;
}

function contentSchemaFor(docs, file, path, method, status, contentType) {
  const doc = docs.get(file);
  const op = doc?.paths?.[path]?.[method];
  if (!op) throw new Error(`no ${method.toUpperCase()} ${path} in ${file}`);
  const useStatus = String(status) in (op.responses ?? {}) ? String(status) : "default";
  let resp = op.responses?.[String(status)] ?? op.responses?.[useStatus];
  if (!resp) throw new Error(`no response ${status} for ${method.toUpperCase()} ${path} in ${file}`);
  if (resp.$ref) resp = resolveRef(resp.$ref, docs, file).schema;
  const content = resp.content ?? {};
  const ct = contentType && content[contentType] ? contentType : Object.keys(content)[0];
  if (!ct) throw new Error(`response ${status} for ${method.toUpperCase()} ${path} has no content`);
  const media = content[ct];
  if (!media?.schema) throw new Error(`response ${status} ${ct} has no schema`);
  return { schema: media.schema, contentType: ct };
}

function requestSchemaFor(docs, file, path, method) {
  const doc = docs.get(file);
  const op = doc?.paths?.[path]?.[method];
  if (!op) throw new Error(`no ${method.toUpperCase()} ${path} in ${file}`);
  const body = op.requestBody;
  if (!body) throw new Error(`${method.toUpperCase()} ${path} has no requestBody`);
  const resolved = body.$ref ? resolveRef(body.$ref, docs, file).schema : body;
  const content = resolved.content ?? {};
  const ct = content["application/json"] ? "application/json" : Object.keys(content)[0];
  if (!ct) throw new Error(`requestBody for ${method.toUpperCase()} ${path} has no content`);
  if (!content[ct]?.schema) throw new Error(`requestBody ${ct} has no schema`);
  return { schema: content[ct].schema, contentType: ct };
}

// ------------------------------------------------------------- extraction

const MARKER_RE =
  /<!--\s*example:\s*(\S+)\s+file=(\S+)\s+operationId=(\S+)\s+direction=(request|response)(?:\s+status=(\S+))?(?:\s+contentType=(\S+))?\s+schema=(.+?)\s*-->/g;
const FENCE_RE = /```json\s*\n([\s\S]*?)\n```/g;

function parseMarkerArgs(m) {
  return {
    id: m[1],
    file: m[2],
    operationId: m[3],
    direction: m[4],
    status: m[5],
    contentType: m[6],
    schema: m[7].trim(),
  };
}

function extractExamples(md, mdFile) {
  const out = [];
  let marker;
  // find markers, then the next ```json fence after each marker
  const markers = [];
  MARKER_RE.lastIndex = 0;
  while ((marker = MARKER_RE.exec(md)) !== null) {
    markers.push({ ...parseMarkerArgs(marker), index: marker.index });
  }
  for (const mk of markers) {
    FENCE_RE.lastIndex = mk.index;
    const fence = FENCE_RE.exec(md);
    if (!fence || fence.index < mk.index) {
      throw new Error(`${mdFile}: no \`\`\`json block after example ${mk.id}`);
    }
    // guard: no second marker between this marker and its fence
    const between = md.slice(mk.index, fence.index);
    if (/<!--\s*example:/.test(between.slice(between.indexOf("-->") + 3))) {
      throw new Error(`${mdFile}: example ${mk.id} marker not followed by its json block`);
    }
    out.push({ ...mk, jsonText: fence[1], mdFile });
  }
  return out;
}

// --------------------------------------------------- endpoint mention check

const MENTION_RE = /\b(GET|POST|PUT|PATCH|DELETE|HEAD)\s+(\/v\d[^\s"'`)\]]*)/g;

function collectMentions(md) {
  const out = [];
  MENTION_RE.lastIndex = 0;
  let m;
  while ((m = MENTION_RE.exec(md)) !== null) {
    let p = m[2].replace(/[.,;:]+$/, "");
    // strip query string for lookup
    p = p.split("?")[0];
    // skip bare namespace mentions (/v1, /v2)
    if (/^\/v\d$/.test(p)) continue;
    out.push({ method: m[1].toLowerCase(), path: p });
  }
  return out;
}

function buildRouteIndex(docs) {
  const index = new Map(); // "method path" -> Set(files)
  for (const [file, doc] of docs) {
    for (const [p, ops] of Object.entries(doc.paths ?? {})) {
      for (const m of Object.keys(ops ?? {})) {
        if (m.startsWith("x-")) continue;
        if (!["get", "post", "put", "patch", "delete", "head", "options"].includes(m)) continue;
        const key = `${m} ${p}`;
        if (!index.has(key)) index.set(key, new Set());
        index.get(key).add(file);
      }
    }
  }
  return index;
}

// ------------------------------------------------------------------- main

function main() {
  const mdFiles = readdirSync(GUIDE_DIR)
    .filter((f) => f.endsWith(".md"))
    .sort();
  if (!mdFiles.length) throw new Error(`no markdown files in ${GUIDE_DIR}`);

  const docs = loadDocs();
  const routeIndex = buildRouteIndex(docs);

  let examples = [];
  let combined = "";
  for (const f of mdFiles) {
    const text = readFileSync(join(GUIDE_DIR, f), "utf-8");
    combined += "\n" + text;
    examples.push(...extractExamples(text, f));
  }

  let failures = 0;

  // 1. citation existence + example validation
  for (const ex of examples) {
    const where = `${ex.mdFile}#${ex.id}`;
    if (!docs.has(ex.file)) {
      console.error(`FAIL ${where}: unknown OpenAPI file ${ex.file}`);
      failures++;
      continue;
    }
    const found = findOperation(docs, ex.file, ex.operationId);
    if (!found) {
      console.error(`FAIL ${where}: operationId ${ex.operationId} not in ${ex.file}`);
      failures++;
      continue;
    }
    let value;
    try {
      value = JSON.parse(ex.jsonText);
    } catch (e) {
      console.error(`FAIL ${where}: invalid JSON: ${e.message}`);
      failures++;
      continue;
    }
    let schema;
    try {
      if (ex.schema.startsWith("#/")) {
        schema = resolveRef(ex.schema, docs, ex.file).schema;
      } else if (ex.schema.startsWith("@request")) {
        const parts = ex.schema.split(/\s+/);
        // "@request <path> <method>"
        const r = requestSchemaFor(docs, ex.file, parts[1], parts[2].toLowerCase());
        schema = r.schema;
      } else if (ex.schema.startsWith("@response")) {
        const parts = ex.schema.split(/\s+/);
        // "@response <path> <method> <status>"
        const r = contentSchemaFor(
          docs,
          ex.file,
          parts[1],
          parts[2].toLowerCase(),
          parts[3] ?? ex.status ?? "200",
          ex.contentType
        );
        schema = r.schema;
      } else {
        throw new Error(`unsupported schema ref ${ex.schema}`);
      }
    } catch (e) {
      console.error(`FAIL ${where}: schema resolve error: ${e.message}`);
      failures++;
      continue;
    }
    const errs = validate(value, schema, docs, ex.file);
    if (errs.length) {
      console.error(`FAIL ${where}: ${errs.length} schema error(s) vs ${ex.schema}:`);
      for (const e of errs.slice(0, 12)) console.error(`  - ${e}`);
      failures++;
    } else {
      console.log(`ok ${where}: ${ex.operationId} ${ex.direction} matches ${ex.schema}`);
    }
  }

  // 2. every METHOD /vN mention must resolve
  const mentions = collectMentions(combined);
  const seenMention = new Set();
  for (const { method, path } of mentions) {
    const key = `${method} ${path}`;
    if (seenMention.has(key)) continue;
    seenMention.add(key);
    if (!routeIndex.has(key)) {
      console.error(`FAIL mention: ${method.toUpperCase()} ${path} matches no OpenAPI path+method`);
      failures++;
    }
  }
  console.log(
    `checked ${examples.length} examples and ${seenMention.size} distinct endpoint mentions across ${mdFiles.length} files.`
  );

  if (!examples.length) {
    console.error("FAIL: no examples found (markers missing?)");
    process.exit(1);
  }
  if (failures) {
    console.error(`${failures} failure(s).`);
    process.exit(1);
  }
  console.log("All guide examples validate against the OpenAPI schemas.");
}

main();
