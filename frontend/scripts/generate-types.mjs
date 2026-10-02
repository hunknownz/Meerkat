// Generates TS types and a standalone (no eval at runtime) Ajv validator from
// ../contracts/workflow.schema.json, which is the single authority for the snapshot contract.
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { compile } from 'json-schema-to-typescript';
import Ajv from 'ajv';
import standaloneCode from 'ajv/dist/standalone/index.js';

const root = new URL('../', import.meta.url);
const schema = JSON.parse(await readFile(new URL('../contracts/workflow.schema.json', root), 'utf8'));
const banner = '/* Generated from contracts/workflow.schema.json by scripts/generate-types.mjs. Do not edit. */';
await mkdir(new URL('src/generated/', root), { recursive: true });
const ts = await compile(schema, 'WorkflowEnvelope', {
  additionalProperties: false, unreachableDefinitions: true, bannerComment: banner, style: { singleQuote: true },
});
await writeFile(new URL('src/generated/workflow.ts', root), ts);

const ajv = new Ajv({ allErrors: false, strict: true, strictRequired: false, allowUnionTypes: true, code: { source: true, esm: true } });
ajv.addSchema(schema);
const id = schema.$id;
const code = standaloneCode(ajv, { validateEnvelope: id, validateSettingsInput: `${id}#/definitions/SettingsInput` });
// Ajv emits require() for its runtime helpers even in ESM mode; rewrite to static imports for bundling.
const helpers = new Map();
const esm = code.replace(/require\("(ajv\/dist\/runtime\/[a-z0-9]+)"\)\.default/g, (_m, mod) => {
  if (!helpers.has(mod)) helpers.set(mod, `ajvRuntime${helpers.size}`);
  return helpers.get(mod);
});
if (/require\(/.test(esm)) throw new Error('unexpected require() in generated validator');
// Inline the only helper Ajv needs (minLength/maxLength: count code points) so the bundle has no CJS interop.
const known = { 'ajv/dist/runtime/ucs2length': (name) => `function ${name}(s) { let n = 0; for (const _ of s) n++; return n; }` };
const imports = [...helpers].map(([mod, name]) => {
  if (!known[mod]) throw new Error(`unsupported Ajv runtime helper ${mod}`);
  return known[mod](name);
}).join('\n');
await writeFile(new URL('src/generated/validate.js', root), `${banner}\n// @ts-nocheck\n${imports}\n${esm}\n`);
await writeFile(new URL('src/generated/validate.d.ts', root), `${banner}\nexport interface Validator { (data: unknown): boolean; errors?: { instancePath: string; message?: string }[] | null }\nexport declare const validateEnvelope: Validator;\nexport declare const validateSettingsInput: Validator;\n`);
