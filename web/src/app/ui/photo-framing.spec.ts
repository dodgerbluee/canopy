import { describe, expect, it } from 'vitest';
import { findPlantFocus, photoPosition } from './photo-framing';

function pixels(width: number, height: number) {
  const data = new Uint8ClampedArray(width * height * 4);
  for (let i = 0; i < data.length; i += 4) data.set([115, 110, 105, 255], i);
  return data;
}
function patch(
  data: Uint8ClampedArray,
  width: number,
  x0: number,
  y0: number,
  x1: number,
  y1: number,
  rgba = [65, 145, 40, 255],
) {
  for (let y = y0; y < y1; y++) for (let x = x0; x < x1; x++) data.set(rgba, (y * width + x) * 4);
}

describe('plant photo framing', () => {
  it('finds the upper canopy instead of the trunk or grass below it', () => {
    const data = pixels(96, 96);
    patch(data, 96, 30, 8, 66, 34);
    patch(data, 96, 0, 80, 96, 96);
    const focus = findPlantFocus(data, 96, 96);
    expect(focus.x).toBeGreaterThan(0.3);
    expect(focus.x).toBeLessThan(0.7);
    expect(focus.y).toBeLessThan(0.42);
    expect(focus.confidence).toBeGreaterThan(0);
  });

  it('responds to a canopy away from the center rather than always using one fixed crop', () => {
    const data = pixels(96, 96);
    patch(data, 96, 54, 18, 77, 40);
    const focus = findPlantFocus(data, 96, 96);
    expect(focus.x).toBeGreaterThan(0.55);
    expect(focus.y).toBeLessThan(0.5);
  });

  it('keeps a short plant in view when unrelated background foliage is higher', () => {
    const data = pixels(96, 96);
    patch(data, 96, 82, 0, 96, 40);
    patch(data, 96, 32, 50, 63, 72);
    const focus = findPlantFocus(data, 96, 96);
    expect(focus.x).toBeGreaterThan(0.35);
    expect(focus.x).toBeLessThan(0.65);
    expect(focus.y).toBeGreaterThan(0.5);
  });

  it('does not let a grassy lower background outweigh a sparse tree canopy', () => {
    const data = pixels(96, 96);
    patch(data, 96, 42, 8, 56, 18);
    patch(data, 96, 0, 46, 96, 96);
    const focus = findPlantFocus(data, 96, 96);
    expect(focus.y).toBeLessThan(0.3);
    expect(focus.confidence).toBeGreaterThan(0);
  });

  it('falls back safely when there is no foliage signal', () => {
    expect(findPlantFocus(pixels(32, 32), 32, 32)).toEqual({ x: 0.5, y: 0.28, confidence: 0 });
  });

  it('does not treat fully transparent green pixels as a subject', () => {
    const data = pixels(32, 32);
    patch(data, 32, 10, 0, 23, 12, [10, 210, 20, 0]);
    expect(findPlantFocus(data, 32, 32).confidence).toBe(0);
  });

  it('handles empty and malformed sampling data without throwing', () => {
    for (const [w, h] of [
      [0, 0],
      [96, 96],
      [-1, 12],
      [Number.NaN, 32],
    ]) {
      expect(findPlantFocus(new Uint8ClampedArray(), w, h)).toEqual({
        x: 0.5,
        y: 0.28,
        confidence: 0,
      });
    }
  });

  it('places a portrait canopy in a wide crop using the actual cover overflow', () => {
    const pos = photoPosition({ x: 0.5, y: 0.2, confidence: 1 }, 100, 400, 200, 100);
    const [x, y] = pos.split(' ').map(parseFloat);
    expect(x).toBe(50);
    expect(y).toBeCloseTo(15.714, 1);
    const lower = photoPosition({ x: 0.5, y: 0.8, confidence: 1 }, 100, 400, 200, 100);
    expect(parseFloat(lower.split(' ')[1])).toBeGreaterThan(70);
  });

  it('handles horizontal overflow for landscape originals', () => {
    const [x, y] = photoPosition({ x: 0.2, y: 0.5, confidence: 1 }, 400, 100, 100, 200)
      .split(' ')
      .map(parseFloat);
    expect(x).toBeCloseTo(15.714, 1);
    expect(y).toBe(50);
  });

  it('keeps an equal-ratio image centered because there is no crop', () => {
    expect(photoPosition({ x: 0.1, y: 0.2, confidence: 1 }, 100, 200, 200, 400)).toBe('50% 50%');
  });

  it('clamps edge focus and guards unavailable layout dimensions', () => {
    expect(photoPosition({ x: 0.5, y: 0, confidence: 1 }, 100, 400, 200, 100)).toBe('50% 0%');
    expect(photoPosition({ x: 0.5, y: 1, confidence: 1 }, 100, 400, 200, 100)).toBe('50% 100%');
    expect(photoPosition({ x: 0.5, y: 0.2, confidence: 1 }, 100, 400, 0, 0)).toBe('50% 28%');
  });
});
