import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { HeroRotation } from './hero-rotation';

describe('Today hero rotation', () => {
  beforeEach(() => sessionStorage.clear());
  afterEach(() => vi.restoreAllMocks());

  it('cycles every eligible plant before repeating, including non-citrus plants', () => {
    const rotation = new HeroRotation();
    expect(
      ['fig', 'lime', 'lemon'].map(() => rotation.pick('garden', ['fig', 'lime', 'lemon'])),
    ).toEqual(['fig', 'lime', 'lemon']);
    expect(rotation.pick('garden', ['fig', 'lime', 'lemon'])).toBe('fig');
  });

  it('continues across a service recreation or page reload in the same tab', () => {
    expect(new HeroRotation().pick('garden', ['fig', 'lime'])).toBe('fig');
    expect(new HeroRotation().pick('garden', ['fig', 'lime'])).toBe('lime');
  });

  it('keeps workspace histories separate and copes with a removed previous choice', () => {
    const rotation = new HeroRotation();
    expect(rotation.pick('one', ['fig', 'lime'])).toBe('fig');
    expect(rotation.pick('two', ['fig', 'lime'])).toBe('fig');
    expect(rotation.pick('one', ['lime', 'lemon'])).toBe('lime');
  });

  it('handles no covers, a single cover and duplicate/empty IDs', () => {
    const rotation = new HeroRotation();
    expect(rotation.pick('garden', [])).toBeNull();
    expect(sessionStorage.length).toBe(0);
    expect(rotation.pick('garden', ['', 'fig', 'fig', 'lime'])).toBe('fig');
    expect(rotation.pick('garden', ['', 'fig', 'fig', 'lime'])).toBe('lime');
    expect(rotation.pick('garden', ['fig'])).toBe('fig');
    expect(rotation.pick('garden', ['fig'])).toBe('fig');
  });

  it('continues rotating in memory when browser storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('unavailable');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('unavailable');
    });
    const rotation = new HeroRotation();
    expect(rotation.pick('garden', ['fig', 'lime'])).toBe('fig');
    expect(rotation.pick('garden', ['fig', 'lime'])).toBe('lime');
    expect(rotation.pick('garden', ['fig', 'lime'])).toBe('fig');
  });
});
