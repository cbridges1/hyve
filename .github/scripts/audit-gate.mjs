// The npm-audit gate in ci.yml: fails when `npm audit --omit=dev --json`
// (the file given as the first argument) has a high/critical advisory with a
// fix available, unless that advisory is in .github/npm-audit-allowlist.txt.
//
// npm audit lists packages, not advisories - a package is flagged either for
// its own advisories or because it depends on a flagged package. A package
// passes here once every advisory behind it, directly or through its
// dependencies, is allowlisted.
import { readFileSync } from 'node:fs';

const [auditPath, allowlistPath = '.github/npm-audit-allowlist.txt'] =
  process.argv.slice(2);
const audit = JSON.parse(readFileSync(auditPath, 'utf8'));
const allowed = new Set(
  readFileSync(allowlistPath, 'utf8')
    .split('\n')
    .map((line) => line.replace(/#.*/, '').trim())
    .filter(Boolean),
);
const vulnerabilities = audit.vulnerabilities ?? {};

const ghsa = (url) => url?.match(/GHSA-[\w-]+/)?.[0];

// The advisories behind a package, following "depends on a flagged
// package" links (which can be circular).
function advisories(name, seen = new Set()) {
  if (seen.has(name) || !vulnerabilities[name]) return [];
  seen.add(name);
  return vulnerabilities[name].via.flatMap((via) =>
    typeof via === 'string'
      ? advisories(via, seen)
      : [ghsa(via.url) ?? via.url],
  );
}

let failures = 0;
for (const [name, vulnerability] of Object.entries(vulnerabilities)) {
  if (
    vulnerability.severity !== 'high' &&
    vulnerability.severity !== 'critical'
  )
    continue;
  const ids = [...new Set(advisories(name))];
  const open = ids.filter((id) => !allowed.has(id));
  const fixable = vulnerability.fixAvailable !== false;
  const status = !open.length
    ? 'allowlisted'
    : fixable
      ? 'FIX AVAILABLE'
      : 'no fix';
  if (open.length && fixable) failures++;
  console.log(
    `${vulnerability.severity}\t${name}\t${status}\t${ids.join(' ')}`,
  );
}

if (failures) {
  console.log(
    `::error::${failures} high/critical production advisories have a fix available (see npm audit output above).`,
  );
  process.exit(1);
}
