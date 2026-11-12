// T01.r1 contract validation gate: SDL must build+validate, every operation must
// validate against the schema, every union selection must be exhaustive for the
// Journal union, and every fixture must be valid JSON.
import { readFileSync, readdirSync } from 'node:fs';
import {
  buildSchema,
  parse,
  validate,
  specifiedRules,
  NoUnusedFragmentsRule,
} from 'graphql';

const dir = (d) =>
  readdirSync(d)
    .filter((f) => f.endsWith('.graphql'))
    .sort()
    .map((f) => `${d}/${f}`);

const sdl = dir('graphql')
  .map((f) => readFileSync(f, 'utf8'))
  .join('\n');

const schema = buildSchema(sdl);
console.log(`SDL: ${dir('graphql').length} files, schema built OK`);

let failures = 0;
// Validate all operation files together so shared fragments in fragments.graphql resolve.
const docs = [];
const allDefs = [];
for (const f of dir('operations')) {
  const doc = parse(readFileSync(f, 'utf8'));
  docs.push({ f, doc });
  allDefs.push(...doc.definitions);
}
const combined = { kind: 'Document', definitions: allDefs };
const errors = validate(schema, combined, [...specifiedRules, NoUnusedFragmentsRule]);
if (errors.length) {
  failures++;
  console.error(`FAIL operations (combined)`);
  for (const e of errors.slice(0, 20)) console.error(`  ${e.message}`);
} else {
  for (const { f, doc } of docs) {
    const ops = doc.definitions.filter((d) => d.kind === 'OperationDefinition');
    console.log(`OK   ${f} (${ops.length} operations)`);
  }
}

// Journal union must be fully covered by the Journal operation.
const journalDoc = docs.find(({ f }) => f.includes('journal'));
const journalOp = journalDoc.doc.definitions.find(
  (d) => d.kind === 'OperationDefinition' && d.name?.value === 'Journal',
);
const spreadTypes = journalOp.selectionSet.selections
  .find((s) => s.name.value === 'journal')
  .selectionSet.selections.find((s) => s.name.value === 'items')
  .selectionSet.selections.filter((s) => s.kind === 'InlineFragment')
  .map((s) => s.typeCondition.name.value)
  .sort();
const unionMembers = schema
  .getType('JournalEntry')
  .getTypes()
  .map((t) => t.name)
  .sort();
if (JSON.stringify(spreadTypes) !== JSON.stringify(unionMembers)) {
  failures++;
  console.error(`FAIL Journal union coverage: doc=${spreadTypes} schema=${unionMembers}`);
} else {
  console.log(`OK   Journal union exhaustive: ${unionMembers.join(', ')}`);
}

for (const f of readdirSync('fixtures/api').filter((x) => x.endsWith('.json')).sort()) {
  try {
    JSON.parse(readFileSync(`fixtures/api/${f}`, 'utf8'));
    console.log(`OK   fixtures/api/${f}`);
  } catch (e) {
    failures++;
    console.error(`FAIL fixtures/api/${f}: ${e.message}`);
  }
}

if (failures) {
  console.error(`VALIDATION FAILED: ${failures} file(s)`);
  process.exit(1);
}
console.log('ALL CONTRACT VALIDATION PASSED');
