package worker

import "testing"

func TestFormatMoney(t *testing.T) {
	t.Parallel()

	cases := map[int64]string{
		0:     "0.00",
		5:     "0.05",
		99:    "0.99",
		100:   "1.00",
		6650:  "66.50",
		2400:  "24.00",
		12345: "123.45",
	}
	for cents, want := range cases {
		if got := formatMoney(cents); got != want {
			t.Errorf("formatMoney(%d) = %q, want %q", cents, got, want)
		}
	}
}
