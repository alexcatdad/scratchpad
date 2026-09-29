import { readFileSync } from "node:fs";

const records = readFileSync("decisions.jsonl", "utf8")
  .trim()
  .split("\n")
  .map((line) => JSON.parse(line));
const ids = new Set();
for (const record of records) {
  if (!record.id || ids.has(record.id) || !record.date || !record.authority)
    throw new Error(`Invalid decision: ${record.id}`);
  ids.add(record.id);
}
console.log(`Validated ${records.length} decision records.`);
