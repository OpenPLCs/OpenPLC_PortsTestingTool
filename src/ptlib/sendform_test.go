package ptlib

import (
	"errors"
	"testing"
)

// The analyser send form's server-side rules (decision 171): a count of 1
// sends once whatever the interval; more needs the floor; each refusal names
// its box.
func TestSendFormRules(t *testing.T) {
	if _, d, err := ParseCANFrame("123", "112233"); err != nil || len(d) != 3 {
		t.Errorf("data without spaces: %v %v", d, err)
	}
	for _, c := range []struct {
		every, count int
		field        string
	}{
		{1, 1, ""}, {0, 0, ""}, {0, 1, ""}, {50, 2, ""}, {50, canCountMax, ""},
		{1, 2, "every"}, {0, 5, "every"}, {50, canCountMax + 1, "count"},
	} {
		err := checkRepeat(c.every, c.count, canEveryMin)
		var f SendFieldError
		got := ""
		if errors.As(err, &f) {
			got = f.Field
		} else if err != nil {
			got = "?"
		}
		if got != c.field {
			t.Errorf("every %d count %d: got %q, want %q", c.every, c.count, got, c.field)
		}
	}
	for in, field := range map[[2]string]string{{"800", "11"}: "id", {"123", "112233445566778899"}: "data", {"12G", ""}: "id", {"1", "1"}: "data"} {
		var f SendFieldError
		if _, _, err := ParseCANFrame(in[0], in[1]); !errors.As(err, &f) || f.Field != field {
			t.Errorf("%v: %v, want field %s", in, err, field)
		}
	}
}
