/** Shared helpers for the collection feature (T10). Payload problems are
 * results, not failures: they map onto field errors and actionable banners. */

export interface PayloadProblem {
  code: string;
  field: string | null;
  message: string;
  currentRevision?: number | null;
}

/** Map `input.<field>` problem paths onto form controls. */
export function problemMap(problems: PayloadProblem[]): Record<string, string> {
  const map: Record<string, string> = {};
  for (const p of problems) {
    if (p.field?.startsWith('input.')) map[p.field.slice('input.'.length)] ??= p.message;
  }
  return map;
}

/** A stale write must tell the user what happened and how to recover. */
export function conflictNote(problems: PayloadProblem[], what: string): string {
  const c = problems.find((p) => p.code === 'CONFLICT');
  if (!c) return '';
  if (c.currentRevision != null) {
    return `${what} changed on another device (now revision ${c.currentRevision}). Nothing was saved — reload and try again.`;
  }
  return `${c.message} Nothing was saved.`;
}

/** Non-field problems (idempotency, capability) surface as a general notice. */
export function generalProblems(problems: PayloadProblem[]): string[] {
  return problems
    .filter((p) => !p.field?.startsWith('input.') && p.code !== 'CONFLICT')
    .map((p) => p.message);
}

/** Trim empty strings to null so "unknown" stays genuinely unknown. */
export function blankToNull(v: string): string | null {
  const t = v.trim();
  return t === '' ? null : t;
}

/** Parse an optional positive integer; undefined = valid-empty, string = error. */
export function parseCount(v: string): { value: number | null; error?: string } {
  const t = v.trim();
  if (t === '') return { value: null };
  const n = Number(t);
  if (!Number.isInteger(n) || n <= 0) return { value: null, error: 'Count must be a positive whole number, or left blank for unknown.' };
  return { value: n };
}

/** Parse an optional non-negative decimal (Decimal scalars travel as strings). */
export function parseDecimal(v: string): { value: string | null; error?: string } {
  const t = v.trim();
  if (t === '') return { value: null };
  if (!/^\d+(\.\d{1,6})?$/.test(t)) return { value: null, error: 'Enter a positive number (up to 6 decimal places).' };
  return { value: t };
}
