/**
 * Receipt-review arithmetic (UI13): decides, from the draft's own numbers,
 * whether the amounts the extraction actually read add up to the total it
 * read. It is deliberately independent of the draft's needsReconciliation
 * flag — the backend sets that flag whenever a receipt draft is offered for
 * owner review, so it says "a person should check this", not "the totals
 * disagree". Only exact integer comparison can say the second thing.
 *
 * BigInt only, no floats: minor-unit strings may exceed Number.MAX_SAFE_INTEGER.
 * Signed values are accepted because a printed discount line is a negative
 * amount on the receipt.
 */

export interface ReceiptAmountInput {
  lines: readonly { amountMinor: string | null }[];
  totalMinor: string | null;
  taxMinor: string | null;
  shippingMinor: string | null;
}

export interface ReceiptAmountReview {
  /**
   * balanced   — every required amount was read and the read amounts sum to
   *              the read total exactly.
   * mismatch   — every required amount was read, but they do not sum to it.
   * incomplete — something needed for the comparison was not read (or was
   *              malformed), so no claim about balancing can be made.
   */
  state: "balanced" | "mismatch" | "incomplete";
  /** Sum of the readable item amounts plus any read overhead. "0" when the
   * comparison cannot be made — it is never shown in that state. */
  readMinor: string;
  /** read − total, signed; null when the comparison cannot be made. */
  differenceMinor: string | null;
}

/** A minor-unit integer string, optionally signed (discounts). Leading zeros
 * are tolerated because they carry no value; BigInt normalizes them. */
const SIGNED_MINOR = /^-?\d+$/;

function readMinor(value: string | null): bigint | null {
  if (value === null || !SIGNED_MINOR.test(value.trim())) return null;
  return BigInt(value.trim());
}

export function reviewReceiptAmounts(
  input: ReceiptAmountInput,
): ReceiptAmountReview {
  const incomplete = (): ReceiptAmountReview => ({
    state: "incomplete",
    readMinor: "0",
    differenceMinor: null,
  });

  const total = readMinor(input.totalMinor);
  if (total === null) return incomplete();
  // An itemized receipt with no lines at all proves nothing about the total.
  if (input.lines.length === 0) return incomplete();

  let sum = 0n;
  for (const line of input.lines) {
    const amount = readMinor(line.amountMinor);
    // A missing amount is not zero: treating it as zero would make a partial
    // read look like a balanced receipt.
    if (amount === null) return incomplete();
    sum += amount;
  }

  // Optional overhead: an unread value contributes nothing to the read sum,
  // which is a statement about what was read, not a claim that it is zero.
  // A value that was supplied but malformed makes the comparison unreliable.
  const tax = readMinor(input.taxMinor);
  if (tax === null) {
    if (input.taxMinor !== null) return incomplete();
  } else sum += tax;

  const shipping = readMinor(input.shippingMinor);
  if (shipping === null) {
    if (input.shippingMinor !== null) return incomplete();
  } else sum += shipping;

  const readMinorStr = sum.toString();
  if (sum === total)
    return { state: "balanced", readMinor: readMinorStr, differenceMinor: "0" };
  return {
    state: "mismatch",
    readMinor: readMinorStr,
    differenceMinor: (sum - total).toString(),
  };
}
