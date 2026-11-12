import { describe, expect, it } from 'vitest';
import { PlantsDocument } from '@canopy/contracts';

describe('contract wiring', () => {
  it('web consumes typed documents from the frozen contracts workspace', () => {
    expect(PlantsDocument.kind).toBe('Document');
  });
});
