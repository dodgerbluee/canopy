package journal

import "testing"

func TestManualRainReceiptHashSurvivesRainReviewUpgrade(t *testing.T) {
	// Existing clients retry receipts made before RainReviewID existed. A nil
	// review must serialize identically to that historical command shape.
	legacy := struct {
		Day      string
		AmountMm *string
		Note     *string
	}{Day: "2026-11-15", AmountMm: ptr("12.7")}
	current := RainInput{Day: legacy.Day, AmountMm: legacy.AmountMm}
	if requestHash(legacy) != requestHash(current) {
		t.Fatal("old manual-rain receipts would stop replaying after upgrade")
	}
	current.RainReviewID = ptr("review")
	if requestHash(legacy) == requestHash(current) {
		t.Fatal("review-confirmation hash must differ from manual rainfall")
	}
}
