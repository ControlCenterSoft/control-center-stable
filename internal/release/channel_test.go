package release

import (
	"errors"
	"testing"
)

func TestPromotionPath(t *testing.T) {
	cases := []struct {
		from Channel
		to   Channel
	}{
		{ChannelDevelopment, ChannelCandidate},
		{ChannelCandidate, ChannelStable},
		{ChannelStable, ChannelStable},
	}
	for _, tc := range cases {
		if err := ValidatePromotion(tc.from, tc.to); err != nil {
			t.Fatalf("ValidatePromotion(%q, %q) error = %v", tc.from, tc.to, err)
		}
	}
}

func TestPromotionRejectsSkippingCandidate(t *testing.T) {
	err := ValidatePromotion(ChannelDevelopment, ChannelStable)
	if !errors.Is(err, ErrInvalidPromotion) {
		t.Fatalf("error = %v, want ErrInvalidPromotion", err)
	}
}

func TestPromotionRejectsRollback(t *testing.T) {
	err := ValidatePromotion(ChannelStable, ChannelCandidate)
	if !errors.Is(err, ErrInvalidPromotion) {
		t.Fatalf("error = %v, want ErrInvalidPromotion", err)
	}
}
