/**
 * Exact money handling for the purchases UI (T12). The server is
 * authoritative and speaks MinorUnits: nonnegative digit strings in minor
 * units. The UI mirrors that contract with BigInt integer arithmetic —
 * no float ever touches a total — and quotes exact amounts in every
 * reconciliation message.
 */

/** Server ceiling (money.MaxMinor, inclusive): 10^15 minor units. */
export const MINOR_MAX = '1000000000000000';

/** ISO 4217 minor-unit exponents for the currencies we surface; anything
 * else falls back to 2 (the common case) for display only — the server
 * validates against the real exponent on write. */
const EXPONENTS: Record<string, number> = {
  JPY: 0,
  KRW: 0,
  VND: 0,
  CLP: 0,
  ISK: 0,
  BHD: 3,
  KWD: 3,
  OMR: 3,
  TND: 3,
  JOD: 3,
  LYD: 3,
};

export function exponentOf(currency: string): number {
  return EXPONENTS[currency.toUpperCase()] ?? 2;
}

/** Normalize a digit string: strip leading zeros, keep "0". */
function norm(minor: string): string {
  const t = minor.replace(/^0+(?=\d)/, '');
  return t;
}

export function isMinorString(v: string): boolean {
  return /^\d+$/.test(v);
}

/**
 * Parse a user-typed major-unit amount ("45.00") into a minor-unit digit
 * string for the given currency. Rejects signs, separators and more
 * decimals than the currency exponent — the server would reject them
 * too, and a rounded guess would silently change the receipt.
 */
export function parseAmount(
  v: string,
  currency: string,
  opts: { required?: boolean } = {},
): { minor: string; error?: string } {
  const t = v.trim();
  if (t === '') {
    return opts.required === false ? { minor: '0' } : { minor: '', error: 'Enter an amount.' };
  }
  if (!/^\d+(\.\d+)?$/.test(t)) {
    return { minor: '', error: 'Enter a nonnegative amount without signs or separators (e.g. 45.00).' };
  }
  const exp = exponentOf(currency);
  const [whole, frac = ''] = t.split('.');
  if (frac.length > exp) {
    return {
      minor: '',
      error:
        exp === 0
          ? `${currency} has no minor unit — enter a whole amount.`
          : `${currency} amounts have ${exp} decimal places — ${t} has ${frac.length}.`,
    };
  }
  const minor = norm(`${whole}${frac.padEnd(exp, '0')}`.replace(/^$/, '0'));
  if (BigInt(minor) > BigInt(MINOR_MAX)) {
    return { minor: '', error: `Amount exceeds the largest accepted value (${fmtMinor(MINOR_MAX, currency)}).` };
  }
  return { minor };
}

/** Render minor units exactly: "10000" USD -> "100.00 USD";
 * "1237" JPY -> "1,237 JPY". Grouping only ever touches display. */
export function fmtMinor(minor: string, currency: string): string {
  const neg = minor.startsWith('-');
  const digits = neg ? minor.slice(1) : minor;
  if (!/^\d+$/.test(digits)) return `${minor} ${currency}`; // defensive: never mangle a server value
  const exp = exponentOf(currency);
  let intPart: string;
  let fracPart = '';
  if (exp === 0) {
    intPart = digits;
  } else if (digits.length <= exp) {
    intPart = '0';
    fracPart = digits.padStart(exp, '0');
  } else {
    intPart = digits.slice(0, digits.length - exp);
    fracPart = digits.slice(digits.length - exp);
  }
  const grouped = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  return `${neg ? '-' : ''}${grouped}${fracPart ? '.' + fracPart : ''} ${currency}`;
}

/** Inverse of parseAmount for prefilling inputs: "4500" USD -> "45.00",
 * "1237" JPY -> "1237". Exact string surgery, no float. */
export function toInput(minor: string, currency: string): string {
  if (!isMinorString(minor)) return '';
  const exp = exponentOf(currency);
  if (exp === 0) return norm(minor);
  if (minor.length <= exp) return `0.${minor.padStart(exp, '0')}`;
  return `${norm(minor.slice(0, minor.length - exp))}.${minor.slice(minor.length - exp)}`;
}

/** Sum of minor-unit digit strings (exact). */
export function addMinor(...xs: string[]): string {
  let sum = 0n;
  for (const x of xs) if (isMinorString(x)) sum += BigInt(x);
  return sum.toString();
}

/** a - b, may be negative (exact). */
export function subMinor(a: string, b: string): string {
  const x = isMinorString(a) ? BigInt(a) : 0n;
  const y = isMinorString(b) ? BigInt(b) : 0n;
  return (x - y).toString();
}

export function cmpMinor(a: string, b: string): -1 | 0 | 1 {
  const x = isMinorString(a) ? BigInt(a) : 0n;
  const y = isMinorString(b) ? BigInt(b) : 0n;
  return x < y ? -1 : x > y ? 1 : 0;
}

/** Signed difference for reconciliation copy: "-0.01" style, exact. */
export function fmtDiff(diff: string, currency: string): string {
  const abs = diff.startsWith('-') ? diff.slice(1) : diff;
  const body = fmtMinor(abs, currency);
  return diff.startsWith('-') ? `-${body}` : `+${body}`;
}
