import { describe, expect, it } from 'vitest';
import { readableSuggestionText } from './suggestion-text';
describe('readable suggestion text', () => {
  it('renders explicit minor units with exact currency exponents', () => {
    expect(
      readableSuggestionText('13900 USD minor units; 1200 JPY minor units; 1234 KWD minor units'),
    ).toBe('139.00 USD; 1,200 JPY; 1.234 KWD');
  });
  it('preserves unknown quantities, ordinary numbers and dates', () => {
    expect(readableSuggestionText('Water 500 ML on 2026-11-08; 13900 USD.')).toBe(
      'Water 500 ML on 2026-11-08; 13900 USD.',
    );
    expect(readableSuggestionText('13900.5 USD minor units; -500 USD minor units')).toBe(
      '13900.5 USD minor units; -500 USD minor units',
    );
    expect(readableSuggestionText(null)).toBe('');
  });
});
