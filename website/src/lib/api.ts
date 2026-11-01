import reference from '@/data/api/reference.json';

export type Schema = {
  type?: string | string[];
  properties?: Record<string, Schema>;
  required?: string[];
  items?: Schema;
  anyOf?: Schema[];
  oneOf?: Schema[];
  enum?: unknown[];
  format?: string;
  description?: string;
  additionalProperties?: boolean | Schema;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
  pattern?: string;
  const?: unknown;
  [key: string]: unknown;
};

export type ApiParameter = {
  name: string;
  in: 'path' | 'query' | 'header' | 'cookie';
  required: boolean;
  schema: Schema;
  description: string;
};

export type ApiOperation = {
  operationId: string;
  method: string;
  path: string;
  group: string;
  audience: 'Integrations' | 'Clients';
  protocol: 'native' | 'integration-compatibility';
  title: string;
  description: string;
  notes: string[];
  parameters: ApiParameter[];
  requestBody: { required?: boolean; content?: Record<string, { schema?: Schema }> } | null;
  responses: Record<string, { description?: string; content?: Record<string, { schema?: Schema }> }>;
  scope: string;
  access: string;
  errorCodes: string[];
  maxBodyBytes: number | null;
  auth?: string;
};

export type ApiGroup = { id: string; title: string; description: string };

export const apiInfo = reference.info;
export const apiGroups = reference.groups as ApiGroup[];
export const apiOperations = reference.operations as unknown as ApiOperation[];

export const operationsFor = (group: string) => apiOperations.filter((operation) => operation.group === group);
export const integrationOperations = apiOperations.filter((operation) => operation.audience === 'Integrations' && operation.protocol === 'native');
export const operationUrl = (operation: ApiOperation) => operation.audience === 'Integrations' && operation.protocol === 'native'
  ? `/developers/api/integrations/#${operation.operationId}`
  : `/developers/api/clients/${operation.group}/#${operation.operationId}`;

export const scopeDescriptions: Record<string, string> = {
  open: 'No credential needed.',
  read: 'Read libraries, titles, the guide, and the member’s own watch state, lists and notifications.',
  playback: 'Play and control playback, and change the member’s own watch state, lists, preferences and recordings.',
  library: 'Scan, report changed paths, match, edit and delete what is in the libraries.',
  admin: 'Everything else the member may administer. Includes read, playback and library.',
  session: 'API keys and personal access tokens are refused. Depending on the operation, send sign-in details, challenge or refresh data, a pairing code, or an existing session token.'
};

const variants = (schema: Schema) => schema.anyOf ?? schema.oneOf;

export const isNullable = (schema: Schema) =>
  (Array.isArray(schema.type) && schema.type.includes('null')) || Boolean(variants(schema)?.some((variant) => variant.type === 'null'));

// A short, readable type label such as "string", "integer ≥ 1", "array of object" or "string | null".
export const typeLabel = (schema: Schema | undefined): string => {
  if (!schema) return 'any';
  const options = variants(schema);
  if (options) {
    const labels = [...new Set(options.map((option) => typeLabel(option)))];
    const meaningful = labels.filter((label) => label !== 'any');
    return (meaningful.length > 0 ? meaningful : labels).join(' | ');
  }
  if (schema.const !== undefined) return JSON.stringify(schema.const);
  const types = Array.isArray(schema.type) ? schema.type : schema.type ? [schema.type] : [];
  const main = types.find((type) => type !== 'null') ?? (schema.properties ? 'object' : 'any');
  let label = main;
  if (main === 'array') label = `array of ${typeLabel(schema.items)}`;
  if (schema.format) label += ` (${schema.format})`;
  if (types.includes('null')) label += ' | null';
  return label;
};

export const constraints = (schema: Schema): string[] => {
  const out: string[] = [];
  if (schema.enum) out.push(`one of ${schema.enum.map((value) => JSON.stringify(value)).join(', ')}`);
  if (schema.minimum !== undefined && schema.maximum !== undefined) out.push(`${schema.minimum}–${schema.maximum}`);
  else if (schema.minimum !== undefined) out.push(`≥ ${schema.minimum}`);
  else if (schema.maximum !== undefined) out.push(`≤ ${schema.maximum}`);
  if (schema.minLength !== undefined || schema.maxLength !== undefined) {
    out.push(`length ${schema.minLength ?? 0}–${schema.maxLength ?? '∞'}`);
  }
  if (schema.minItems !== undefined || schema.maxItems !== undefined) {
    out.push(`${schema.minItems ?? 0}–${schema.maxItems ?? '∞'} items`);
  }
  if (schema.pattern) out.push(`pattern ${schema.pattern}`);
  return out;
};

// The object-like children of a schema, looking through arrays and anyOf wrappers.
export const childObject = (schema: Schema | undefined): Schema | undefined => {
  if (!schema) return undefined;
  if (schema.properties) return schema;
  if (schema.items) return childObject(schema.items);
  const options = variants(schema);
  if (options) return options.map(childObject).find(Boolean);
  if (schema.additionalProperties && typeof schema.additionalProperties === 'object') return childObject(schema.additionalProperties);
  return undefined;
};

const sampleString = (name: string, schema: Schema) => {
  const key = name.toLowerCase();
  if (schema.format === 'date-time' || key.endsWith('at') || key.includes('time')) return '2026-10-18T19:30:00Z';
  if (key === 'id' || key.endsWith('id')) return 'id_8h2k4q';
  if (key.includes('url') || key.includes('href')) return 'https://example.com/…';
  if (key.includes('cursor')) return 'eyJvIjoyMH0';
  if (key.includes('revision')) return 'r12';
  if (key.includes('name') || key.includes('title')) return 'Fargo';
  if (key.includes('email')) return 'sam@example.com';
  if (key.includes('language') || key === 'lang') return 'en';
  return 'string';
};

// A plausible example value for a schema, kept shallow so large responses stay readable.
export const exampleFor = (schema: Schema | undefined, name = '', depth = 0): unknown => {
  if (!schema) return null;
  if (schema.const !== undefined) return schema.const;
  if (schema.enum?.length) return schema.enum[0];
  const options = variants(schema);
  if (options) {
    const pick = options.find((option) => option.type !== 'null') ?? options[0];
    return exampleFor(pick, name, depth);
  }
  const types = Array.isArray(schema.type) ? schema.type : schema.type ? [schema.type] : [];
  const type = types.find((candidate) => candidate !== 'null') ?? (schema.properties ? 'object' : undefined);
  switch (type) {
    case 'object': {
      if (depth > 4) return {};
      const out: Record<string, unknown> = {};
      for (const [key, child] of Object.entries(schema.properties ?? {})) out[key] = exampleFor(child, key, depth + 1);
      if (!schema.properties && schema.additionalProperties && typeof schema.additionalProperties === 'object') {
        out.key = exampleFor(schema.additionalProperties, 'value', depth + 1);
      }
      return out;
    }
    case 'array':
      return depth > 4 ? [] : [exampleFor(schema.items, name, depth + 1)];
    case 'integer':
      if (/ms$/i.test(name)) return 1800000;
      return schema.minimum !== undefined && schema.minimum > 0 ? schema.minimum : 0;
    case 'number':
      return 0;
    case 'boolean':
      return true;
    case 'string':
      return sampleString(name, schema);
    default:
      return null;
  }
};

export const jsonExample = (schema: Schema | undefined) => JSON.stringify(exampleFor(schema), null, 2);

export const jsonSchemaOf = (content: Record<string, { schema?: Schema }> | undefined) => {
  if (!content) return { mediaType: undefined, schema: undefined };
  const mediaType = content['application/json'] ? 'application/json' : Object.keys(content)[0];
  return { mediaType, schema: content[mediaType]?.schema };
};

export const curlFor = (operation: ApiOperation) => {
  const path = operation.path.replace(/\{([^}]+)\}/g, (_, name) => `{${name}}`);
  const query = operation.parameters
    .filter((parameter) => parameter.in === 'query' && parameter.required)
    .map((parameter) => `${parameter.name}=…`)
    .join('&');
  const url = `http://127.0.0.1:32500${path}${query ? `?${query}` : ''}`;
  const lines = [`curl -X ${operation.method.toUpperCase()} "${url}"`];
  if (operation.scope === 'session' && operation.access !== 'public') lines.push('  -H "Authorization: Bearer $PORTICO_ACCESS_TOKEN"');
  else if (operation.scope !== 'open' && operation.access !== 'public') lines.push('  -H "Authorization: Bearer $PORTICO_API_KEY"');
  const { mediaType, schema } = jsonSchemaOf(operation.requestBody?.content);
  if (mediaType === 'application/json' && schema) {
    lines.push('  -H "Content-Type: application/json"');
    const body = JSON.stringify(exampleFor(schema));
    lines.push(`  -d '${body.length > 160 ? JSON.stringify(exampleFor(schema), null, 2).replace(/'/g, "'\\''") : body}'`);
  }
  return lines.join(' \\\n');
};
