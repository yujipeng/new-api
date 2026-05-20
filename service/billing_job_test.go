package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestYesterdayInShanghai_StableAcrossUTCBoundary(t *testing.T) {
	loc, err := time.LoadLocation(billingJobTimeZoneName)
	if err != nil {
		t.Skipf("tzdata not available in test image: %v", err)
	}
	billingJobLocation = loc

	tcs := []struct {
		name string
		now  time.Time
		want string
	}{
		{
			name: "Shanghai midnight (UTC 16:00 previous day)",
			now:  time.Date(2026, 5, 20, 16, 0, 0, 0, time.UTC),
			want: "2026-05-20",
		},
		{
			name: "Shanghai 03:00 (post-cutoff, prev day not yet rolled)",
			now:  time.Date(2026, 5, 20, 19, 0, 0, 0, time.UTC),
			want: "2026-05-20",
		},
		{
			name: "Shanghai 23:59 of same day",
			now:  time.Date(2026, 5, 21, 15, 59, 0, 0, time.UTC),
			want: "2026-05-20",
		},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			got := YesterdayInShanghai(tc.now)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDayBoundsInShanghai_24HourSpan(t *testing.T) {
	loc, err := time.LoadLocation(billingJobTimeZoneName)
	if err != nil {
		t.Skipf("tzdata not available: %v", err)
	}
	billingJobLocation = loc

	start, end := dayBoundsInShanghai("2026-05-19")
	assert.Equal(t, int64(24*3600), end-start, "exactly 24h span")

	expectStart := time.Date(2026, 5, 19, 0, 0, 0, 0, loc).Unix()
	assert.Equal(t, expectStart, start)
}

func TestFirstCSV(t *testing.T) {
	assert.Equal(t, "", firstCSV(""))
	assert.Equal(t, "alpha", firstCSV("alpha"))
	assert.Equal(t, "alpha", firstCSV("alpha,beta,gamma"))
	assert.Equal(t, "alpha", firstCSV("alpha,"))
}
