// Regenerate only after deliberately changing the exact dependency version.
// The installed package README is the grouping authority; its public metadata
// supplies the supported IDs. Never scrape a moving website at application run.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const api = require('dukascopy-node');
const pkg = require('dukascopy-node/package.json');
const readme = fs.readFileSync(require.resolve('dukascopy-node/README.md'), 'utf8');
const groups = [];
const seen = new Set();
for (const line of readme.split('\n')) {
  const heading = line.match(/^<h3 id="[^"]+">(.+)<\/h3>$/);
  if (heading) groups.push({ name: heading[1].trim(), instruments: [] });
  const row = line.match(/^\|\[(.+)\]\(https:\/\/www\.dukascopy-node\.app\/instrument\/([^)]*)\)\|`([^`]+)`\|([^|]+)\|$/);
  if (!row) continue;
  const [, name, urlID, id, rawDate] = row;
  assert.equal(urlID, id);
  assert.ok(groups.length && api.instrumentMetaData[id], `Unsupported README instrument ${id}`);
  assert.ok(!seen.has(id), `Duplicate instrument ${id}`);
  const date = new Date(`${rawDate} 00:00:00 GMT`);
  assert.ok(Number.isFinite(+date), `Invalid earliest date for ${id}`);
  seen.add(id);
  groups.at(-1).instruments.push({ id, name, earliestDate: date.toISOString().slice(0, 10) });
}
assert.deepEqual([...seen].sort(), Object.keys(api.instrumentMetaData).sort());
assert.ok(groups.every(group => group.instruments.length));
const result = { version: pkg.version, groups, timeframes: Object.values(api.Timeframe) };
const destination = path.join(__dirname, '../internal/screener/catalog.json');
const output = `${JSON.stringify(result, null, 2)}\n`;
if (process.argv.includes('--check')) {
  assert.equal(fs.readFileSync(destination, 'utf8'), output, 'Embedded catalog differs from the pinned installed package; regenerate it deliberately.');
} else {
  fs.writeFileSync(destination, output);
}
console.log(`${pkg.version}: ${seen.size} instruments in ${groups.length} groups`);
