// Small formatters that several views share. Largely without i18n: the units
// (s/min/h/d, k/M) are the same in both languages, the sentence around them the
// callers build over their own translation keys. The one exception stands at
// milliarde() and justifies itself there.

import i18n from "./i18n";

// fmtBytes brings a file size to the coarsest unit that still
// describes it — `812 B`, `14,2 kB`, `3,1 MB`. Used by the file browser of the
// workspace.
export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["kB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

// fmtUSD brings an amount to as many decimal digits as it still carries —
// `2.746 $`, `12,30 $`, `0,0042 $`. A single run often costs fractions of a
// cent; rounded to two digits it would read 0,00 $ everywhere. Used by the cost
// page and by the backlog (cost on the task card).
//
// From a thousand with thousands separators, because that is exactly where they
// start to help: `2746 $` you read digit by digit, `2.746 $` at one glance.
export function fmtUSD(v: number): string {
  if (v >= 1000) return `${group(Math.round(v))} $`;
  if (v >= 1) return `${dezimal(v, 2)} $`;
  return `${dezimal(v, 4)} $`;
}

// dezimal: a fixed number of decimal digits, with the separator of the UI.
// Without it the German page would show `12.30 $` next to `2.847 $`, and the
// same point would mean two things in one line.
function dezimal(v: number, stellen: number): string {
  return v.toFixed(stellen).replace(".", komma());
}

// fmtCount brings a count to what a person takes in while skimming — `842`,
// `12,3 k`, `148 M`.
//
// The reason was a number from the cost view: 147952885 read cache tokens.
// Nobody reads that as a hundred forty-eight million, you count it through
// digit by digit. Meant for tokens, blocks, files — everything of which an
// instance has six-figure amounts.
//
// The exact number is not lost in the process: whoever needs it (checking a
// bill) finds it in the `title` next to it — that is what exact() is for.
export function fmtCount(n: number): string {
  const v = Math.round(n);
  if (v < 10_000) return group(v);
  if (v < 1_000_000) return `${trim(v / 1000)} k`;
  if (v < 1_000_000_000) return `${trim(v / 1_000_000)} M`;
  return `${trim(v / 1_000_000_000)} ${milliarde()}`;
}

// milliarde: the one character of this file that has to know the language.
//
// k and M are named the same in both languages — the billion is not: `Mrd`
// against `B`, and a German `2,5 B` would read as bytes. Hence the exception
// from the rule above, and only here. Without this step an agent with a grown
// cache would stand there with `2500 M`, and that you count through digit by
// digit again — exactly what fmtCount set out against.
function milliarde(): string {
  return deutsch() ? "Mrd" : "B";
}

// exact is the long form for the tooltip next to a shortened number.
export function exact(n: number): string {
  return group(Math.round(n));
}

// group sets thousands separators — in the spelling of the UI.
//
// The point stood here hard-coded once, with the same reasoning as above: a
// number that looks different depending on the browser language makes
// screenshots incomparable. For units that argument holds, for separators it
// does not: `1.234` is one thousand two hundred thirty-four in German and
// something over one in English. On the English UI `2.847 $` therefore stood for
// an amount of two thousand eight hundred — read as two dollars fifty-five.
function group(v: number): string {
  const neg = v < 0;
  const digits = Math.abs(v).toString();
  const sep = tausender();
  let out = "";
  for (let i = 0; i < digits.length; i++) {
    if (i > 0 && (digits.length - i) % 3 === 0) out += sep;
    out += digits[i];
  }
  return neg ? `-${out}` : out;
}

// trim shows one decimal digit as long as it says something: `12,3 k`, but
// `148 M` and not `148,0 M`.
function trim(v: number): string {
  const one = v.toFixed(1);
  const rounded = one.endsWith(".0") ? one.slice(0, -2) : one;
  return rounded.replace(".", komma());
}

// The two separators, and the one place where they are decided. They hang
// together: whoever sets the point as the thousands separator has to set the
// comma as the decimal separator, otherwise `1.234,5` against `1,234.5` makes a
// mishmash that is right in neither of the two languages.
function deutsch(): boolean {
  return i18n.language?.startsWith("de") ?? true;
}

function tausender(): string {
  return deutsch() ? "." : ",";
}

function komma(): string {
  return deutsch() ? "," : ".";
}

// fmtDelta brings a span of time in milliseconds to the coarsest unit that
// still describes it — `42 s`, `3 min`, `2 h 15 min`, `4 d`. Used by the
// Heartbeat (next/last run) and by the Activity-Feed (duration of a sub-run).
export function fmtDelta(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`;
}
