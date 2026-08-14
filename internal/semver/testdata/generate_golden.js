const semver = require('semver');
const fs = require('fs');

const cases = JSON.parse(fs.readFileSync('corpus.json', 'utf8'));

// Real dependency ranges are overwhelmingly caret ranges, so they exercise almost none of the
// grammar. These cover the corners where implementations actually diverge: prerelease admission,
// the -0 upper bound, x-ranges, hyphen ranges, unions, and zero-major caret behaviour.
const synthetic = [
  '^1.2.3', '~1.2.3', '^0.2.3', '^0.0.3', '^0.0.x', '^0.x', '^1.x', '~1.2', '~1', '1.2.3',
  '=1.2.3', 'v1.2.3', '1.2', '1', '*', 'x', '', '>=1.2.3', '>1.2.3', '<=1.2.3', '<1.2.3',
  '>1.2', '<1.2', '>=1.2', '<=1.2', '1.2.3 - 2.3.4', '1.2 - 2.3', '1 - 2',
  '^1.2.3 || ~2.0.0', '>=1.0.0 <2.0.0', '>=1.0.0 <2.0.0-0',
  '^1.2.3-alpha', '>=1.2.3-alpha', '1.2.3-alpha', '~1.2.3-beta.2', '^0.0.0',
  '>=1.2.3-alpha <2.0.0', '^2.0.0-rc.1', '1.x.x', '1.2.x', '>=0.0.0', '<0.0.0',
];
const synthVersions = [
  '0.0.1', '0.0.2', '0.0.3', '0.0.4', '0.1.0', '0.2.0', '0.2.3', '0.2.4', '0.3.0',
  '1.0.0', '1.2.2', '1.2.3', '1.2.4', '1.3.0', '1.9.9', '2.0.0', '2.3.4', '2.4.0', '3.0.0',
  '1.2.3-alpha', '1.2.3-alpha.1', '1.2.3-beta', '1.2.3-beta.2', '1.2.3-beta.10', '1.2.3-rc.1',
  '2.0.0-alpha', '2.0.0-rc.1', '2.0.0-0', '1.3.0-0', '0.0.4-0', '1.2.4-alpha',
];
for (const r of synthetic) cases.push({ pkg: '(synthetic)', range: r, versions: synthVersions });

const out = [];
let skipped = 0;
for (const c of cases) {
  if (semver.validRange(c.range) === null) { skipped++; continue; }
  const sat = {};
  for (const v of c.versions) {
    if (!semver.valid(v)) continue;
    sat[v] = semver.satisfies(v, c.range);
  }
  const valid = c.versions.filter(v => semver.valid(v));
  out.push({
    pkg: c.pkg,
    range: c.range,
    satisfies: sat,
    maxSatisfying: semver.maxSatisfying(valid, c.range),
  });
}
fs.writeFileSync('golden.json', JSON.stringify(out));
const pairs = out.reduce((n, c) => n + Object.keys(c.satisfies).length, 0);
console.log(`oracle: semver ${require('semver/package.json').version}`);
console.log(`cases ${out.length}, pairs ${pairs}, ranges rejected as invalid by npm ${skipped}`);
