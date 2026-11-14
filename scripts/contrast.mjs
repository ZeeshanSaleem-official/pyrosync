import fs from "node:fs";
import path from "node:path";

const tokensPath = path.resolve(process.cwd(), "web/styles/tokens.css");
const css = fs.readFileSync(tokensPath, "utf8");

const tokens = {};
for (const match of css.matchAll(/--([a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{6})\s*;/g)) {
  tokens[match[1]] = match[2].toUpperCase();
}

function linearize(c) {
  const s = c / 255;
  return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

function luminance(hex) {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  return 0.2126 * linearize(r) + 0.7152 * linearize(g) + 0.0722 * linearize(b);
}

function contrastRatio(hexA, hexB) {
  const l1 = luminance(hexA);
  const l2 = luminance(hexB);
  const [hi, lo] = l1 >= l2 ? [l1, l2] : [l2, l1];
  return (hi + 0.05) / (lo + 0.05);
}

const checks = [
  ["text-1", "bg-canvas", 4.5],
  ["text-1", "bg-panel", 4.5],
  ["text-1", "bg-raised", 4.5],
  ["text-2", "bg-canvas", 4.5],
  ["text-2", "bg-panel", 4.5],
  ["text-2", "bg-raised", 4.5],
  ["text-3", "bg-canvas", 4.5],
  ["text-3", "bg-panel", 4.5],
  ["text-3", "bg-raised", 4.5],
  ["sel-fg", "sel-bg", 4.5],
  ["warn", "bg-panel", 4.5],
  ["error", "bg-panel", 4.5],
  ["focus", "bg-canvas", 3.0],
];

let failed = false;
for (const [fg, bg, minRatio] of checks) {
  if (!tokens[fg] || !tokens[bg]) {
    console.error(`Missing token: ${fg} or ${bg}`);
    failed = true;
    continue;
  }
  const ratio = contrastRatio(tokens[fg], tokens[bg]);
  const ok = ratio >= minRatio;
  console.log(`${ok ? "PASS" : "FAIL"}  ${fg} (${tokens[fg]}) on ${bg} (${tokens[bg]}): ${ratio.toFixed(2)}:1 (min ${minRatio}:1)`);
  if (!ok) failed = true;
}

if (failed) {
  process.exit(1);
}
