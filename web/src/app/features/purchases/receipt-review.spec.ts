import { describe, expect, it } from "vitest";
import { reviewReceiptAmounts } from "./receipt-review";
const receipt = {
  lines: [
    { amountMinor: "4999" },
    { amountMinor: "6500" },
    { amountMinor: "2999" },
    { amountMinor: "4999" },
  ],
  totalMinor: "21106",
  taxMinor: "1609",
  shippingMinor: null,
};
describe("receipt amount review", () => {
  it("balances the read item amounts plus tax even when shipping was not read", () => {
    expect(reviewReceiptAmounts(receipt)).toMatchObject({
      state: "balanced",
      readMinor: "21106",
      differenceMinor: "0",
    });
  });
  it("reports the exact discrepancy rather than misusing a review-required flag", () => {
    expect(
      reviewReceiptAmounts({ ...receipt, totalMinor: "21107" }),
    ).toMatchObject({
      state: "mismatch",
      readMinor: "21106",
      differenceMinor: "-1",
    });
  });
  it("does not equate a missing amount with zero", () => {
    expect(
      reviewReceiptAmounts({ ...receipt, lines: [{ amountMinor: null }] })
        .state,
    ).toBe("incomplete");
    expect(reviewReceiptAmounts({ ...receipt, totalMinor: null }).state).toBe(
      "incomplete",
    );
  });
  it("requires item amounts to call a read complete", () => {
    expect(
      reviewReceiptAmounts({
        lines: [],
        totalMinor: "0",
        taxMinor: null,
        shippingMinor: null,
      }).state,
    ).toBe("incomplete");
  });
  it("keeps zero readable and uses exact integer arithmetic beyond JS float precision", () => {
    expect(
      reviewReceiptAmounts({
        lines: [{ amountMinor: "0" }],
        totalMinor: "0",
        taxMinor: null,
        shippingMinor: null,
      }).state,
    ).toBe("balanced");
    expect(
      reviewReceiptAmounts({
        lines: [{ amountMinor: "9007199254740993" }],
        taxMinor: "1",
        shippingMinor: null,
        totalMinor: "9007199254740994",
      }).state,
    ).toBe("balanced");
  });
  it("includes printed discount amounts and refuses malformed values", () => {
    expect(
      reviewReceiptAmounts({
        lines: [{ amountMinor: "1000" }, { amountMinor: "-100" }],
        totalMinor: "900",
        taxMinor: null,
        shippingMinor: null,
      }).state,
    ).toBe("balanced");
    expect(reviewReceiptAmounts({ ...receipt, taxMinor: "16.09" }).state).toBe(
      "incomplete",
    );
  });
});
