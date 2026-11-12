import { describe, expect, it } from 'vitest';
import {
  MINOR_MAX,
  addMinor,
  cmpMinor,
  exponentOf,
  fmtDiff,
  fmtMinor,
  parseAmount,
  subMinor,
} from './money';

/** MONEY UI boundary values (T12 verify line): parsing, formatting and
 * arithmetic stay exact digit strings end to end — no float rounding. */
describe('money (exact minor-unit helpers)', () => {
  it('parses major-unit input into minor units per currency exponent', () => {
    expect(parseAmount('45.00', 'USD').minor).toBe('4500');
    expect(parseAmount('45', 'USD').minor).toBe('4500');
    expect(parseAmount('0.05', 'USD').minor).toBe('5');
    expect(parseAmount('0', 'USD').minor).toBe('0');
    expect(parseAmount('1237', 'JPY').minor).toBe('1237');
    expect(parseAmount('1.250', 'KWD').minor).toBe('1250');
    expect(parseAmount(' 45.00 ', 'USD').minor).toBe('4500');
  });

  it('rejects more decimals than the currency exponent — never rounds', () => {
    const r = parseAmount('44.999', 'USD');
    expect(r.minor).toBe('');
    expect(r.error).toContain('2 decimal places');
    expect(parseAmount('10.5', 'JPY').error).toContain('no minor unit');
  });

  it('rejects signs, separators and junk', () => {
    for (const bad of ['-5', '+5', '1,000', '1e3', '5.', '.5', '45.0 0', 'abc']) {
      expect(parseAmount(bad, 'USD').error, bad).toBeTruthy();
    }
  });

  it('honours the server ceiling exactly (1e15 inclusive)', () => {
    expect(parseAmount('10000000000000', 'USD').minor).toBe(MINOR_MAX); // 1e13 major = 1e15 minor
    const over = parseAmount('10000000000000.01', 'USD');
    expect(over.minor).toBe('');
    expect(over.error).toContain('largest accepted value');
  });

  it('formats minor units exactly with grouping and exponent', () => {
    expect(fmtMinor('10000', 'USD')).toBe('100.00 USD');
    expect(fmtMinor('5000', 'USD')).toBe('50.00 USD');
    expect(fmtMinor('5', 'USD')).toBe('0.05 USD');
    expect(fmtMinor('0', 'USD')).toBe('0.00 USD');
    expect(fmtMinor('1237', 'JPY')).toBe('1,237 JPY');
    expect(fmtMinor('1234567890123', 'USD')).toBe('12,345,678,901.23 USD');
    expect(fmtMinor(MINOR_MAX, 'USD')).toBe('10,000,000,000,000.00 USD');
  });

  it('adds and subtracts without float drift (0.1+0.2 problem)', () => {
    expect(addMinor('10', '20')).toBe('30');
    expect(addMinor('4500', '4500', '500', '500')).toBe('10000');
    expect(subMinor('4500', '4499')).toBe('1');
    expect(subMinor('4499', '4500')).toBe('-1');
    expect(addMinor('9007199254740993', '0')).toBe('9007199254740993'); // beyond Number.MAX_SAFE_INTEGER
    expect(subMinor('9007199254740993', '1')).toBe('9007199254740992');
  });

  it('compares exactly', () => {
    expect(cmpMinor('4500', '4500')).toBe(0);
    expect(cmpMinor('4499', '4500')).toBe(-1);
    expect(cmpMinor('1000000000000000000', '999999999999999999')).toBe(1);
  });

  it('quotes exact signed discrepancies for reconciliation copy', () => {
    expect(fmtDiff('-1', 'USD')).toBe('-0.01 USD');
    expect(fmtDiff('1', 'USD')).toBe('+0.01 USD');
    expect(fmtDiff('-150', 'USD')).toBe('-1.50 USD');
    expect(fmtDiff('-150', 'JPY')).toBe('-150 JPY');
  });

  it('falls back to exponent 2 for unknown codes (display only)', () => {
    expect(exponentOf('XTS')).toBe(2);
    expect(fmtMinor('250', 'xts')).toBe('2.50 xts');
  });
});
