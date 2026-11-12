/**
 * Pure, browser-free plant framing helpers (UI04).
 *
 * No Angular, DOM, data-layer or contract imports here: the detector works on
 * a small sampled RGBA buffer and the geometry helper works on plain numbers,
 * so both are testable without a browser and the presentation boundary stays
 * clean.
 *
 * Nothing here is AI and nothing is a per-image lookup table: the focus comes
 * from the sampled pixels of the image actually being displayed, and the crop
 * position is derived from real `object-fit: cover` geometry.
 */

/** Normalized subject focus inside the image: x/y in [0,1], origin top-left. */
export interface PlantFocus {
  x: number;
  y: number;
  /** 0..1 strength of the foliage signal that produced this focus. */
  confidence: number;
}

/** Largest sample edge the detector is willing to walk. */
export const MAX_SAMPLE_EDGE = 96;

/** Honest fallback: portrait upper-canopy, flagged as no real signal. */
export const FALLBACK_FOCUS: PlantFocus = { x: 0.5, y: 0.28, confidence: 0 };

/** Safe CSS value when the geometry inputs are unusable. */
export const FALLBACK_POSITION = '50% 28%';

const MIN_CHANNEL_SPREAD = 18; // below this the pixel is grey/black/white noise
const MIN_GREEN_EXCESS = 8; // green must actually beat red and blue
const MIN_GREEN_RATIO = 1.08; // and by a real margin, not by rounding
const MAX_LUMA = 236; // near-white highlights carry no colour information
const MIN_LUMA = 22; // near-black shadows likewise
const ALPHA_CUTOFF = 8; // fully/borderline transparent pixels are ignored
const MIN_WEIGHT = 1e-6; // effectively "no usable signal in this buffer"

/**
 * Bounded, content-aware foliage focus.
 *
 * The buffer is sampled down to at most 96x96 before scoring, so cost is
 * independent of the original photo size. Two passes: first each row's green
 * background baseline is estimated from its outer edges and subtracted, so a
 * lawn or background canopy spanning the frame cannot vote as subject; then a
 * stronger central-subject prior picks the densest vertical foliage band and
 * the focus is the weighted centroid inside that band, with only a very mild
 * upward bias. Short plants in lower-middle stay lower-middle; tall trees
 * focus high because their own canopy is what is dense there.
 *
 * Never throws: malformed dimensions, short or empty buffers, NaN pixels and
 * fully transparent buffers all return the fallback focus.
 */
export function findPlantFocus(
  pixels: Uint8ClampedArray,
  width: number,
  height: number,
): PlantFocus {
  if (!pixels || typeof pixels.length !== 'number') return { ...FALLBACK_FOCUS };
  if (!isFiniteInt(width) || !isFiniteInt(height) || width <= 0 || height <= 0) {
    return { ...FALLBACK_FOCUS };
  }
  // up-front buffer validation: a short buffer means the dimensions cannot be
  // trusted, so return the fallback instead of silently reading undefined
  if (pixels.length < width * height * 4) return { ...FALLBACK_FOCUS };

  const cols = Math.min(width, MAX_SAMPLE_EDGE);
  const rows = Math.min(height, MAX_SAMPLE_EDGE);
  if (cols <= 0 || rows <= 0) return { ...FALLBACK_FOCUS };

  // Pass 1: per-sample foliage weights, with each row's green background
  // baseline (estimated from its outer edges) subtracted so full-width
  // grass/background rows are suppressed before any centroid is formed.
  const weight = new Float64Array(cols * rows);
  const rowScore = new Float64Array(rows);
  for (let sy = 0; sy < rows; sy++) {
    const rowBase = sy * cols;
    const edge = Math.max(1, Math.round(cols * 0.18));
    let edgeSum = 0;
    let edgeCount = 0;
    for (let sx = 0; sx < cols; sx++) {
      const src = sampleIndex(sx, sy, cols, rows, width, height);
      const raw = foliageWeight(pixels, src);
      weight[rowBase + sx] = raw;
      if (sx < edge || sx >= cols - edge) {
        edgeSum += raw;
        edgeCount++;
      }
    }
    const baseline = edgeCount > 0 ? (edgeSum / edgeCount) * 0.95 : 0;
    // mild bottom background suppression; upper rows are never penalised
    const ny = (sy + 0.5) / rows;
    const vertical = ny > 0.78 ? 0.25 : ny > 0.62 ? 0.7 : 1;
    let score = 0;
    for (let sx = 0; sx < cols; sx++) {
      const nx = (sx + 0.5) / cols;
      const excess = weight[rowBase + sx] - baseline;
      const w = excess > 0 ? excess * vertical * centralPrior(nx) : 0;
      weight[rowBase + sx] = w;
      score += w;
    }
    rowScore[sy] = score;
  }

  // Pass 2: the densest vertical foliage band, found on smoothed row scores.
  // A broad background cannot drag the centroid across the whole frame; the
  // focus is the weighted centroid inside a neighbourhood of this peak, so a
  // short plant focuses at its own (possibly low) leaf cluster.
  const radius = Math.max(1, Math.round(rows * 0.1));
  let peak = 0;
  let peakScore = -1;
  for (let sy = 0; sy < rows; sy++) {
    let band = 0;
    for (let k = Math.max(0, sy - radius); k <= Math.min(rows - 1, sy + radius); k++) {
      band += rowScore[k];
    }
    if (band > peakScore) {
      peakScore = band;
      peak = sy;
    }
  }
  if (!(peakScore > 0)) return { ...FALLBACK_FOCUS };

  const span = Math.max(1, Math.round(rows * 0.18));
  const lo = Math.max(0, peak - span);
  const hi = Math.min(rows - 1, peak + span);

  let sumW = 0;
  let sumWX = 0;
  let sumWY = 0;
  let sumWYY = 0;
  let hits = 0;
  for (let sy = lo; sy <= hi; sy++) {
    const ny = (sy + 0.5) / rows;
    const rowBase = sy * cols;
    for (let sx = 0; sx < cols; sx++) {
      const w = weight[rowBase + sx];
      if (w <= 0) continue;
      hits++;
      const nx = (sx + 0.5) / cols;
      sumW += w;
      sumWX += w * nx;
      sumWY += w * ny;
      sumWYY += w * ny * ny;
    }
  }

  if (hits === 0 || !(sumW > MIN_WEIGHT)) return { ...FALLBACK_FOCUS };

  let x = sumWX / sumW;
  let y = sumWY / sumW;

  // Very mild upward bias, scaled by the vertical spread of the chosen band
  // only. Tall trees already focus high because their canopy is the dense
  // band; a small plant in lower-middle stays lower-middle.
  let spreadY = Math.sqrt(Math.max(0, sumWYY / sumW - (sumWY / sumW) ** 2));
  if (!Number.isFinite(spreadY)) spreadY = 0;
  spreadY = Math.min(0.5, Math.max(0, spreadY));
  y -= spreadY * 0.15;

  x = clamp01(x);
  y = clamp01(y);

  // Confidence is the share of usable samples that read as plant material,
  // tempered by how tightly they cluster. It is deliberately not part of the
  // crop geometry.
  const coverage = hits / (cols * rows);
  const tightness = 1 - Math.min(1, spreadY / 0.35);
  const confidence = clamp01(Math.sqrt(coverage) * 1.6 * (0.45 + 0.55 * tightness));

  return { x, y, confidence };
}

/**
 * CSS `object-position` for an `object-fit: cover` box, chosen so the plant
 * focus lands as close to the centre of the visible crop as the geometry
 * allows.
 *
 * Cover geometry: the rendered image is scaled by max(boxW/imgW, boxH/imgH),
 * so the visible fraction of each axis is box/rendered. The focus-centred crop
 * start is clamped into the image, and that start is expressed as a percentage
 * of the overflow — which is precisely what object-position percentages mean.
 * An axis with no overflow reports 50%.
 *
 * Invalid or zero dimensions return the safe fallback string. Confidence is
 * not consulted; it is a signal-quality number, not geometry.
 */
export function photoPosition(
  focus: PlantFocus,
  imageWidth: number,
  imageHeight: number,
  boxWidth: number,
  boxHeight: number,
): string {
  if (
    !isPositiveFinite(imageWidth) ||
    !isPositiveFinite(imageHeight) ||
    !isPositiveFinite(boxWidth) ||
    !isPositiveFinite(boxHeight)
  ) {
    return FALLBACK_POSITION;
  }

  const fx = focus && Number.isFinite(focus.x) ? clamp01(focus.x) : FALLBACK_FOCUS.x;
  const fy = focus && Number.isFinite(focus.y) ? clamp01(focus.y) : FALLBACK_FOCUS.y;

  const scale = Math.max(boxWidth / imageWidth, boxHeight / imageHeight);
  if (!Number.isFinite(scale) || scale <= 0) return FALLBACK_POSITION;

  const renderedW = imageWidth * scale;
  const renderedH = imageHeight * scale;
  const overflowX = renderedW - boxWidth;
  const overflowY = renderedH - boxHeight;

  const px = axisPosition(fx, overflowX, renderedW, boxWidth);
  const py = axisPosition(fy, overflowY, renderedH, boxHeight);
  return `${px}% ${py}%`;
}

/** Percentage of the overflow at which a focus-centred crop must start. */
function axisPosition(focus: number, overflow: number, rendered: number, box: number): number {
  if (overflow <= 0) return 50;
  const start = clamp(focus * rendered - box / 2, 0, overflow);
  return round2((start / overflow) * 100);
}

/** Weight of one RGBA pixel as plant material; 0 means "ignore this sample". */
function foliageWeight(pixels: Uint8ClampedArray, index: number): number {
  if (index < 0 || index + 3 >= pixels.length) return 0;
  const r = num(pixels[index]);
  const g = num(pixels[index + 1]);
  const b = num(pixels[index + 2]);
  const a = num(pixels[index + 3]);
  if (!Number.isFinite(r) || !Number.isFinite(g) || !Number.isFinite(b) || !Number.isFinite(a)) {
    return 0;
  }
  if (a < ALPHA_CUTOFF) return 0;

  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const spread = max - min;
  const luma = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  if (max <= MIN_LUMA || luma >= MAX_LUMA || spread < MIN_CHANNEL_SPREAD) return 0;

  const excess = g - Math.max(r, b);
  const ratio = g / (Math.max(r, b) + 1);
  let strength: number;
  if (excess >= MIN_GREEN_EXCESS && ratio >= MIN_GREEN_RATIO) {
    // clear leaf green
    strength = 1;
  } else if (g >= r && g > b && r > b && spread >= MIN_CHANNEL_SPREAD) {
    // yellow-green fruit / new growth: green and red both above blue
    strength = 0.6;
  } else {
    return 0;
  }

  const saturation = spread / Math.max(1, max);
  const alpha = Math.min(1, a / 255);
  return strength * (0.4 + 0.6 * saturation) * alpha;
}

/**
 * Central-subject prior: a bell around the frame centre. Strong enough that
 * background foliage at the edges loses to the subject, wide enough (sigma
 * 0.19) that a genuinely off-centre subject around x=0.65 still wins.
 */
function centralPrior(nx: number): number {
  const dx = nx - 0.5;
  return Math.exp(-0.5 * (dx / 0.19) ** 2);
}

/** Map a coarse sample coordinate back to the source RGBA buffer. */
function sampleIndex(
  sx: number,
  sy: number,
  cols: number,
  rows: number,
  width: number,
  height: number,
): number {
  const px = Math.min(width - 1, Math.floor(((sx + 0.5) * width) / cols));
  const py = Math.min(height - 1, Math.floor(((sy + 0.5) * height) / rows));
  return (py * width + px) * 4;
}

function num(value: number): number {
  return typeof value === 'number' ? value : Number.NaN;
}

function isFiniteInt(value: number): boolean {
  return Number.isInteger(value) && value > 0;
}

function isPositiveFinite(value: number): boolean {
  return typeof value === 'number' && Number.isFinite(value) && value > 0;
}

function clamp(value: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, value));
}

function clamp01(value: number): number {
  return clamp(value, 0, 1);
}

function round2(value: number): number {
  return Math.round(value * 100) / 100;
}
