import { fmtMinor } from '../purchases/money';

/** Translate explicit minor-unit amounts only; never guess a currency or
 * reinterpret ordinary model numbers, dates, or quantities. */
export function readableSuggestionText(value: string | null | undefined): string {
  return (value ?? '').replace(
    /(?<![\w.,+-])(\d+)\s+([A-Z]{3})\s+minor units\b/g,
    (_, minor, currency) => fmtMinor(minor, currency),
  );
}
